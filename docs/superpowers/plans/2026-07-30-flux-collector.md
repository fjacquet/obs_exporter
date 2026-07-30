# Flux Collector and Ping Correction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the object-port ping decode so it matches items by name, then add an opt-in Flux collector that serves the metric families the ObjectScale management API does not.

**Architecture:** Two independent, non-breaking releases on branch `feat/flux-collector`. v3.1.0 corrects `internal/ecs/dt.go`'s positional XML decode and adds a `Sample.Type` (gauge/counter) that both export paths honour. v3.2.0 adds `Flux`, a `ResourceCollector` (ADR-0009) that POSTs one Flux query per measurement to the same 4443 endpoint with the same session token, decodes a columnar all-strings JSON envelope, and maps Flux's `host` tag onto the `/vdc/nodes` `nodename` every other collector already uses.

**Tech Stack:** Go 1.26.4, `resty/v2` (via `internal/ecsclient`), `prometheus/client_golang`, OpenTelemetry Go SDK, `logrus`, stdlib `encoding/xml` / `encoding/json`.

**Spec:** `docs/superpowers/specs/2026-07-30-flux-collector-design.md`

## Global Constraints

- **Absent, never zero (ADR-0007).** A value that is missing, unparseable, or that the cluster reports as unknown yields *no sample*. Never a zero, never a stale substitute.
- **One metric name = one ordered label-key set (ADR-0006).** `TestLabelKeyConsistency` must stay green. A differing label-key set means a different metric name.
- **One request per measurement per cycle, never per node (ADR-0002).** Queries close with `|> last()` and carry no host filter.
- **Exactly one source emits a given metric name per cycle.** Decided in `Registry`, before any request is issued.
- **Naming:** `ecs_<object>_<metric>[_<unit>]`. Per-second values are gauges and must never be `rate()`d. Cumulative counters take `_total` and `Type: Counter`.
- **No inline `nosemgrep` or `//nolint`.** Restructure instead — semgrep blocks on findings.
- **Never log a response body at `--trace` without the auth-path skip** already present in `ecsclient`.
- **Flux range window is `-15m`.** Not the guide's `-30m`, not five minutes. Release note OBS04J-596 records the store timing out at one hour; `statDataHead` writes points five minutes apart, so a five-minute window can legitimately return nothing.
- Gate every commit with `make sure`; run `make ci` before the final commit of each phase.

## File Structure

**Phase 1 (v3.1.0)**

| File | Responsibility |
|---|---|
| `internal/ecs/dt.go` (modify) | Ping payload decoded by `Name`; emits `active_connections` + `maintenance_mode` |
| `internal/ecs/dt_test.go` (modify) | Ordering-independence, tri-state status, absent-item cases |
| `internal/ecs/sample.go` (modify) | `SampleType`, `Sample.Type`, propagation through `WithCluster` |
| `internal/ecs/snapshot.go` (modify) | `Snapshot.MetricType` — the type lookup the OTLP path needs |
| `internal/ecs/prometheus.go` (modify) | `CounterValue` for counter samples |
| `internal/ecs/otlp.go` (modify) | `Float64ObservableCounter` for counter names |
| `docs/metrics.md`, `CHANGELOG.md`, `grafana/dashboards/obs-nodes.json` (modify) | Documentation and the new panel |

**Phase 2 (v3.2.0)**

| File | Responsibility |
|---|---|
| `internal/ecs/fluxjson.go` (create) | Tolerant decode of the `Series`/`Columns`/`Values` envelope. Knows nothing about metrics. |
| `internal/ecs/fluxjson_test.go` (create) | Hostile-input coverage for the decoder |
| `internal/ecs/flux.go` (create) | The collector: query table, node mapper, sample emission |
| `internal/ecs/flux_test.go` (create) | Collector behaviour against `ecsclient.Mock` |
| `internal/ecs/testdata/flux_*.json` (create) | Fixtures |
| `internal/config/config.go` (modify) | `collectFlux` flag |
| `internal/ecs/resource.go` (modify) | Registry wiring and arbitration |
| `internal/ecs/nodes.go` (modify) | Suppress the three arbitrated names when Flux owns them |

`fluxjson.go` is deliberately separate from `flux.go`: the decoder is the part that must survive payload surprises, and it is testable with no client, no config, and no metric knowledge.

---

## Phase 1 — v3.1.0

### Task 1: Match ping items by name

**Files:**
- Modify: `internal/ecs/dt.go:27-31` (the `pingResp` type), `internal/ecs/dt.go:149-158` (emission)
- Test: `internal/ecs/dt_test.go`

**Interfaces:**
- Consumes: `anyToFloat(any) (float64, bool)` from `points.go`; `copyLabels`, `Sample`, `Label` from `sample.go`; `findSample`/`mustSample`/`mockClient` from `fixtures_test.go`.
- Produces: metric `ecs_node_maintenance_mode{node}`; `ecs_node_active_connections{node}` keeps its name and meaning.

- [ ] **Step 1: Write the failing tests**

Append to `internal/ecs/dt_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/ecs -run 'TestDTPing' -v`
Expected: FAIL — `TestDTPingMatchesItemsByName` reports `ecs_node_active_connections` not found (the reversed payload's first item has no `Value`), and the other three fail to compile or find `ecs_node_maintenance_mode`.

- [ ] **Step 3: Replace the ping type**

In `internal/ecs/dt.go`, replace lines 27-31:

```go
// pingItem is one entry of the object-port ping payload. The 4.3 REST reference
// documents PingList as 0-* PingItem elements with no guaranteed ordering, and
// the &item=load-factor parameter changes which are present, so items must be
// matched by Name and never by position.
type pingItem struct {
	Name   string `xml:"Name"`
	Value  string `xml:"Value"`
	Status string `xml:"Status"`
}

// pingResp models the object-port ping XML (GET https://<node>:9021/?ping).
type pingResp struct {
	Items []pingItem `xml:"PingItem"`
}

// item returns the named PingItem, if the node reported it.
func (p pingResp) item(name string) (pingItem, bool) {
	for _, it := range p.Items {
		if it.Name == name {
			return it, true
		}
	}
	return pingItem{}, false
}
```

- [ ] **Step 4: Emit both items**

In `internal/ecs/dt.go`, replace the ping emission (the `if pingErr == nil { ... }` block at 156-158) with:

```go
	if pingErr == nil {
		out = appendPing(out, ping, node)
	}
```

and add below `upValue`:

```go
// appendPing maps the ping payload's two documented items onto samples.
// LOAD_FACTOR is the node's active Jetty connection count, per the 4.3 REST
// reference. MAINTENANCE_MODE is tri-state: the cluster may report UNKNOWN, and
// flattening that to 0 would assert the one thing an operator acts on.
func appendPing(out []Sample, p pingResp, node Label) []Sample {
	if it, ok := p.item("LOAD_FACTOR"); ok {
		if v, ok := anyToFloat(it.Value); ok {
			out = append(out, Sample{Name: "ecs_node_active_connections", Labels: copyLabels([]Label{node}), Value: v})
		}
	}
	if it, ok := p.item("MAINTENANCE_MODE"); ok {
		switch strings.ToUpper(strings.TrimSpace(it.Status)) {
		case "OFF":
			out = append(out, Sample{Name: "ecs_node_maintenance_mode", Labels: copyLabels([]Label{node}), Value: 0})
		case "ON":
			out = append(out, Sample{Name: "ecs_node_maintenance_mode", Labels: copyLabels([]Label{node}), Value: 1})
		}
	}
	return out
}
```

Add `"strings"` to the import block in `internal/ecs/dt.go`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/ecs -run 'TestDT' -v`
Expected: PASS, including the pre-existing `TestDTCollect` (its `pingXML` has LOAD_FACTOR with `Value` 42, so `active_connections` stays 42).

- [ ] **Step 6: Commit**

```bash
git add internal/ecs/dt.go internal/ecs/dt_test.go
git commit -m "fix(ecs): match ping items by name, and expose maintenance mode

PingItem is documented as 0-* elements with no guaranteed ordering, so decoding
PingItem>Value into a scalar read whichever item came first. Match on Name, and
add ecs_node_maintenance_mode with UNKNOWN yielding an absent sample."
```

---

### Task 2: Sample.Type and the Prometheus counter path

**Files:**
- Modify: `internal/ecs/sample.go:11-16` (the `Sample` type), `internal/ecs/sample.go:31-36` (`WithCluster`), `internal/ecs/prometheus.go:44-48`
- Test: `internal/ecs/sample_test.go`, `internal/ecs/prometheus_test.go`

**Interfaces:**
- Produces: `type SampleType uint8` with constants `Gauge` (zero value) and `Counter`; field `Sample.Type SampleType`. Task 3 and every Phase 2 task depend on these exact names.

- [ ] **Step 1: Write the failing tests**

Append to `internal/ecs/sample_test.go`:

```go
func TestWithClusterPreservesType(t *testing.T) {
	// The collection loop stamps the cluster label on every sample. A counter
	// that loses its type there would silently export as a gauge.
	s := Sample{Name: "ecs_node_requests_total", Value: 1, Type: Counter}
	if got := s.WithCluster("c1").Type; got != Counter {
		t.Errorf("Type after WithCluster = %v, want Counter", got)
	}
}

func TestSampleTypeZeroValueIsGauge(t *testing.T) {
	// Every existing collector builds Samples without a Type; they must stay gauges.
	if (Sample{}).Type != Gauge {
		t.Error("the zero SampleType must be Gauge")
	}
}
```

Append to `internal/ecs/prometheus_test.go`:

```go
func TestPromCollectorEmitsCounterType(t *testing.T) {
	store := NewSnapshotStore()
	store.Store(&Snapshot{Clusters: []*ClusterSnapshot{{
		Cluster: "c1",
		Samples: []Sample{
			{Name: "ecs_node_requests_total", Labels: []Label{{"node", "n1"}}, Value: 5, Type: Counter},
			{Name: "ecs_node_cpu_utilization_percent", Labels: []Label{{"node", "n1"}}, Value: 12},
		},
	}}})

	reg := prometheus.NewRegistry()
	reg.MustRegister(NewPromCollector(store))
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]dto.MetricType{}
	for _, mf := range mfs {
		types[mf.GetName()] = mf.GetType()
	}
	if types["ecs_node_requests_total"] != dto.MetricType_COUNTER {
		t.Errorf("requests_total type = %v, want COUNTER", types["ecs_node_requests_total"])
	}
	if types["ecs_node_cpu_utilization_percent"] != dto.MetricType_GAUGE {
		t.Errorf("cpu_utilization type = %v, want GAUGE", types["ecs_node_cpu_utilization_percent"])
	}
}
```

Add `dto "github.com/prometheus/client_model/go"` to `prometheus_test.go`'s imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/ecs -run 'TestWithClusterPreservesType|TestSampleTypeZeroValueIsGauge|TestPromCollectorEmitsCounterType' -v`
Expected: FAIL — compile error, `Type` and `Counter` are undefined.

