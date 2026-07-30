package ecs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"
)

const dtStatXML = `<?xml version="1.0" encoding="UTF-8"?>
<stats>
  <entry><total_dt_num>128</total_dt_num><unready_dt_num>2</unready_dt_num><unknown_dt_num>1</unknown_dt_num></entry>
</stats>`

const pingXML = `<?xml version="1.0" encoding="UTF-8"?>
<PingList xmlns="http://www.emc.com">
  <PingItem><Name>LOAD_FACTOR</Name><Value>42</Value><Status>OK</Status></PingItem>
</PingList>`

// hostRecorder captures the hosts a URL builder was called with. The DT
// collector scrapes nodes concurrently, so recording needs a lock.
type hostRecorder struct {
	mu    sync.Mutex
	hosts []string
}

func (r *hostRecorder) record(host string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hosts = append(r.hosts, host)
}

func (r *hostRecorder) seen(host string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.hosts, host)
}

func TestDTCollect(t *testing.T) {
	dtSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(dtStatXML))
	}))
	defer dtSrv.Close()
	pingSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(pingXML))
	}))
	defer pingSrv.Close()

	var dtHosts, pingHosts hostRecorder
	d := &DT{
		httpClient: &http.Client{Timeout: 5 * time.Second},
		dtURL:      func(host string) string { dtHosts.record(host); return dtSrv.URL },
		pingURL:    func(host string) string { pingHosts.record(host); return pingSrv.URL },
	}
	samples, err := d.Collect(context.Background(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}

	// Nodes are labeled by the inventory's nodename, which is the same
	// identifier the dashboard nodes collector emits as displayName — the two
	// metric sets must join on {node=...}.
	n1 := Label{"node", "supr01-r01"}
	mustSample(t, samples, "ecs_node_scrape_up", 1, n1, Label{"endpoint", "dt"})
	mustSample(t, samples, "ecs_node_scrape_up", 1, n1, Label{"endpoint", "object"})
	mustSample(t, samples, "ecs_node_dt_total", 128, n1)
	mustSample(t, samples, "ecs_node_dt_unready", 2, n1)
	mustSample(t, samples, "ecs_node_dt_unknown", 1, n1)
	mustSample(t, samples, "ecs_node_active_connections", 42, n1)
	// Both inventory nodes get scraped.
	mustSample(t, samples, "ecs_node_scrape_up", 1, Label{"node", "supr01-r02"}, Label{"endpoint", "dt"})

	// The object port answers on the data network, the DT stats port on the
	// management address: scraping both at mgmt_ip silently loses connections
	// on any network-segmented cluster.
	if !dtHosts.seen("10.0.0.1") {
		t.Errorf("DT stats scraped hosts %v, want the node's mgmt_ip 10.0.0.1", dtHosts.hosts)
	}
	if !pingHosts.seen("10.1.0.1") {
		t.Errorf("ping scraped hosts %v, want the node's data_ip 10.1.0.1", pingHosts.hosts)
	}
	if pingHosts.seen("10.0.0.1") {
		t.Errorf("ping scraped the mgmt_ip; hosts = %v", pingHosts.hosts)
	}
}

func TestDTCollectFallsBackToMgmtIP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(pingXML))
	}))
	defer srv.Close()

	// A node inventory without data_ip: a cluster carrying both networks on one
	// interface must still get its object port scraped.
	c := mockClient(t)
	c.Responses[pathVdcNodes] = `{"node":[{"nodename":"supr01-r01","mgmt_ip":"10.0.0.1"}]}`

	var pingHosts hostRecorder
	d := &DT{
		httpClient: &http.Client{Timeout: 5 * time.Second},
		dtURL:      func(string) string { return srv.URL },
		pingURL:    func(host string) string { pingHosts.record(host); return srv.URL },
	}
	if _, err := d.Collect(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if !pingHosts.seen("10.0.0.1") {
		t.Errorf("ping scraped hosts %v, want the mgmt_ip fallback 10.0.0.1", pingHosts.hosts)
	}
}

func TestDTCollectLabelsByIPWithoutNodename(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(pingXML))
	}))
	defer srv.Close()

	c := mockClient(t)
	c.Responses[pathVdcNodes] = `{"node":[{"mgmt_ip":"10.0.0.1","data_ip":"10.1.0.1"}]}`

	d := &DT{
		httpClient: &http.Client{Timeout: 5 * time.Second},
		dtURL:      func(string) string { return srv.URL },
		pingURL:    func(string) string { return srv.URL },
	}
	samples, err := d.Collect(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	// No nodename to join on: the management IP keeps the series identifiable
	// rather than collapsing every node onto an empty label.
	mustSample(t, samples, "ecs_node_active_connections", 42, Label{"node", "10.0.0.1"})
}

