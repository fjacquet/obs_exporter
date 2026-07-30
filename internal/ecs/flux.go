package ecs

import (
	"cmp"
	"context"
	"net"
	"strings"

	"github.com/fjacquet/obs_exporter/internal/ecsclient"
)

// Flux is the opt-in collector for metric families the management API does
// not serve, sourced from the cluster's Flux/InfluxDB monitoring store. The
// query table and emission logic arrive in the following task; registering
// the collector here — even inert — is what makes Registry's arbitration
// decision (which source owns the three contested per-node names) testable
// now, before either half of the query/emission work exists.
type Flux struct{}

// Name identifies this collector in ecs_collector_up.
func (Flux) Name() string { return "flux" }

// Collect is a stub: it issues no requests and returns no samples. Replaced
// in the next task with the Flux query table and Sample emission.
func (Flux) Collect(ctx context.Context, c ecsclient.Client) ([]Sample, error) {
	return nil, nil
}

// nodeMapper resolves a Flux `host` tag onto the /vdc/nodes nodename every other
// collector uses as the node label. Flux reports host as an FQDN in the 4.3
// reference's example and node_id as a UUID, and neither is guaranteed to equal
// nodename, so each inventory node is indexed under every identifier it
// publishes. A series that does not join is worse than an absent one.
type nodeMapper struct {
	byKey map[string]string
}

// newNodeMapper reads the node inventory and indexes it.
func newNodeMapper(ctx context.Context, c ecsclient.Client) (*nodeMapper, error) {
	var inv vdcNodesResp
	if err := c.Get(ctx, pathVdcNodes, &inv); err != nil {
		return nil, err
	}
	m := &nodeMapper{byKey: map[string]string{}}
	for _, n := range inv.Node {
		// The same precedence the DT collector uses, so both label a node identically.
		label := cmp.Or(n.Nodename, n.MgmtIP, n.DataIP)
		if label == "" {
			continue
		}
		for _, key := range []string{n.Nodename, shortHost(n.Nodename), n.MgmtIP, n.DataIP} {
			if key == "" {
				continue
			}
			k := strings.ToLower(key)
			// A key two different nodes both claim cannot identify either of them.
			// Blanking it makes lookup fail, so the row is dropped and counted,
			// rather than silently attributed to whichever node was indexed last.
			if prior, seen := m.byKey[k]; seen && prior != label {
				m.byKey[k] = ""
				continue
			}
			m.byKey[k] = label
		}
	}
	return m, nil
}

// lookup resolves a Flux host tag, trying it whole and then truncated at the
// first dot, so a qualified name joins a bare nodename.
func (m *nodeMapper) lookup(host string) (string, bool) {
	h := strings.ToLower(strings.TrimSpace(host))
	if n, ok := m.byKey[h]; ok && n != "" {
		return n, true
	}
	if n, ok := m.byKey[shortHost(h)]; ok && n != "" {
		return n, true
	}
	return "", false
}

// shortHost truncates an FQDN at its first dot. An IP address is returned
// unchanged: truncating one yields a meaningless key that could collide across
// nodes sharing a prefix.
func shortHost(h string) string {
	if net.ParseIP(h) != nil {
		return h
	}
	if i := strings.Index(h, "."); i >= 0 {
		return h[:i]
	}
	return h
}