- [ ] **Step 3: Add the type**

In `internal/ecs/sample.go`, above `Sample`:

```go
// SampleType distinguishes a monotonic counter from a gauge. The zero value is
// Gauge, so every collector that predates this type keeps emitting gauges with
// no change.
type SampleType uint8

const (
	// Gauge is a value that may move in either direction.
	Gauge SampleType = iota
	// Counter is a cumulative value that only increases, and restarts from zero
	// when the process producing it restarts — which is exactly what ObjectScale
	// documents for the monitoring_main fields. Consumers rate() it.
	Counter
)
```

Add the field to `Sample`:

```go
type Sample struct {
	Name   string
	Labels []Label
	Value  float64
	Type   SampleType
}
```

Carry it through `WithCluster`'s return:

```go
	return Sample{Name: s.Name, Labels: labels, Value: s.Value, Type: s.Type}
```

- [ ] **Step 4: Select the Prometheus value type**

In `internal/ecs/prometheus.go`, replace line 45:

```go
		valueType := prometheus.GaugeValue
		if s.Type == Counter {
			valueType = prometheus.CounterValue
		}
		m, err := prometheus.NewConstMetric(desc, valueType, s.Value, vals...)
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/ecs -v` then `make sure`
Expected: PASS, all packages.

- [ ] **Step 6: Commit**

```bash
git add internal/ecs/sample.go internal/ecs/sample_test.go internal/ecs/prometheus.go internal/ecs/prometheus_test.go
git commit -m "feat(ecs): give Sample a type, and honour counters on the Prometheus path

The zero value is Gauge, so no existing series changes. Flux exposes cumulative
counters that reset on datahead restart; a gauge would read each reset as a cliff."
```

---

### Task 3: Counters on the OTLP path

**Files:**
- Modify: `internal/ecs/snapshot.go` (add `MetricType`), `internal/ecs/otlp.go:77-94`
- Test: `internal/ecs/otlp_test.go`

**Interfaces:**
- Consumes: `SampleType`, `Gauge`, `Counter` from Task 2.
- Produces: `(*Snapshot).MetricType(name string) SampleType`.

- [ ] **Step 1: Write the failing test**

Append to `internal/ecs/otlp_test.go`:

```go
func TestOTLPExporterRegistersCounters(t *testing.T) {
	store := NewSnapshotStore()
	store.Store(&Snapshot{Clusters: []*ClusterSnapshot{{
		Cluster: "c1",
		Samples: []Sample{
			{Name: "ecs_node_requests_total", Labels: []Label{{"node", "n1"}}, Value: 5, Type: Counter},
			{Name: "ecs_node_cpu_utilization_percent", Labels: []Label{{"node", "n1"}}, Value: 12},
		},
	}}})

	reader := sdkmetric.NewManualReader()
	exp := newOTLPExporter(reader, store, "test")
	if err := exp.EnsureInstruments(); err != nil {
		t.Fatal(err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Sum[float64]:
				if d.IsMonotonic {
					kinds[m.Name] = "counter"
				}
			case metricdata.Gauge[float64]:
				kinds[m.Name] = "gauge"
			}
		}
	}
	if kinds["ecs_node_requests_total"] != "counter" {
		t.Errorf("requests_total registered as %q, want counter", kinds["ecs_node_requests_total"])
	}
	if kinds["ecs_node_cpu_utilization_percent"] != "gauge" {
		t.Errorf("cpu_utilization registered as %q, want gauge", kinds["ecs_node_cpu_utilization_percent"])
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/ecs -run TestOTLPExporterRegistersCounters -v`
Expected: FAIL — `requests_total registered as "gauge", want counter`.

- [ ] **Step 3: Add the type lookup**

Append to `internal/ecs/snapshot.go`:

```go
// MetricType returns the sample type recorded for a metric name, defaulting to
// Gauge when the name is absent. One name carries one type across every cluster
// in a snapshot — the same single-schema-per-name invariant ADR-0006 imposes on
// label keys — so the first match is authoritative.
func (s *Snapshot) MetricType(name string) SampleType {
	for _, c := range s.Clusters {
		for _, smp := range c.Samples {
			if smp.Name == name {
				return smp.Type
			}
		}
	}
	return Gauge
}
```

- [ ] **Step 4: Branch instrument registration**

In `internal/ecs/otlp.go`, replace the body of the `for _, name := range snap.MetricNames()` loop (lines 78-93) with:

```go
		if _, ok := e.registered[name]; ok {
			continue
		}
		metricName := name
		observe := func(_ context.Context, obs metric.Float64Observer) error {
			for _, s := range e.store.Load().SamplesByName(metricName) {
				obs.Observe(s.Value, metric.WithAttributes(attrsFor(s.Labels)...))
			}
			return nil
		}
		var err error
		if snap.MetricType(metricName) == Counter {
			_, err = e.meter.Float64ObservableCounter(metricName, metric.WithFloat64Callback(observe))
		} else {
			_, err = e.meter.Float64ObservableGauge(metricName, metric.WithFloat64Callback(observe))
		}
		if err != nil {
			return err
		}
		e.registered[metricName] = struct{}{}
```