func TestDTCollectSkipsNodeWithoutAddress(t *testing.T) {
	c := mockClient(t)
	// A named node with no address at all cannot be scraped; emitting
	// a scrape_up=0 for it would report a healthy node as down.
	c.Responses[pathVdcNodes] = `{"node":[{"nodename":"supr01-r01"}]}`

	d := &DT{
		httpClient: &http.Client{Timeout: 5 * time.Second},
		dtURL:      func(string) string { t.Error("addressless node was scraped"); return "" },
		pingURL:    func(string) string { t.Error("addressless node was scraped"); return "" },
	}
	samples, err := d.Collect(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 0 {
		t.Errorf("got %d samples for an addressless node, want none: %v", len(samples), samples)
	}
}

func TestDTCollectNodeDown(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer down.Close()

	d := &DT{
		httpClient: &http.Client{Timeout: 5 * time.Second},
		dtURL:      func(string) string { return down.URL },
		pingURL:    func(string) string { return down.URL },
	}
	samples, err := d.Collect(context.Background(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	n1 := Label{"node", "supr01-r01"}
	mustSample(t, samples, "ecs_node_scrape_up", 0, n1, Label{"endpoint", "dt"})
	// The object port is a separate network and gets its own signal.
	mustSample(t, samples, "ecs_node_scrape_up", 0, n1, Label{"endpoint", "object"})
	if _, ok := findSample(samples, "ecs_node_dt_total", n1); ok {
		t.Error("dt_total should be absent when the node scrape fails")
	}
}

// The 4.3 REST reference documents PingList as 0-* PingItem elements with no
// guaranteed ordering, and &item=load-factor changes which are present. Ordering
// must not decide which item the metrics read.
const pingReversedXML = `<?xml version="1.0" encoding="UTF-8"?>
<PingList xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <PingItem><Name>MAINTENANCE_MODE</Name><Status>OFF</Status><Text>Data Node is Available</Text></PingItem>
  <PingItem><Name>LOAD_FACTOR</Name><Value>7</Value></PingItem>
</PingList>`

// collectPing runs the DT collector against one ping payload, stubbing the DT
// stats port so only the ping path is under test.
func collectPing(t *testing.T, body string) []Sample {
	t.Helper()
	pingSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(pingSrv.Close)
	dtSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(dtStatXML))
	}))
	t.Cleanup(dtSrv.Close)

	d := &DT{
		httpClient: &http.Client{Timeout: 5 * time.Second},
		dtURL:      func(string) string { return dtSrv.URL },
		pingURL:    func(string) string { return pingSrv.URL },
	}
	samples, err := d.Collect(context.Background(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	return samples
}

func TestDTPingMatchesItemsByName(t *testing.T) {
	samples := collectPing(t, pingReversedXML)
	n1 := Label{"node", "supr01-r01"}
	// A positional decode reads MAINTENANCE_MODE's (absent) Value here and
	// reports either nothing or the wrong item.
	mustSample(t, samples, "ecs_node_active_connections", 7, n1)
	mustSample(t, samples, "ecs_node_maintenance_mode", 0, n1)
}

func TestDTPingMaintenanceModeStatuses(t *testing.T) {
	// UNKNOWN is documented alongside ON and OFF. A status the cluster itself
	// cannot determine must not be reported as 0 — "not in maintenance" is the
	// reading an operator would act on.
	for _, tc := range []struct {
		status string
		want   float64
		absent bool
	}{
		{status: "OFF", want: 0},
		{status: "ON", want: 1},
		{status: "UNKNOWN", absent: true},
		{status: "wedged", absent: true},
	} {
		t.Run(tc.status, func(t *testing.T) {
			samples := collectPing(t, `<PingList><PingItem><Name>MAINTENANCE_MODE</Name><Status>`+tc.status+`</Status></PingItem></PingList>`)
			s, ok := findSample(samples, "ecs_node_maintenance_mode", Label{"node", "supr01-r01"})
			if tc.absent {
				if ok {
					t.Fatalf("status %q emitted %v, want an absent sample", tc.status, s.Value)
				}
				return
			}
			if !ok {
				t.Fatalf("status %q emitted no sample, want %v", tc.status, tc.want)
			}
			if s.Value != tc.want {
				t.Errorf("status %q = %v, want %v", tc.status, s.Value, tc.want)
			}
		})
	}
}

func TestDTPingAbsentItemsEmitNothing(t *testing.T) {
	// An empty PingList still means the port answered: reachability is reported,
	// the two item metrics are not invented.
	samples := collectPing(t, `<PingList></PingList>`)
	n1 := Label{"node", "supr01-r01"}
	mustSample(t, samples, "ecs_node_scrape_up", 1, n1, Label{"endpoint", "object"})
	if _, ok := findSample(samples, "ecs_node_active_connections", n1); ok {
		t.Error("active_connections emitted for a payload with no LOAD_FACTOR item")
	}
	if _, ok := findSample(samples, "ecs_node_maintenance_mode", n1); ok {
		t.Error("maintenance_mode emitted for a payload with no MAINTENANCE_MODE item")
	}
}

func TestDTPingUnparseableLoadFactorIsAbsent(t *testing.T) {
	samples := collectPing(t, `<PingList><PingItem><Name>LOAD_FACTOR</Name><Value>N/A</Value></PingItem></PingList>`)
	if _, ok := findSample(samples, "ecs_node_active_connections", Label{"node", "supr01-r01"}); ok {
		t.Error("active_connections emitted for an unparseable Value")
	}
}
