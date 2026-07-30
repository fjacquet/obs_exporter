package ecs

import (
	"cmp"
	"context"
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/fjacquet/obs_exporter/internal/config"
	"github.com/fjacquet/obs_exporter/internal/ecsclient"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

// dtStatResp models the node-local DT stats XML (GET http://<node>:9101/stats/dt/DTInitStat).
type dtStatResp struct {
	TotalDT   float64 `xml:"entry>total_dt_num"`
	UnreadyDT float64 `xml:"entry>unready_dt_num"`
	UnknownDT float64 `xml:"entry>unknown_dt_num"`
}

// pingResp models the object-port ping XML (GET https://<node>:9021/?ping); Value
// is the node's current active-connection count.
type pingResp struct {
	Value float64 `xml:"PingItem>Value"`
}

// DT is the opt-in legacy collector for node-local directory-table stats and
// active connections. Both endpoints are UNDOCUMENTED internal ECS services kept
// for v1 parity; enable per cluster with collectDT. Node addressing comes from
// the management API's /vdc/nodes inventory.
//
// The two ports do NOT share an address on a network-segmented cluster (the
// layout Dell recommends for production since ECS 3.8, and the default from 4.x
// on): the object port answers on the node's data network and the DT stats port
// on a private link-local fabric VLAN. See dtNode for which address each scrape
// uses and docs/metrics.md for the reachability caveat this leaves.
type DT struct {
	httpClient *http.Client
	// dtURL/pingURL build the node-local endpoint URLs from the host each port
	// answers on; tests override them to point at httptest servers.
	dtURL   func(host string) string
	pingURL func(host string) string
}

// dtNode is one node's addressing for the two node-local scrapes.
type dtNode struct {
	// label identifies the node in the emitted metrics. It is the inventory's
	// nodename, which is the same identifier the dashboard collectors expose as
	// displayName — so DT series join with the rest of the per-node metrics
	// instead of forming a disjoint IP-keyed set.
	label string
	// mgmtIP hosts the DT stats port, dataIP the object port. On clusters that
	// carry both networks on one interface these are equal.
	mgmtIP string
	dataIP string
}

// NewDT builds the DT collector for one cluster's ports/TLS settings.
func NewDT(cl config.Cluster) *DT {
	transport := http.DefaultTransport
	if cl.InsecureSkipVerify.Bool() {
		transport = &http.Transport{TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cl.InsecureSkipVerify.Bool(), // operator opt-in for self-signed ECS certs
			MinVersion:         tls.VersionTLS12,
		}}
	}
	return &DT{
		httpClient: &http.Client{Transport: transport, Timeout: 30 * time.Second},
		dtURL:      func(host string) string { return fmt.Sprintf("http://%s:%d/stats/dt/DTInitStat", host, cl.DTPort) },
		pingURL:    func(host string) string { return fmt.Sprintf("https://%s:%d/?ping", host, cl.ObjPort) },
	}
}

// Name identifies this collector in ecs_collector_up.
func (*DT) Name() string { return "dt" }

// Collect lists the cluster's nodes and scrapes each node's DT stats and active
// connections in parallel. A node failure degrades to ecs_node_scrape_up=0 for that
// node only.
func (d *DT) Collect(ctx context.Context, c ecsclient.Client) ([]Sample, error) {
	var inv vdcNodesResp
	if err := c.Get(ctx, pathVdcNodes, &inv); err != nil {
		return nil, err
	}

	var mu sync.Mutex
	var out []Sample
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for _, n := range inv.Node {
		// Guard the raw inventory, not the derived fields: after the fallbacks
		// below the two addresses are empty together, so testing them would read
		// as two conditions where there is one.
		if n.MgmtIP == "" && n.DataIP == "" {
			continue
		}
		// A cluster that publishes only one of the two addresses is still worth
		// scraping on the one it has.
		node := dtNode{
			label:  cmp.Or(n.Nodename, n.MgmtIP, n.DataIP),
			mgmtIP: cmp.Or(n.MgmtIP, n.DataIP),
			dataIP: cmp.Or(n.DataIP, n.MgmtIP),
		}
		g.Go(func() error {
			samples := d.collectNode(gctx, c.Name(), node)
			mu.Lock()
			out = append(out, samples...)
			mu.Unlock()
			return nil // graceful per-node degradation
		})
	}
	_ = g.Wait()
	return out, nil
}

// scrapeUp is the per-endpoint reachability metric. The two node-local ports sit
// on different networks, so one up-signal cannot describe both: on the segmented
// layout the DT port is unreachable while the object port answers normally, and
// nothing would report the reverse. One name plus {endpoint} makes each failure
// visible on its own and leaves room for a third port without a new metric name
// (ADR-0012).
const scrapeUp = "ecs_node_scrape_up"

func (d *DT) collectNode(ctx context.Context, cluster string, n dtNode) []Sample {
	node := Label{Key: "node", Value: n.label}

	var out []Sample
	var dt dtStatResp
	dtErr := d.fetchXML(ctx, d.dtURL(n.mgmtIP), &dt)
	if dtErr != nil {
		log.WithFields(log.Fields{"cluster": cluster, "node": n.label, "host": n.mgmtIP, "err": dtErr}).
			Debug("DT stats scrape failed")
	}
	out = append(out, Sample{Name: scrapeUp, Labels: []Label{node, {Key: "endpoint", Value: "dt"}}, Value: upValue(dtErr)})
	if dtErr == nil {
		out = append(out,
			Sample{Name: "ecs_node_dt_total", Labels: []Label{node}, Value: dt.TotalDT},
			Sample{Name: "ecs_node_dt_unready", Labels: []Label{node}, Value: dt.UnreadyDT},
			Sample{Name: "ecs_node_dt_unknown", Labels: []Label{node}, Value: dt.UnknownDT},
		)
	}

	var ping pingResp
	pingErr := d.fetchXML(ctx, d.pingURL(n.dataIP), &ping)
	if pingErr != nil {
		log.WithFields(log.Fields{"cluster": cluster, "node": n.label, "host": n.dataIP, "err": pingErr}).
			Debug("ping scrape failed")
	}
	out = append(out, Sample{Name: scrapeUp, Labels: []Label{node, {Key: "endpoint", Value: "object"}}, Value: upValue(pingErr)})
	if pingErr == nil {
		out = append(out, Sample{Name: "ecs_node_active_connections", Labels: []Label{node}, Value: ping.Value})
	}
	return out
}

// upValue maps a scrape outcome onto the 1/0 an up-metric carries.
func upValue(err error) float64 {
	if err != nil {
		return 0
	}
	return 1
}

func (d *DT) fetchXML(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return err
	}
	return xml.Unmarshal(body, out)
}