Update the doc comment on `EnsureInstruments` (line 69-71) to say it registers an observable gauge *or counter* per the sample type.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/ecs -v` then `make sure`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ecs/snapshot.go internal/ecs/otlp.go internal/ecs/otlp_test.go
git commit -m "feat(ecs): register OTLP counters for counter-typed samples

A name's type is fixed for the whole snapshot, so the instrument kind is chosen
once at registration alongside the existing gauge path."
```

---

### Task 4: Document the ping change

**Files:**
- Modify: `docs/metrics.md:176-185`, `CHANGELOG.md`, `grafana/dashboards/obs-nodes.json:346`

- [ ] **Step 1: Correct and extend the metrics table**

In `docs/metrics.md`, replace the `ecs_node_active_connections` row and add one below it:

```markdown
| `ecs_node_active_connections` | active Jetty connections on the node, from the object-port ping's `LOAD_FACTOR` item (port 9021, `data_ip`) |
| `ecs_node_maintenance_mode` | 1 when the node reports `MAINTENANCE_MODE` `ON`, 0 when `OFF`. Absent when the node reports `UNKNOWN` — the exporter does not guess a node out of maintenance. |
```

Add a sentence to the surrounding prose at `docs/metrics.md:176`: the ping payload's items are matched by `Name`, because the API documents `PingList` as `0-*` `PingItem` elements with no guaranteed order.

- [ ] **Step 2: Add the Grafana panel**

In `grafana/dashboards/obs-nodes.json`, insert after the panel with `"id": 6` (closing brace at line 346), before the closing `]`:

```json
    ,{
      "id": 7,
      "type": "timeseries",
      "title": "Maintenance mode",
      "datasource": {
        "type": "prometheus",
        "uid": "prometheus"
      },
      "gridPos": {
        "h": 8,
        "w": 8,
        "x": 0,
        "y": 16
      },
      "fieldConfig": {
        "defaults": {
          "max": 1,
          "min": 0,
          "custom": {
            "spanNulls": false
          }
        },
        "overrides": []
      },
      "options": {
        "legend": {
          "displayMode": "table",
          "placement": "right",
          "calcs": [
            "last"
          ]
        }
      },
      "targets": [
        {
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "expr": "ecs_node_maintenance_mode{cluster=~\"$cluster\",node=~\"$node\"}",
          "legendFormat": "{{cluster}} {{node}}",
          "refId": "A"
        }
      ]
    }
```

- [ ] **Step 3: Verify the dashboard JSON still parses**

Run: `python3 -m json.tool grafana/dashboards/obs-nodes.json > /dev/null && echo ok`
Expected: `ok`

- [ ] **Step 4: Add the CHANGELOG entry**

Add a `## [3.1.0]` section above the existing `## [3.0.0]`:

```markdown
### Fixed

- The object-port ping is decoded by item `Name` rather than by position.
  `PingItem` is documented as `0-*` elements with no guaranteed ordering, so
  `ecs_node_active_connections` previously read whichever item came first. Its
  name and meaning are unchanged — the 4.3 REST reference defines `LOAD_FACTOR`
  as the node's active Jetty connection count.

### Added

- `ecs_node_maintenance_mode`, from the ping's `MAINTENANCE_MODE` item. A node
  reporting `UNKNOWN` yields an absent sample, never 0.
- Internal: samples carry a gauge/counter type, honoured by both the Prometheus
  and OTLP export paths. No existing series changes type.
```

- [ ] **Step 5: Run the full gate and commit**

Run: `make ci`
Expected: PASS.

```bash
git add docs/metrics.md CHANGELOG.md grafana/dashboards/obs-nodes.json
git commit -m "docs(metrics): record the ping correction and maintenance mode"
```

---

## Phase 2 — v3.2.0

> Phase 2 emits metrics whose exact values cannot be verified until Benjamin's 4.3 traces arrive. Fixtures below are transcribed from the 4.3 admin guide's own worked example, so they are documented shapes rather than invented ones. Task 11 is the gate that replaces them.

### Task 5: The Flux response decoder

**Files:**
- Create: `internal/ecs/fluxjson.go`, `internal/ecs/fluxjson_test.go`, `internal/ecs/testdata/flux_transactions.json`

**Interfaces:**
- Consumes: `cleanScalar([]byte) (string, bool)` and `anyToFloat(any) (float64, bool)` from `points.go`.
- Produces: `type fluxResp`, `(fluxResp).rows() []fluxRow`, `(fluxRow).value(col string) (string, bool)`, `(fluxRow).num(col string) (float64, bool)`.

- [ ] **Step 1: Add the fixture**

Create `internal/ecs/testdata/flux_transactions.json`, transcribed from the admin guide's JSON example:

```json
{
  "Series": [
    {
      "Datatypes": ["long", "dateTime:RFC3339", "dateTime:RFC3339", "dateTime:RFC3339", "long", "string", "string", "string", "string", "string", "string"],
      "Columns": ["table", "_start", "_stop", "_time", "_value", "_field", "_measurement", "host", "node_id", "process", "tag"],
      "Values": [
        ["0", "2020-03-10T09:54:31.207799855Z", "2020-03-10T10:24:31.207799855Z", "2020-03-10T09:56:43Z", "17", "failed_request_counter", "statDataHead_performance_internal_transactions", "supr01-r01.example.com", "28cd473e-ca45-4623-b30d-0481c548a650", "statDataHead", "dashboard"],
        ["1", "2020-03-10T09:54:31.207799855Z", "2020-03-10T10:24:31.207799855Z", "2020-03-10T09:56:43Z", "9312", "succeed_request_counter", "statDataHead_performance_internal_transactions", "supr01-r01.example.com", "28cd473e-ca45-4623-b30d-0481c548a650", "statDataHead", "dashboard"]
      ]
    }
  ]
}
```

- [ ] **Step 2: Write the failing tests**

Create `internal/ecs/fluxjson_test.go`:

```go
package ecs

import (
	"encoding/json"
	"testing"
)

func decodeFlux(t *testing.T, body string) fluxResp {
	t.Helper()
	var r fluxResp
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestFluxRowsAddressColumnsByName(t *testing.T) {
	rows := decodeFlux(t, fixture(t, "flux_transactions.json")).rows()
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	field, ok := rows[0].value("_field")
	if !ok || field != "failed_request_counter" {
		t.Errorf("_field = %q (ok=%v), want failed_request_counter", field, ok)
	}
	v, ok := rows[0].num("_value")
	if !ok || v != 17 {
		t.Errorf("_value = %v (ok=%v), want 17", v, ok)
	}
	if host, _ := rows[1].value("host"); host != "supr01-r01.example.com" {
		t.Errorf("host = %q, want supr01-r01.example.com", host)
	}
}

func TestFluxRowsToleratesReorderedColumns(t *testing.T) {
	// Column order is not part of the contract; only names are.
	rows := decodeFlux(t, `{"Series":[{"Columns":["_value","_field","host"],
		"Values":[["3","usage_user","n1"]]}]}`).rows()
	v, ok := rows[0].num("_value")
	if !ok || v != 3 {
		t.Errorf("_value = %v (ok=%v), want 3", v, ok)
	}
}

func TestFluxRowsSkipsMisalignedRows(t *testing.T) {
	// A row whose width disagrees with its column list cannot be aligned, so it
	// cannot be trusted — silently mapping it would attach values to the wrong keys.
	rows := decodeFlux(t, `{"Series":[{"Columns":["_value","_field"],
		"Values":[["3"],["4","usage_user"]]}]}`).rows()
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want only the aligned one", len(rows))
	}
}

func TestFluxRowsTreatNullAndNAAsAbsent(t *testing.T) {
	rows := decodeFlux(t, `{"Series":[{"Columns":["_value","_field","host"],
		"Values":[[null,"usage_user","n1"],["N/A","usage_user","n2"]]}]}`).rows()
	for i, r := range rows {
		if _, ok := r.num("_value"); ok {
			t.Errorf("row %d yielded a value for an absent cell", i)
		}
	}
}

func TestFluxRowsHandlesEmptyAndMissingSeries(t *testing.T) {
	for _, body := range []string{
		`{"Series":[]}`,
		`{}`,
		`{"Series":[{"Columns":["_value"],"Values":[]}]}`,
	} {
		if got := len(decodeFlux(t, body).rows()); got != 0 {
			t.Errorf("%s yielded %d rows, want 0", body, got)
		}
	}
}

func TestFluxRowsMissingColumnIsAbsentNotZero(t *testing.T) {
	rows := decodeFlux(t, `{"Series":[{"Columns":["_field"],"Values":[["usage_user"]]}]}`).rows()
	if _, ok := rows[0].num("_value"); ok {
		t.Error("num returned ok for a column that is not present")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/ecs -run TestFlux -v`
