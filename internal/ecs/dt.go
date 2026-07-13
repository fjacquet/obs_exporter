package ecs

import (
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
// for v1 parity; enable per cluster with collectDT. Node addresses come from the
// management API's /vdc/nodes inventory.
type DT struct {
	httpClient *http.Client
	// dtURL/pingURL build the node-local endpoint URLs; tests override them to
	// point at httptest servers.
	dtURL   func(node string) string
	pingURL func(node string) string
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
		dtURL:      func(node string) string { return fmt.Sprintf("http://%s:%d/stats/dt/DTInitStat", node, cl.DTPort) },
		pingURL:    func(node string) string { return fmt.Sprintf("https://%s:%d/?ping", node, cl.ObjPort) },
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
		node := n.MgmtIP
		if node == "" {
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

func (d *DT) collectNode(ctx context.Context, cluster, node string) []Sample {
	nodeLabel := []Label{{Key: "node", Value: node}}
	up := 1.0

	var dt dtStatResp
	if err := d.fetchXML(ctx, d.dtURL(node), &dt); err != nil {
		log.WithFields(log.Fields{"cluster": cluster, "node": node, "err": err}).Debug("DT stats scrape failed")
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
	if err := d.fetchXML(ctx, d.pingURL(node), &ping); err != nil {
		log.WithFields(log.Fields{"cluster": cluster, "node": node, "err": err}).Debug("ping scrape failed")
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
