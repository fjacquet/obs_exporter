package ecs

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestDTCollect(t *testing.T) {
	dtSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(dtStatXML))
	}))
	defer dtSrv.Close()
	pingSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(pingXML))
	}))
	defer pingSrv.Close()

	d := &DT{
		httpClient: &http.Client{Timeout: 5 * time.Second},
		dtURL:      func(string) string { return dtSrv.URL },
		pingURL:    func(string) string { return pingSrv.URL },
	}
	samples, err := d.Collect(context.Background(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}

	n1 := Label{"node", "10.0.0.1"}
	mustSample(t, samples, "ecs_node_dt_up", 1, n1)
	mustSample(t, samples, "ecs_node_dt_total", 128, n1)
	mustSample(t, samples, "ecs_node_dt_unready", 2, n1)
	mustSample(t, samples, "ecs_node_dt_unknown", 1, n1)
	mustSample(t, samples, "ecs_node_active_connections", 42, n1)
	// Both inventory nodes get scraped.
	mustSample(t, samples, "ecs_node_dt_up", 1, Label{"node", "10.0.0.2"})
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
	n1 := Label{"node", "10.0.0.1"}
	mustSample(t, samples, "ecs_node_dt_up", 0, n1)
	if _, ok := findSample(samples, "ecs_node_dt_total", n1); ok {
		t.Error("dt_total should be absent when the node scrape fails")
	}
}