Expected: FAIL — compile error, `fluxResp` undefined.

- [ ] **Step 4: Write the decoder**

Create `internal/ecs/fluxjson.go`:

```go
package ecs

import "encoding/json"

// The Flux API answers with a columnar envelope:
//
//	{"Series":[{"Datatypes":[…],
//	            "Columns":["table","_start","_stop","_time","_value","_field","_measurement","host","node_id","process","tag"],
//	            "Values":[["0","2020-…Z","2020-…Z","2020-…Z","1","failed_request_counter","statDataHead_…","ecs.lss.emc.com","28cd…","statDataHead","dashboard"]]}]}
//
// Every cell is a JSON string, numbers included, and column order is not part of
// the contract. This file resolves columns by name and yields nothing for a cell
// it cannot read, so an unreadable value produces an absent sample rather than a
// zero (ADR-0007) — the same discipline points.go applies to the dashboard's
// time series, for the same reason.

// fluxResp is the decoded query response.
type fluxResp struct {
	Series []fluxSeries `json:"Series"`
}

// fluxSeries is one result table. Cells stay raw so the shared tolerant scalar
// parsing decides what counts as absent.
type fluxSeries struct {
	Columns []string            `json:"Columns"`
	Values  [][]json.RawMessage `json:"Values"`
}

// fluxRow is one result row, addressable by column name. Columns whose cell held
// nothing are absent from the map rather than present and empty.
type fluxRow struct {
	cols map[string]string
}

// rows flattens every Series into name-addressable rows. A row whose width
// disagrees with its column list is dropped: it cannot be aligned, so mapping it
// would attach values to the wrong keys.
func (r fluxResp) rows() []fluxRow {
	var out []fluxRow
	for _, s := range r.Series {
		for _, raw := range s.Values {
			if len(raw) != len(s.Columns) {
				continue
			}
			cols := make(map[string]string, len(raw))
			for i, cell := range raw {
				if v, ok := cleanScalar(cell); ok {
					cols[s.Columns[i]] = v
				}
			}
			out = append(out, fluxRow{cols: cols})
		}
	}
	return out
}

// value returns the row's cell for a column, and whether it carried anything.
func (r fluxRow) value(col string) (string, bool) {
	v, ok := r.cols[col]
	return v, ok
}

// num parses a column as a float. A missing column and an unparseable one are
// both absent — neither is zero.
func (r fluxRow) num(col string) (float64, bool) {
	s, ok := r.cols[col]
	if !ok {
		return 0, false
	}
	return anyToFloat(s)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/ecs -run TestFlux -v`
Expected: PASS, all six tests.

- [ ] **Step 6: Commit**

```bash
git add internal/ecs/fluxjson.go internal/ecs/fluxjson_test.go internal/ecs/testdata/flux_transactions.json
git commit -m "feat(ecs): decode the Flux columnar response

Columns are resolved by name and every cell is a string, so parsing reuses the
tolerant scalar rules points.go already applies to the dashboard payloads."
```

---

### Task 6: The collectFlux flag and registry arbitration

**Files:**
- Modify: `internal/config/config.go:27` (beside `CollectDT`), `internal/ecs/resource.go:21-38`, `internal/ecs/nodes.go:41` (the `Nodes` type) and `:88-90`
- Test: `internal/config/config_test.go`, `internal/ecs/collector_test.go`

**Interfaces:**
- Consumes: nothing from Phase 2 so far.
- Produces: `config.Cluster.CollectFlux bool`; `ecs.Nodes` gains field `FluxOwnsPerf bool`; `Registry` appends `Flux{}` when the flag is set.

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/config_test.go`, inside the defaults test at line 47:

```go
	if c.CollectFlux {
		t.Error("Flux collection should default to disabled")
	}
```

Append to `internal/ecs/collector_test.go`:

```go
func TestRegistryArbitratesPerfNamesWithFlux(t *testing.T) {
	// The three names below exist in both sources. Exactly one collector may own
	// each per cycle, and the decision is made here, before any request goes out.
	off := Registry(config.Cluster{})
	on := Registry(config.Cluster{CollectFlux: true})

	var offNodes, onNodes Nodes
	for _, rc := range off {
		if n, ok := rc.(Nodes); ok {
			offNodes = n
		}
	}
	for _, rc := range on {
		if n, ok := rc.(Nodes); ok {
			onNodes = n
		}
	}
	if offNodes.FluxOwnsPerf {
		t.Error("Nodes must keep the perf names when Flux is off")
	}
	if !onNodes.FluxOwnsPerf {
		t.Error("Nodes must yield the perf names when Flux is on")
	}

	var hasFlux bool
	for _, rc := range on {
		if rc.Name() == "flux" {
			hasFlux = true
		}
	}
	if !hasFlux {
		t.Error("collectFlux must register the flux collector")
	}
	for _, rc := range off {
		if rc.Name() == "flux" {
			t.Error("flux must not be registered when the flag is unset")
		}
	}
}

