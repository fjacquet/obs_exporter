package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// fluxFixtures maps a measurement name to its embedded capture. A measurement
// absent from this map answers with the empty envelope a real cluster returns
// for a measurement it does not carry.
var fluxFixtures = map[string]string{
	"cpu":                             "fixtures/flux_cpu.json",
	"mem":                             "fixtures/flux_mem.json",
	"net":                             "fixtures/flux_net.json",
	"dtquery_dt_status":               "fixtures/flux_dt_status.json",
	"dtquery_dt_dist_host_dt_node_id": "fixtures/flux_dt_dist.json",
	"statDataHead_performance_internal_transactions": "fixtures/flux_transactions.json",
	"statDataHead_performance_internal_throughput":   "fixtures/flux_throughput.json",
	"statDataHead_performance_internal_latency":      "fixtures/flux_latency.json",
	"cq_performance_transaction":                     "fixtures/flux_cq_transaction.json",
	"cq_performance_throughput":                      "fixtures/flux_cq_throughput.json",
}

// emptyEnvelope is what a live 4.3 answers for a measurement the store does not
// carry: HTTP 200, one Series with every field null.
const emptyEnvelope = `{"Series":[{"Datatypes":null,"Columns":null,"Values":null}]}`

// fluxQueryBody is the shape of the exporter's POST body. ecsclient.Post JSON-
// encodes the map the collector builds (map[string]string{"query": ...}), so
// the wire body is a JSON object, not the raw Flux script.
type fluxQueryBody struct {
	Query string `json:"query"`
}

// fluxHandler serves POST /flux/api/external/v2/query. All queries share one
// path, so the measurement is read out of the request body — the same routing
// the collector's own tests use. The body must be JSON-decoded before matching:
// encoding/json escapes every `"` in the query script, so a measurement name
// quoted in the script (`r._measurement == "cpu"`) never appears as a literal
// `"cpu"` substring in the raw request bytes — only in the decoded query string.
func fluxHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("X-SDS-AUTH-TOKEN") != mockToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var q fluxQueryBody
		if err := json.Unmarshal(body, &q); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		var file string
		for measurement, f := range fluxFixtures {
			if strings.Contains(q.Query, `"`+measurement+`"`) {
				file = f
				break
			}
		}
		if file == "" {
			_, _ = io.WriteString(w, emptyEnvelope)
			return
		}
		raw, err := fixtures.ReadFile(file)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		out, err := freshenTimes(raw, time.Now().UTC())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(out)
	}
}

// freshenTimes rewrites every _time cell to now.
//
// The fixtures are real captures taken at a fixed instant in July 2026, and the
// collector drops points older than fluxMaxAge — so replayed literally, every
// demo row would be correctly discarded as stale and make demo would show an
// empty dashboard. Only _time is touched; values, columns and datatypes stay as
// the cluster wrote them.
func freshenTimes(raw []byte, now time.Time) ([]byte, error) {
	var env struct {
		Series []struct {
			Datatypes []string   `json:"Datatypes"`
			Columns   []string   `json:"Columns"`
			Values    [][]string `json:"Values"`
		} `json:"Series"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	stamp := now.Format(time.RFC3339Nano)
	for _, s := range env.Series {
		for i, col := range s.Columns {
			if col != "_time" {
				continue
			}
			for _, row := range s.Values {
				if i < len(row) {
					row[i] = stamp
				}
			}
		}
	}
	return json.Marshal(env)
}
