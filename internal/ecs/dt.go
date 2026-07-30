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
// connections in parallel. A node failure degrades to ecs_node_dt_up=0 for that
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
		// A cluster that publishes only one of the two addresses is still worth
		// scraping on the one it has; only a node with neither is unreachable.
		node := dtNode{
			label:  cmp.Or(n.Nodename, n.MgmtIP, n.DataIP),
			mgmtIP: cmp.Or(n.MgmtIP, n.DataIP),
			dataIP: cmp.Or(n.DataIP, n.MgmtIP),
		}
		if node.mgmtIP == "" && node.dataIP == "" {
			continue
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

func (d *DT) collectNode(ctx context.Context, cluster string, n dtNode) []Sample {
	nodeLabel := []Label{{Key: "node", Value: n.label}}
	up := 1.0

	var dt dtStatResp
	if err := d.fetchXML(ctx, d.dtURL(n.mgmtIP), &dt); err != nil {
		log.WithFields(log.Fields{"cluster": cluster, "node": n.label, "host": n.mgmtIP, "err": err}).
			Debug("DT stats scrape failed")
		up = 0
	}

	out := []Sample{{Name: "ecs_node_dt_up", Labels: nodeLabel, Value: up}}
	if up == 1 {
		out = append(out,
			Sample{Name: "ecs_node_dt_total", Labels: nodeLabel, Value: dt.TotalDT},
			Sample{Name: "ecs_node_dt_unready", Labels: nodeLabel, Value: dt.UnreadyDT},
			Sample{Name: "ecs_node_dt_unknown", Labels: nodeLabel, Value: dt.UnknownDT},
		)
	}

	var ping pingResp
	if err := d.fetchXML(ctx, d.pingURL(n.dataIP), &ping); err != nil {
		log.WithFields(log.Fields{"cluster": cluster, "node": n.label, "host": n.dataIP, "err": err}).
			Debug("ping scrape failed")
		return out
	}
	return append(out, Sample{Name: "ecs_node_active_connections", Labels: nodeLabel, Value: ping.Value})
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