func TestNodesYieldsArbitratedNames(t *testing.T) {
	samples, err := Nodes{FluxOwnsPerf: true}.Collect(t.Context(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"ecs_node_cpu_utilization_percent",
		"ecs_node_memory_utilization_percent",
		"ecs_node_memory_used_bytes",
	} {
		if _, ok := findSample(samples, name); ok {
			t.Errorf("%s emitted by Nodes while Flux owns it", name)
		}
	}
	// Names Flux cannot fill — its net measurement carries a per-interface
	// dimension, so it must use a different name — stay with the dashboard.
	if _, ok := findSample(samples, "ecs_node_nic_bandwidth"); !ok {
		t.Error("nic_bandwidth must stay on the dashboard path")
	}
}
```

Add `"github.com/fjacquet/obs_exporter/internal/config"` to `collector_test.go`'s imports if absent.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config ./internal/ecs -run 'CollectFlux|Arbitrat|YieldsArbitrated' -v`
Expected: FAIL — `CollectFlux` and `FluxOwnsPerf` undefined.

- [ ] **Step 3: Add the config flag**

In `internal/config/config.go`, after `CollectDT` (line 27):

```go
	// CollectFlux opts in to querying the cluster's Flux/InfluxDB monitoring
	// store (POST /flux/api/external/v2/query on the management port) for metric
	// families the management API does not serve. Requires the cluster account to
	// hold SYSTEM_MONITOR or SYSTEM_ADMIN. Off by default: it adds a second data
	// protocol and a dependency on an ObjectScale internal.
	CollectFlux bool `yaml:"collectFlux"`
```

- [ ] **Step 4: Wire the registry and the suppression**

In `internal/ecs/resource.go`, replace the `Nodes{}` entry and add the Flux registration:

```go
	rcs := []ResourceCollector{
		Cluster{},
		Replication{},
		// When Flux is enabled it owns the three per-node performance names the
		// dashboard payloads no longer carry on 4.3. Deciding here, once, keeps a
		// single answer to "who emits this name" and makes it true before any
		// request is issued.
		Nodes{FluxOwnsPerf: cl.CollectFlux},
		Info{},
	}
```

and after the `CollectDT` block:

```go
	if cl.CollectFlux {
		rcs = append(rcs, Flux{})
	}
```

In `internal/ecs/nodes.go`, give `Nodes` the field and a receiver name:

```go
// Nodes collects per-node health, capacity, utilization, and transaction stats
// from the dashboard API. FluxOwnsPerf suppresses the three names the Flux
// collector takes over, so exactly one source emits each per cycle (ADR-0006).
type Nodes struct{ FluxOwnsPerf bool }
```

Change the method receiver from `func (Nodes) Collect(` to `func (nc Nodes) Collect(`, and wrap lines 88-90:

```go
		if !nc.FluxOwnsPerf {
			out = appendSeries(out, "ecs_node_cpu_utilization_percent", n.NodeCPUUtilization, node)
			out = appendSeries(out, "ecs_node_memory_utilization_percent", n.NodeMemoryUtilization, node)
			out = appendSeries(out, "ecs_node_memory_used_bytes", n.NodeMemoryUtilizationBytes, node)
		}
```

Add a temporary stub so the package compiles before Task 7; it is replaced there:

```go
// in internal/ecs/flux.go
package ecs

// Flux is the opt-in collector for metrics the management API does not serve.
type Flux struct{}

// Name identifies this collector in ecs_collector_up.
func (Flux) Name() string { return "flux" }
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/config ./internal/ecs -v`
Expected: the arbitration tests PASS. `TestRegistryArbitratesPerfNamesWithFlux` will still fail on `Flux{}` not satisfying `ResourceCollector` until Task 7 adds `Collect`; if so, add the stub method returning `nil, nil` and note it is replaced in Task 7.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/ecs/resource.go internal/ecs/nodes.go internal/ecs/flux.go internal/ecs/collector_test.go
git commit -m "feat(config): add collectFlux, and arbitrate the shared per-node names

Registry decides ownership once, before any request. Only three names are
contested; everything else Flux serves carries a dimension the dashboard fields
do not, which forces a distinct name under ADR-0006 anyway."
```

---

### Task 7: The node mapper

**Files:**
- Modify: `internal/ecs/flux.go`
- Test: `internal/ecs/flux_test.go`

**Interfaces:**
- Consumes: `vdcNodesResp` and `pathVdcNodes` from `info.go`; `ecsclient.Client`.
- Produces: `newNodeMapper(ctx, ecsclient.Client) (*nodeMapper, error)` and `(*nodeMapper).lookup(host string) (string, bool)`.

- [ ] **Step 1: Write the failing tests**

Create `internal/ecs/flux_test.go`:

```go
package ecs

import (
	"testing"
)

func TestNodeMapperResolvesFluxHosts(t *testing.T) {
	// The vdc-nodes fixture names supr01-r01 at 10.0.0.1 / 10.1.0.1. Flux reports
	// host as an FQDN in the reference's example, so the mapper must join a
	// qualified name onto the bare nodename every other collector labels with.
	m, err := newNodeMapper(t.Context(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ host, want string }{
		{"supr01-r01", "supr01-r01"},
		{"supr01-r01.example.com", "supr01-r01"},
		{"SUPR01-R01.EXAMPLE.COM", "supr01-r01"},
		{"10.0.0.1", "supr01-r01"},
		{"10.1.0.1", "supr01-r01"},
	} {
		got, ok := m.lookup(tc.host)
		if !ok || got != tc.want {
			t.Errorf("lookup(%q) = %q,%v; want %q,true", tc.host, got, ok, tc.want)
		}
	}
}

func TestNodeMapperRejectsUnknownHosts(t *testing.T) {
	// A host that joins nothing must fail loudly to its caller rather than
	// produce a series no dashboard query can line up with the rest.
	m, err := newNodeMapper(t.Context(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := m.lookup("someone-elses-node.example.com"); ok {
		t.Errorf("lookup of an unknown host returned %q, want no match", got)
	}
}

func TestShortHostLeavesIPsAlone(t *testing.T) {
	// Truncating an IPv4 address at its first dot produces a meaningless key
	// that could collide across nodes.
	if got := shortHost("10.0.0.1"); got != "10.0.0.1" {
		t.Errorf("shortHost(IP) = %q, want it unchanged", got)
	}
	if got := shortHost("n1.example.com"); got != "n1" {
		t.Errorf("shortHost(FQDN) = %q, want n1", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/ecs -run 'NodeMapper|ShortHost' -v`
Expected: FAIL — `newNodeMapper` undefined.

- [ ] **Step 3: Implement the mapper**

Append to `internal/ecs/flux.go`:

```go
import (
	"cmp"
	"context"
	"net"
	"strings"

	"github.com/fjacquet/obs_exporter/internal/ecsclient"
)

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
			// The prior != label test is load-bearing: one node legitimately yields
			// the same key twice — mgmt_ip equals data_ip on a flat network, and
			// shortHost(nodename) equals nodename when the name is unqualified.
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
	// A blank entry marks a key claimed by more than one node; it identifies
	// nobody, so it must not resolve.
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/ecs -run 'NodeMapper|ShortHost' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ecs/flux.go internal/ecs/flux_test.go
git commit -m "feat(ecs): map Flux host tags onto the inventory nodename

Flux tags rows by FQDN or UUID; the rest of the exporter labels nodes by
nodename. Index every identifier a node publishes so the two sets join."
```

---

### Task 8: The query table and sample emission

**Files:**
- Modify: `internal/ecs/flux.go`, `internal/ecs/flux_test.go`
- Create: `internal/ecs/testdata/flux_cpu.json`, `internal/ecs/testdata/flux_net.json`, `internal/ecs/testdata/flux_dt_status.json`

**Interfaces:**
- Consumes: `fluxResp`/`fluxRow` (Task 5), `nodeMapper` (Task 7), `SampleType`/`Counter` (Task 2), `ecsclient.Client.Post`.
- Produces: `(Flux).Collect(ctx, ecsclient.Client) ([]Sample, error)`; constants `fluxPath`, `fluxRange`; the `fluxQueries` table.

- [ ] **Step 1: Add the fixtures**

Every file added under `internal/ecs/testdata/` must also be copied
byte-identically to `cmd/mockecs/fixtures/`, or `TestFixturesMatchMockecs`
fails and the branch goes red. Mirror each of the three below as you add it.
Wiring `cmd/mockecs` to *serve* the Flux endpoint is still Task 11's job; only
the fixture copies belong here.

`internal/ecs/testdata/flux_cpu.json`:

```json
{
  "Series": [
    {
      "Columns": ["_value", "_field", "_measurement", "cpu", "host", "node_id", "tag"],
      "Values": [
        ["31.5", "usage_user", "cpu", "cpu-total", "supr01-r01.example.com", "28cd473e-ca45-4623-b30d-0481c548a650", "dashboard"],
        ["12.25", "usage_user", "cpu", "cpu-total", "supr01-r02.example.com", "39de584f-db56-5734-c41e-1592d659b761", "dashboard"]
      ]
    }
  ]
}
```

`internal/ecs/testdata/flux_net.json`:

```json
{
  "Series": [
    {
      "Columns": ["_value", "_field", "_measurement", "host", "interface", "node_id", "tag"],
      "Values": [
        ["994013184", "bytes_recv", "net", "supr01-r01.example.com", "eth0", "28cd473e-ca45-4623-b30d-0481c548a650", "dashboard"],
        ["551944704", "bytes_sent", "net", "supr01-r01.example.com", "eth0", "28cd473e-ca45-4623-b30d-0481c548a650", "dashboard"],
        ["77", "bytes_recv", "net", "not-in-this-cluster.example.com", "eth0", "ffffffff-0000-0000-0000-000000000000", "dashboard"]
      ]
    }
  ]
}
```

`internal/ecs/testdata/flux_dt_status.json`:

```json
{
  "Series": [
    {
      "Columns": ["_value", "_field", "_measurement", "process", "tag"],
      "Values": [
        ["128", "total", "dtquery_dt_status", "dtquery", "dashboard"],
        ["2", "unready", "dtquery_dt_status", "dtquery", "dashboard"],
        ["1", "unknown", "dtquery_dt_status", "dtquery", "dashboard"]
      ]
    }
  ]
}
```

- [ ] **Step 2: Write the failing tests**

Append to `internal/ecs/flux_test.go`:

```go
// fluxMock answers every Flux POST with the fixture chosen by the measurement
// named in the query body. The Mock keys responses by path alone, and all eight
// queries share one path, so the collector's request bodies drive the routing.
func fluxMock(t *testing.T, byMeasurement map[string]string) ecsclient.Client {
	t.Helper()
	return &fluxClient{Client: mockClient(t), bodies: byMeasurement, t: t}
}

type fluxClient struct {
	ecsclient.Client
	bodies map[string]string
	t      *testing.T
}

func (f *fluxClient) Post(_ context.Context, path string, body, out any) error {
	if path != fluxPath {
		return fmt.Errorf("unexpected POST to %s", path)
	}
	q, ok := body.(map[string]string)
	if !ok {
		f.t.Fatalf("query body is %T, want map[string]string", body)
	}
	for measurement, fixtureName := range f.bodies {
		if strings.Contains(q["query"], `"`+measurement+`"`) {
			return json.Unmarshal([]byte(fixture(f.t, fixtureName)), out)
		}
	}
	// A measurement the cluster does not carry answers 200 with no rows.
	return json.Unmarshal([]byte(`{"Series":[]}`), out)
}

func collectFlux(t *testing.T, byMeasurement map[string]string) []Sample {
	t.Helper()
	samples, err := Flux{}.Collect(t.Context(), fluxMock(t, byMeasurement))
	if err != nil {
		t.Fatal(err)
	}
	return samples
}

func TestFluxCollectPerNodeGauges(t *testing.T) {
	samples := collectFlux(t, map[string]string{"cpu": "flux_cpu.json"})
	mustSample(t, samples, "ecs_node_cpu_utilization_percent", 31.5, Label{"node", "supr01-r01"})
	mustSample(t, samples, "ecs_node_cpu_utilization_percent", 12.25, Label{"node", "supr01-r02"})
	s, _ := findSample(samples, "ecs_node_cpu_utilization_percent", Label{"node", "supr01-r01"})
	if s.Type != Gauge {
		t.Error("cpu utilization must be a gauge")
	}
}

func TestFluxCollectNetworkCounters(t *testing.T) {
	samples := collectFlux(t, map[string]string{"net": "flux_net.json"})
	n1 := Label{"node", "supr01-r01"}
	mustSample(t, samples, "ecs_node_network_bytes_total", 994013184, n1, Label{"interface", "eth0"}, Label{"direction", "received"})
	mustSample(t, samples, "ecs_node_network_bytes_total", 551944704, n1, Label{"interface", "eth0"}, Label{"direction", "transmitted"})

	s, _ := findSample(samples, "ecs_node_network_bytes_total", n1, Label{"direction", "received"})
	if s.Type != Counter {
		t.Error("network bytes must be a counter: the guide documents these fields as resetting on datahead restart")
	}
	// Label order is part of the metric's schema (ADR-0006).
	wantKeys := []string{"node", "interface", "direction"}
	gotKeys := make([]string, len(s.Labels))
	for i, l := range s.Labels {
		gotKeys[i] = l.Key
	}
	if !slices.Equal(gotKeys, wantKeys) {
		t.Errorf("label keys = %v, want %v", gotKeys, wantKeys)
	}
}

func TestFluxCollectClusterScopedDT(t *testing.T) {
	// dtquery_dt_status is tagged {process, tag} only — it is cluster-wide, and
	// must not pretend to be per-node.
	samples := collectFlux(t, map[string]string{"dtquery_dt_status": "flux_dt_status.json"})
	mustSample(t, samples, "ecs_cluster_dt_total", 128)
	mustSample(t, samples, "ecs_cluster_dt_unready", 2)
	mustSample(t, samples, "ecs_cluster_dt_unknown", 1)
	s, _ := findSample(samples, "ecs_cluster_dt_total")
	if len(s.Labels) != 0 {
		t.Errorf("cluster DT carries labels %v, want none", s.Labels)
	}
}

func TestFluxQueryScriptShape(t *testing.T) {
	var cpu fluxQuery
	for _, q := range fluxQueries {
		if q.measurement == "cpu" {
			cpu = q
		}
	}
	script := cpu.script()
	for _, want := range []string{
		`from(bucket:"monitoring_op")`,
		`range(start: -15m)`,
		`r._measurement == "cpu"`,
		`r.cpu == "cpu-total"`,
		`|> last()`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("cpu script missing %q:\n%s", want, script)
		}
	}
	// A host filter would turn one cluster-wide request into N+1 per node.
	if strings.Contains(script, "r.host ==") {
		t.Errorf("script filters by host:\n%s", script)
	}
}
```

Imports for `flux_test.go`: `context`, `encoding/json`, `fmt`, `slices`, `strings`, `testing`, and `github.com/fjacquet/obs_exporter/internal/ecsclient`.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/ecs -run TestFlux -v`
Expected: FAIL — `fluxPath`, `fluxQueries`, and `Flux.Collect` undefined.

- [ ] **Step 4: Implement the query table and Collect**

Append to `internal/ecs/flux.go` (and extend the import block with `fmt`, `slices`, and `log "github.com/sirupsen/logrus"`):

```go
// fluxPath is the Flux query endpoint, served on the same management port and
// accepting the same X-SDS-AUTH-TOKEN as every other call this exporter makes.
const fluxPath = "/flux/api/external/v2/query"

// fluxRange is how far back each query looks. The store punishes wide windows —
// the 4.3 release notes record Grafana timing out against it at one hour — while
// statDataHead writes points five minutes apart, so a five-minute window can
// legitimately return nothing. Fifteen minutes keeps two to three points of
// margin an order of magnitude below the failing window.
const fluxRange = "-15m"

// fluxField maps one measurement field onto a metric name, its type, and the
// constant labels that distinguish it from its siblings under the same name.
type fluxField struct {
	field  string
	name   string
	typ    SampleType
	labels []Label
}

// fluxQuery is one measurement's request and its field mapping. One request per
// measurement, closed with |> last() and carrying no host filter, returns the
// newest point per node in a single cluster-wide pass (ADR-0002).
type fluxQuery struct {
	bucket      string
	measurement string
	// extra is an additional filter line, empty for most measurements.
	extra string
	// perNode marks rows that carry a host tag needing a node label.
	perNode bool
	// tagLabels are row tags copied through as labels. A row missing one is
	// dropped rather than emitted with a short label set, because one metric name
	// carries one ordered label-key set (ADR-0006).
	tagLabels []string
	fields    []fluxField
}

// script renders the Flux program for this measurement.
func (q fluxQuery) script() string {
	s := fmt.Sprintf("from(bucket:%q)\n  |> range(start: %s)\n  |> filter(fn: (r) => r._measurement == %q)\n",
		q.bucket, fluxRange, q.measurement)
	if q.extra != "" {
		s += q.extra + "\n"
	}
	return s + "  |> last()"
}

// fluxQueries is the measurement-to-metric mapping, documented in
// docs/metrics.md. The three buckets divide by scope and by whether values
// arrive pre-rated: monitoring_op is per-node system state, monitoring_main
// per-node cumulative counters, monitoring_vdc VDC-wide values already expressed
// as rates — which is why the last group are gauges and must never be rate()d.
var fluxQueries = []fluxQuery{
	{
		bucket: "monitoring_op", measurement: "cpu",
		// The cpu measurement is tagged per core; cpu-total keeps one series per
		// node, matching the shape nodes.go reserves for this name.
		extra:   `  |> filter(fn: (r) => r.cpu == "cpu-total")`,
		perNode: true,
		fields: []fluxField{
			{field: "usage_user", name: "ecs_node_cpu_utilization_percent"},
		},
	},
	{
		bucket: "monitoring_op", measurement: "mem", perNode: true,
		fields: []fluxField{
			{field: "used_percent", name: "ecs_node_memory_utilization_percent"},
			{field: "used", name: "ecs_node_memory_used_bytes"},
		},
	},
	{
		bucket: "monitoring_op", measurement: "net", perNode: true,
		// net has no total row to fall back on, so the interface dimension is kept
		// rather than summed — adding a management and a data NIC together would
		// produce a number nobody asked for.
		tagLabels: []string{"interface"},
		fields: []fluxField{
			{field: "bytes_recv", name: "ecs_node_network_bytes_total", typ: Counter, labels: []Label{{"direction", "received"}}},
			{field: "bytes_sent", name: "ecs_node_network_bytes_total", typ: Counter, labels: []Label{{"direction", "transmitted"}}},
		},
	},
	{
		// Tagged {process, tag} only: cluster-wide, never per node.
		bucket: "monitoring_op", measurement: "dtquery_dt_status",
		fields: []fluxField{
			{field: "total", name: "ecs_cluster_dt_total"},
			{field: "unready", name: "ecs_cluster_dt_unready"},
			{field: "unknown", name: "ecs_cluster_dt_unknown"},
		},
	},
	{
		bucket: "monitoring_main", measurement: "statDataHead_performance_internal_transactions", perNode: true,
		fields: []fluxField{
			{field: "succeed_request_counter", name: "ecs_node_requests_total", typ: Counter, labels: []Label{{"outcome", "success"}}},
			{field: "failed_request_counter", name: "ecs_node_requests_total", typ: Counter, labels: []Label{{"outcome", "failed"}}},
		},
	},
	{
		bucket: "monitoring_main", measurement: "statDataHead_performance_internal_throughput", perNode: true,
		fields: []fluxField{
			{field: "total_read_requests_size", name: "ecs_node_request_bytes_total", typ: Counter, labels: []Label{{"op", "read"}}},
			{field: "total_write_requests_size", name: "ecs_node_request_bytes_total", typ: Counter, labels: []Label{{"op", "write"}}},
		},
	},
	{
		bucket: "monitoring_vdc", measurement: "cq_performance_transaction",
		fields: []fluxField{
			{field: "succeed_request_counter", name: "ecs_cluster_requests_per_second", labels: []Label{{"outcome", "success"}}},
			{field: "failed_request_counter", name: "ecs_cluster_requests_per_second", labels: []Label{{"outcome", "failed"}}},
		},
	},
	{
		bucket: "monitoring_vdc", measurement: "cq_performance_throughput",
		fields: []fluxField{
			{field: "total_read_requests_size", name: "ecs_cluster_request_bytes_per_second", labels: []Label{{"op", "read"}}},
			{field: "total_write_requests_size", name: "ecs_cluster_request_bytes_per_second", labels: []Label{{"op", "write"}}},
		},
	},
}

// Collect issues one query per measurement and maps the rows onto samples. A
// transport or auth failure fails the whole collector (ecs_collector_up=0); a
// measurement the cluster does not carry answers with no rows and yields a
// warning plus absent samples, because measurement names are undocumented and a
// rename must not take the other seven down with it.
func (Flux) Collect(ctx context.Context, c ecsclient.Client) ([]Sample, error) {
	mapper, err := newNodeMapper(ctx, c)
	if err != nil {
		return nil, err
	}

	var out []Sample
	var unmapped float64
	for _, q := range fluxQueries {
		var resp fluxResp
		if err := c.Post(ctx, fluxPath, map[string]string{"query": q.script()}, &resp); err != nil {
			return nil, fmt.Errorf("flux %s/%s: %w", q.bucket, q.measurement, err)
		}
		rows := resp.rows()
		if len(rows) == 0 {
			log.WithFields(log.Fields{"cluster": c.Name(), "bucket": q.bucket, "measurement": q.measurement}).
				Warn("Flux measurement returned no rows; its samples are absent this cycle")
			continue
		}
		samples, miss := q.samples(rows, mapper)
		out = append(out, samples...)
		unmapped += miss
	}

	// Always emitted, including as 0: "mapping worked" is the information that
	// distinguishes a healthy cycle from one where every node tag joined nothing.
	out = append(out, Sample{
		Name:   "ecs_collector_unmapped_nodes",
		Labels: []Label{{"collector", "flux"}},
		Value:  unmapped,
	})
	return out, nil
}

// samples maps one measurement's rows, returning the samples and how many rows
// were dropped for an unresolvable host.
func (q fluxQuery) samples(rows []fluxRow, mapper *nodeMapper) ([]Sample, float64) {
	var out []Sample
	var unmapped float64
	for _, row := range rows {
		field, ok := row.value("_field")
		if !ok {
			continue
		}
		v, ok := row.num("_value")
		if !ok {
			continue // absent, never zero
		}

		var base []Label
		if q.perNode {
			host, ok := row.value("host")
			if !ok {
				continue
			}
			node, ok := mapper.lookup(host)
			if !ok {
				unmapped++
				continue
			}
			base = append(base, Label{"node", node})
		}
		short := false
		for _, tag := range q.tagLabels {
			tv, ok := row.value(tag)
			if !ok {
				short = true // a partial label set would break the name's schema
				break
			}
			base = append(base, Label{tag, tv})
		}
		if short {
			continue
		}

		for _, f := range q.fields {
			if f.field != field {
				continue
			}
			out = append(out, Sample{
				Name:   f.name,
				Labels: append(slices.Clone(base), f.labels...),
				Value:  v,
				Type:   f.typ,
			})
		}
	}
	return out, unmapped
}
```

Delete the Task 6 stub `Collect` if one was added.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/ecs -run TestFlux -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ecs/flux.go internal/ecs/flux_test.go internal/ecs/testdata/flux_cpu.json internal/ecs/testdata/flux_net.json internal/ecs/testdata/flux_dt_status.json
git commit -m "feat(ecs): add the Flux collector's query table and emission

One request per measurement, closed with last() and no host filter, so a cycle
stays one cluster-wide pass. monitoring_vdc values are already per-second rates
and stay gauges; monitoring_main integers are documented counters."
```

---

### Task 9: Degradation and export-path coverage

**Files:**
- Modify: `internal/ecs/flux_test.go`

**Interfaces:**
- Consumes: everything from Tasks 5-8.

- [ ] **Step 1: Write the failing tests**

Append to `internal/ecs/flux_test.go`:

```go
func TestFluxCollectFailsOnEndpointError(t *testing.T) {
	// An unreachable or unauthorized endpoint degrades this collector alone:
	// returning an error is what drives ecs_collector_up{collector="flux"}=0.
	c := mockClient(t)
	c.Errs = map[string]error{fluxPath: errors.New("401 Unauthorized")}
	if _, err := (Flux{}).Collect(t.Context(), &fluxClient{Client: c, bodies: nil, t: t, fail: true}); err == nil {
		t.Error("Collect must return an error when the Flux endpoint rejects the query")
	}
}

func TestFluxCollectSurvivesRenamedMeasurement(t *testing.T) {
	// Measurement names are undocumented and unversioned: net/utilization is
	// listed in 3.8 and gone in 4.3. One missing measurement must not take the
	// other seven with it.
	samples := collectFlux(t, map[string]string{"cpu": "flux_cpu.json"})
	mustSample(t, samples, "ecs_node_cpu_utilization_percent", 31.5, Label{"node", "supr01-r01"})
	for _, absent := range []string{"ecs_cluster_dt_total", "ecs_node_requests_total"} {
		if _, ok := findSample(samples, absent); ok {
			t.Errorf("%s emitted from an empty measurement", absent)
		}
	}
}

func TestFluxCountsUnmappedHosts(t *testing.T) {
	// flux_net.json carries one row for a host absent from the inventory. Without
	// this counter, a cluster whose tag space we guessed wrong reports a healthy
	// collector producing no data.
	samples := collectFlux(t, map[string]string{"net": "flux_net.json"})
	mustSample(t, samples, "ecs_collector_unmapped_nodes", 1, Label{"collector", "flux"})
	if _, ok := findSample(samples, "ecs_node_network_bytes_total", Label{"node", "not-in-this-cluster.example.com"}); ok {
		t.Error("an unmappable host produced a series that cannot join the others")
	}
}

func TestFluxCollectEmitsZeroUnmappedOnSuccess(t *testing.T) {
	samples := collectFlux(t, map[string]string{"cpu": "flux_cpu.json"})
	mustSample(t, samples, "ecs_collector_unmapped_nodes", 0, Label{"collector", "flux"})
}
```

Add a `fail bool` field to `fluxClient` and honour it at the top of its `Post`:

```go
	if f.fail {
		return errors.New("401 Unauthorized")
	}
```

Add `errors` to the imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/ecs -run TestFlux -v`
Expected: FAIL — compile error on the new `fail` field.

- [ ] **Step 3: Make them pass**

Add the `fail` field and guard as described. No change to `flux.go` should be needed; if `TestFluxCollectEmitsZeroUnmappedOnSuccess` fails, the unconditional `ecs_collector_unmapped_nodes` append in `Collect` is missing.

- [ ] **Step 4: Run the whole suite**

Run: `go test ./... -race`
Expected: PASS, including `TestLabelKeyConsistency`.

- [ ] **Step 5: Commit**

```bash
git add internal/ecs/flux_test.go
git commit -m "test(ecs): cover Flux degradation and the unmapped-node counter"
```

---

### Task 10: Document the Flux collector

**Files:**
- Modify: `docs/metrics.md`, `CHANGELOG.md`, `config.yaml`, `docs/adr/0011-flux-collector-for-unreachable-metrics.md`

- [ ] **Step 1: Add the mapping table to `docs/metrics.md`**

Add a `## Flux collector (opt-in)` section containing: the prerequisite (`SYSTEM_MONITOR` or `SYSTEM_ADMIN`), the `collectFlux` flag, and the four mapping tables copied verbatim from the spec's "Metric mapping" section — `monitoring_op` per node, `monitoring_op` cluster-scoped, `monitoring_main` per node, `monitoring_vdc` cluster-wide. State explicitly that the `monitoring_vdc` metrics are already per-second and must never be `rate()`d, and that `ecs_node_network_bytes_total` / `ecs_node_requests_total` / `ecs_node_request_bytes_total` are counters that reset on datahead restart.

- [ ] **Step 2: Document the flag in `config.yaml`**

Add beneath the existing `collectDT` comment:

```yaml
    # Query the cluster's Flux monitoring store for per-node performance and
    # cluster DT counters the management API does not serve. Requires the
    # account to hold SYSTEM_MONITOR or SYSTEM_ADMIN. Off by default.
    # collectFlux: true
```

- [ ] **Step 3: Close ADR-0011's open questions**

Change ADR-0011's Status line to record the answers, and replace the "Open questions" section with the resolved table from the spec, linking to `docs/superpowers/specs/2026-07-30-flux-collector-design.md`. Correct the ADR's Context claim that `dtquery_dt_status` gives "DT total / unknown / unready, plus per-node distribution": its tags are `process, tag`, so it is cluster-scoped, and `dtquery_dt_dist_host_dt_node_id` is tagged `dt_node_id, process, tag` with no host.

- [ ] **Step 4: Add the CHANGELOG entry**

```markdown
## [3.2.0]

### Added

- Opt-in `collectFlux` collector querying the cluster's Flux monitoring store
  for per-node CPU, memory and network metrics, per-node request counters, and
  cluster-wide DT and transaction metrics the management API does not serve.
  Off by default; requires `SYSTEM_MONITOR` or `SYSTEM_ADMIN`.
- `ecs_collector_unmapped_nodes{collector="flux"}` reports Flux rows whose host
  tag matched no node in the inventory.

### Changed

- When `collectFlux` is enabled it becomes the sole source of
  `ecs_node_cpu_utilization_percent`, `ecs_node_memory_utilization_percent` and
  `ecs_node_memory_used_bytes`. Every other Flux metric is additive.
```

- [ ] **Step 5: Verify docs build and commit**

Run: `uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict` then `make ci`
Expected: both PASS.

```bash
git add docs/metrics.md docs/adr/0011-flux-collector-for-unreachable-metrics.md CHANGELOG.md config.yaml
git commit -m "docs(flux): record the bucket mapping and close ADR-0011's questions"
```

---

### Task 11: Validate against live traces — GATED

**Blocked on:** Benjamin's 4.3 traces. Do not start this task until they arrive; do not merge Phase 2 to `main` without it.

**Files:**
- Modify: `internal/ecs/testdata/flux_*.json`, `internal/ecs/flux.go`, `internal/ecs/flux_test.go`, `cmd/mockecs/fixtures/`

- [ ] **Step 1: Replace the fixtures with real captures**

Overwrite each `testdata/flux_*.json` with the corresponding real response, anonymised only in hostname and IP values. Re-run `go test ./internal/ecs -run TestFlux -v` and fix whatever the real shape breaks — a `null` cell, an unexpected column, a `Datatypes` mismatch.

- [ ] **Step 2: Check the host tag against the inventory**

Compare the trace's `host` values with `/vdc/nodes` `nodename`. If `lookup` resolves every host, assert it with a test using the real values. If it does not, extend `newNodeMapper`'s candidate keys (`node_id` against an inventory UUID field, for instance) — and only then, because guessing at the key space before seeing it is what the unmapped counter exists to catch.

- [ ] **Step 3: Decide the deferred latency metrics**

With `cq_performance_latency`'s `id` tag values in hand, either add the measurement to `fluxQueries` as `ecs_cluster_request_latency_milliseconds{id,quantile}` with `p50`/`p99` mapped to `quantile`, or record in `docs/metrics.md` why it stays out.

- [ ] **Step 4: Confirm the missing-measurement error shape**

Query a measurement that does not exist. If it answers 200 with empty `Series`, the warn-and-continue path is already correct. If it answers an HTTP error, change `Collect` to distinguish a per-measurement 4xx (warn, continue) from a transport or auth failure (fail the collector), and add a test for each.

- [ ] **Step 5: Sync the demo fixtures**

The fixture *copies* already live in `cmd/mockecs/fixtures/` — Tasks 5 and 8 mirror each file as they add it, because `TestFixturesMatchMockecs` fails otherwise. What remains here is teaching `cmd/mockecs` to **serve** them: answer `POST /flux/api/external/v2/query`, routing on the measurement named in the request body, so `make demo` exercises the collector end to end. Re-sync any fixture whose contents changed when the real traces replaced them, then run `go test ./internal/ecs -run TestFixturesMatchMockecs` to confirm the copies still match byte for byte.

- [ ] **Step 6: Full gate and commit**

Run: `make ci`

```bash
git add -A internal/ecs cmd/mockecs docs
git commit -m "test(flux): validate the collector against live 4.3 traces"
```

---

## Self-Review

**Spec coverage.** Every spec section maps to a task: ping correction → Task 1; `Sample.Type` → Tasks 2-3; ping docs → Task 4; `fluxjson.go` → Task 5; config and arbitration → Task 6; node mapping → Task 7; query template, batching and the four mapping tables → Task 8; degradation and the unmapped counter → Task 9; `docs/metrics.md` mapping table and ADR closure → Task 10; all five "open, pending live traces" items → Task 11.

**Known gaps, deliberate.** `cq_performance_latency` is absent from `fluxQueries` by design (Task 11 Step 3 decides it). `dtquery_dt_status_detailed_type`, the `_head`/`_namespace`/`_method` breakdowns, and the `disk`/`diskio`/`nstat`/`system` measurements are out of scope per the spec.

**Type consistency.** `SampleType`/`Gauge`/`Counter` (Task 2) are consumed unchanged in Tasks 3, 8, 9. `fluxResp`/`fluxRow`/`rows`/`value`/`num` (Task 5) are consumed unchanged in Task 8. `newNodeMapper`/`lookup`/`shortHost` (Task 7) are consumed unchanged in Task 8. `fluxPath`/`fluxQueries`/`fluxQuery.script`/`fluxQuery.samples` are defined in Task 8 and referenced by the Task 8-9 tests only. `Nodes.FluxOwnsPerf` (Task 6) is read in `nodes.go` in the same task.

**One risk worth naming.** Task 6 introduces `Flux{}` before Task 7-8 give it a `Collect` method. The task includes a stub so the package compiles; if the executing agent skips it, `resource.go` will not build.
