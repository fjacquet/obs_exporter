package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newFluxServer starts an httptest server serving only the Flux route, the
// same way main.go wires it — a lean stand-in for the full mux so these tests
// exercise the real HTTP path rather than calling fluxHandler's closure directly.
func newFluxServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/flux/api/external/v2/query", fluxHandler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// postFluxQuery POSTs a JSON body shaped exactly like ecsclient.Post encodes
// it (map[string]string{"query": script}), which is what turns the request
// into the wire format the real exporter sends: encoding/json escapes every
// `"` inside script, so a naive substring match against the raw request bytes
// would never find a quoted measurement name.
func postFluxQuery(t *testing.T, srv *httptest.Server, token, script string) *http.Response {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": script})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/flux/api/external/v2/query", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("X-SDS-AUTH-TOKEN", token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// fluxScriptFor renders a minimal but realistic Flux script naming
// measurement, the same shape fluxQuery.script produces in internal/ecs/flux.go.
func fluxScriptFor(measurement string) string {
	return "from(bucket:\"monitoring_op\")\n  |> range(start: -15m)\n  |> filter(fn: (r) => r._measurement == \"" + measurement + "\")\n  |> last()"
}

// fluxEnvelope mirrors the shape freshenTimes decodes into, for assertions.
type fluxEnvelope struct {
	Series []struct {
		Datatypes []string   `json:"Datatypes"`
		Columns   []string   `json:"Columns"`
		Values    [][]string `json:"Values"`
	} `json:"Series"`
}

func TestFluxHandlerRoutesKnownMeasurementToItsFixture(t *testing.T) {
	// Every measurement fluxQueries actually asks for must resolve to a
	// fixture, driven the same way the real exporter's request body is built:
	// JSON-encoded, so the measurement name arrives escaped (\"cpu\", not
	// "cpu") in the raw bytes and only a JSON-decoded match finds it.
	srv := newFluxServer(t)
	for measurement, file := range fluxFixtures {
		t.Run(measurement, func(t *testing.T) {
			resp := postFluxQuery(t, srv, mockToken, fluxScriptFor(measurement))
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			got, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			want, err := fixtures.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var wantEnv, gotEnv fluxEnvelope
			if err := json.Unmarshal(want, &wantEnv); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			if err := json.Unmarshal(got, &gotEnv); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if len(gotEnv.Series) != len(wantEnv.Series) {
				t.Fatalf("Series count = %d, want %d", len(gotEnv.Series), len(wantEnv.Series))
			}
			for i := range wantEnv.Series {
				if !equalStrings(gotEnv.Series[i].Datatypes, wantEnv.Series[i].Datatypes) {
					t.Errorf("Datatypes = %v, want %v", gotEnv.Series[i].Datatypes, wantEnv.Series[i].Datatypes)
				}
				if !equalStrings(gotEnv.Series[i].Columns, wantEnv.Series[i].Columns) {
					t.Errorf("Columns = %v, want %v", gotEnv.Series[i].Columns, wantEnv.Series[i].Columns)
				}
				if len(gotEnv.Series[i].Values) != len(wantEnv.Series[i].Values) {
					t.Errorf("Values row count = %d, want %d", len(gotEnv.Series[i].Values), len(wantEnv.Series[i].Values))
				}
			}
		})
	}
}

func TestFluxHandlerUnknownMeasurementGetsEmptyEnvelope(t *testing.T) {
	// A measurement the collector might query that this mock does not carry
	// must still answer 200 with the real empty-envelope shape, not an error —
	// that is what lets Collect distinguish "cluster has nothing" from "cluster
	// is unreachable".
	srv := newFluxServer(t)
	resp := postFluxQuery(t, srv, mockToken, fluxScriptFor("no_such_measurement"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != emptyEnvelope {
		t.Errorf("body = %s, want %s", got, emptyEnvelope)
	}
	var env fluxEnvelope
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Series) != 1 || env.Series[0].Datatypes != nil || env.Series[0].Columns != nil || env.Series[0].Values != nil {
		t.Errorf("empty envelope = %+v, want one Series with everything null", env.Series)
	}
}

func TestFluxHandlerRequiresSessionToken(t *testing.T) {
	srv := newFluxServer(t)
	resp := postFluxQuery(t, srv, "", fluxScriptFor("cpu"))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestFluxHandlerRejectsGet(t *testing.T) {
	srv := newFluxServer(t)
	resp, err := srv.Client().Get(srv.URL + "/flux/api/external/v2/query")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
	if got := resp.Header.Get("Allow"); got != http.MethodPost {
		t.Errorf("Allow header = %q, want %q", got, http.MethodPost)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFreshenTimesRewritesTimeColumnOnly(t *testing.T) {
	raw, err := fixtures.ReadFile("fixtures/flux_cpu.json")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC)
	out, err := freshenTimes(raw, now)
	if err != nil {
		t.Fatal(err)
	}

	var before, after fluxEnvelope
	if err := json.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out, &after); err != nil {
		t.Fatal(err)
	}
	if len(before.Series) != len(after.Series) {
		t.Fatalf("Series count changed: %d -> %d", len(before.Series), len(after.Series))
	}
	wantStamp := now.Format(time.RFC3339Nano)
	for si := range before.Series {
		if !equalStrings(before.Series[si].Datatypes, after.Series[si].Datatypes) {
			t.Errorf("Datatypes changed: %v -> %v", before.Series[si].Datatypes, after.Series[si].Datatypes)
		}
		if !equalStrings(before.Series[si].Columns, after.Series[si].Columns) {
			t.Errorf("Columns changed: %v -> %v", before.Series[si].Columns, after.Series[si].Columns)
		}
		timeCol := -1
		for i, c := range before.Series[si].Columns {
			if c == "_time" {
				timeCol = i
			}
		}
		if timeCol == -1 {
			t.Fatalf("fixture has no _time column: %v", before.Series[si].Columns)
		}
		if len(before.Series[si].Values) != len(after.Series[si].Values) {
			t.Fatalf("Values row count changed: %d -> %d", len(before.Series[si].Values), len(after.Series[si].Values))
		}
		for ri, row := range before.Series[si].Values {
			gotRow := after.Series[si].Values[ri]
			if len(gotRow) != len(row) {
				t.Fatalf("row %d width changed: %d -> %d", ri, len(row), len(gotRow))
			}
			for ci := range row {
				if ci == timeCol {
					if gotRow[ci] != wantStamp {
						t.Errorf("row %d _time = %q, want %q", ri, gotRow[ci], wantStamp)
					}
					continue
				}
				if gotRow[ci] != row[ci] {
					t.Errorf("row %d col %d changed: %q -> %q, want unchanged", ri, ci, row[ci], gotRow[ci])
				}
			}
		}
	}
}

func TestFreshenTimesEmptyEnvelopeRoundTripsUnchangedInShape(t *testing.T) {
	// flux_empty.json has Datatypes, Columns and Values all null. The round
	// trip must preserve that shape: no Series added or dropped, no field
	// promoted from null to an empty non-nil slice that would read differently
	// to a client distinguishing "no rows" from "no columns at all".
	raw, err := fixtures.ReadFile("fixtures/flux_empty.json")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC)
	out, err := freshenTimes(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	var env fluxEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Series) != 1 {
		t.Fatalf("Series count = %d, want 1", len(env.Series))
	}
	if env.Series[0].Datatypes != nil || env.Series[0].Columns != nil || env.Series[0].Values != nil {
		t.Errorf("empty envelope round trip = %+v, want everything nil", env.Series[0])
	}
}
