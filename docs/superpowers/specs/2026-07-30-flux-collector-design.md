# Flux collector, and the ping-metric correction

Date: 2026-07-30
Status: design approved, implementation split across two releases
Implements: [ADR-0011](../../adr/0011-flux-collector-for-unreachable-metrics.md)

## Context

ADR-0011 accepted an opt-in Flux collector as the route to metric families the
management API does not serve, and left six questions to the implementation PR.
All six are now answered — by the live-4.3 reporter, and independently validated
against the ObjectScale 4.3 admin guide and release notes.

The reporter also found a defect while reviewing the DT collector:
`ecs_node_active_connections` has never carried a connection count. The object
port's ping payload is

```xml
<PingList xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <PingItem><Name>LOAD_FACTOR</Name><Value>1</Value></PingItem>
  <PingItem><Name>MAINTENANCE_MODE</Name><Status>OFF</Status><Text>Data Node is Available</Text></PingItem>
</PingList>
```

and `internal/ecs/dt.go` decodes it as `xml:"PingItem>Value"` into a scalar. Go's
XML decoder keeps the *first* match, so the metric reports LOAD_FACTOR — a load
balancer weight, effectively always 1 — under a name promising connection counts.
The doc comment asserting otherwise is wrong. Neither value in the payload is a
supervision metric; both are signals for a load balancer fronting the node.

### Answers to ADR-0011's open questions

| # | Question | Answer | Source |
|---|---|---|---|
| 1 | Auth and config surface | Same 4443, same `X-SDS-AUTH-TOKEN` from `/login`, role `SYSTEM_MONITOR` or `SYSTEM_ADMIN`. One bool flag, no `flux:` block. | reporter + admin guide, "Flux API" |
| 2 | Bucket/measurement mapping | Table below. Three buckets, not two. | admin guide, "Flux API field descriptions" |
| 3 | Version skew | Warning plus absent series, never a hard collector failure. | reporter; the 4.3 guide confirms `net` has no `utilization` field. Its presence in 3.8 is the reporter's recollection, unverified either way — which is itself the argument for tolerating absence rather than asserting a schema |
| 4 | Response parsing | Structured JSON (`Series`/`Datatypes`/`Columns`/`Values`) via `accept:application/json`. Annotated CSV is offered but not used. | admin guide worked example |
| 5 | Whether DT moves to Flux | Partly. Flux DT is cluster-scoped; per-node DT stays on `collectDT`. | admin guide tag listings |
| 6 | The "DT Query Services" REST section | Not an API surface. `dtquery` is a *process* in the system-process table that "provides REST APIs to get Directory Table details" — internal, not a documented 4443 endpoint. Moot regardless: in 4.x the REST/dashboard layer sources from Flux underneath, so it could not be an independent source. | admin guide, Table 26 |

### What the 4.3 admin guide confirms

- Endpoint is exactly `POST https://<node>:4443/flux/api/external/v2/query`, with
  headers `X-SDS-AUTH-TOKEN`, `accept: application/json`,
  `content-type: application/json`, and body `{"query": "<flux script>"}`. There
  is **no** `org=` query parameter.
- The response shape is
  `{"Series":[{"Datatypes":[…],"Columns":[…],"Values":[[…]]}]}`.
- **Every cell in `Values` is a JSON string**, numbers included (`"_value": "1"`,
  `"table": "0"`). This is the same string-typed-number hazard ADR-0007 records
  for the dashboard payloads, which is why this collector gets its own tolerant
  decode file rather than plain struct tags.
- Of `monitoring_main`: *"Most of the integer fields are increasing counters; that
  is, values that increase over time. Increasing counters restart from zero after
  the datahead service restart."* Documented counter semantics **and** documented
  resets.
- Tags common to all measurements: `host` (name of data node), `node_id` (ID of
  data node), `tag` (internal, set to `dashboard`). In the guide's example `host`
  is an FQDN (`ecs.lss.emc.com`) and `node_id` a UUID.

### What the 4.3 release notes add

OBS04J-596 records that Grafana dashboards **time out against this store with the
default one-hour window**, and prescribes reducing the window to five minutes.
The store punishes wide ranges, so the guide's own `range(start: -30m)` examples
are not a safe default for a collector that runs every cycle.

## Scope: two releases

The work splits cleanly on whether it depends on data we do not have.

**v4.0.0 — no external dependency, shippable now.**
The ping-metric correction (breaking), plus `Sample.Type` as internal groundwork.
The type change exports no new behaviour — the zero value is gauge and every
existing sample stays one — but it touches `sample.go` and both export paths, so
it is worth landing on its own while the Flux half is blocked.

**v4.1.0 — needs live traces.**
The Flux collector. Entirely additive and opt-in; nothing in it is breaking.

## v4.0.0: the ping correction

`pingResp` becomes a slice matched by `Name`, never by position:

```go
type pingItem struct {
	Name   string `xml:"Name"`
	Value  string `xml:"Value"`
	Status string `xml:"Status"`
}

type pingResp struct {
	Items []pingItem `xml:"PingItem"`
}
```

Emitted:

| Metric | Source | Absent when |
|---|---|---|
| `ecs_node_load_factor{node}` | item `Name=LOAD_FACTOR`, `Value` | item missing, or `Value` unparseable |
| `ecs_node_maintenance_mode{node}` | item `Name=MAINTENANCE_MODE`, `Status`: `OFF`→0, `ON`→1 | item missing, or `Status` is any other string |
| `ecs_node_scrape_up{node,endpoint="object"}` | unchanged | never |

An unrecognised `MAINTENANCE_MODE` status yields an absent sample, not 0. Under
ADR-0007 a value we cannot interpret must not be reported as "not in maintenance",
which is the one reading an operator would act on.

`ecs_node_active_connections` is deleted, along with the false doc comment at
`dt.go:27-28`.

Fallout: `docs/metrics.md`, a new `docs/migration-v4.md`, the Grafana panel bound
to `active_connections`, and CHANGELOG.

### `Sample.Type`

```go
type SampleType uint8

const (
	Gauge SampleType = iota // zero value: every existing sample is unchanged
	Counter
)

type Sample struct {
	Name   string
	Labels []Label
	Value  float64
	Type   SampleType
}
```

`prometheus.go` selects `prometheus.CounterValue` for `Counter` instead of the
currently hardcoded `GaugeValue`; `otlp.go` registers an observable counter
rather than an observable gauge for those instruments. Both export paths already
key on metric name, so the type travels with the sample and needs no registry.

Known pre-existing wart, not fixed here: `ecs_cluster_transactions_total` is a
gauge whose name ends in `_total`. It predates this change and renaming it is a
separate breaking decision.

## v4.1.0: the Flux collector

### Config

One per-cluster bool, `collectFlux`, off by default, mirroring `collectDT`. No
new credentials, no bucket or org settings — the collector reuses the cluster's
existing `ecsclient` session. The only operator prerequisite is that the account
holds `SYSTEM_MONITOR` or `SYSTEM_ADMIN`.

### Files

- `internal/ecs/flux.go` — the `ResourceCollector`: query construction, the
  per-measurement loop, node mapping, sample emission.
- `internal/ecs/fluxjson.go` — tolerant decode of `Series`/`Columns`/`Values`.
  Resolves columns by name to index (never by position), parses the
  all-strings cells, and yields nothing for anything it cannot parse. This is the
  `points.go` of this format and exists for the same reason.
- `internal/ecs/flux_test.go`, `internal/ecs/fluxjson_test.go`,
  `internal/ecs/testdata/flux_*.json`.

### Query template

```
from(bucket:"<bucket>")
  |> range(start: -15m)
  |> filter(fn: (r) => r._measurement == "<measurement>")
  |> last()
```

with one extra filter for `cpu`:

```
  |> filter(fn: (r) => r.cpu == "cpu-total")
```

`|> last()` groups per tag set, so one request returns the newest point per node
(and per interface, where that dimension exists) — one pass per cycle,
cluster-wide, never N+1 per node, per ADR-0002.

The 15-minute range is deliberate and sits between two constraints. The guide's
`statDataHead` example writes points five minutes apart, so a five-minute window
can legitimately return nothing; one hour is the window OBS04J-596 says times
out. Fifteen minutes carries two to three points of margin at an order of
magnitude below the failing window.

Eight requests per cycle: four against `monitoring_op`, two against
`monitoring_main`, two against `monitoring_vdc`.

### Metric mapping

The three buckets divide by scope and by whether values arrive pre-rated:
`monitoring_op` is per-node system state, `monitoring_main` per-node cumulative
counters, `monitoring_vdc` VDC-wide values already expressed as rates.

**`monitoring_op` — per node**

| Measurement / field | Metric | Type | Note |
|---|---|---|---|
| `cpu` / `usage_user` | `ecs_node_cpu_utilization_percent{node}` | gauge | filtered to `cpu == "cpu-total"` |
| `mem` / `used_percent` | `ecs_node_memory_utilization_percent{node}` | gauge | |
| `mem` / `used` | `ecs_node_memory_used_bytes{node}` | gauge | |
| `net` / `bytes_recv` | `ecs_node_network_bytes_total{node,interface,direction="received"}` | counter | |
| `net` / `bytes_sent` | `ecs_node_network_bytes_total{node,interface,direction="transmitted"}` | counter | |

**`monitoring_op` — cluster-scoped**

| Measurement / field | Metric | Type |
|---|---|---|
| `dtquery_dt_status` / `total` | `ecs_cluster_dt_total` | gauge |
| `dtquery_dt_status` / `unready` | `ecs_cluster_dt_unready` | gauge |
| `dtquery_dt_status` / `unknown` | `ecs_cluster_dt_unknown` | gauge |

**`monitoring_main` — per node, cumulative**

| Measurement / field | Metric | Type |
|---|---|---|
| `statDataHead_performance_internal_transactions` / `succeed_request_counter` | `ecs_node_requests_total{node,outcome="success"}` | counter |
| `statDataHead_performance_internal_transactions` / `failed_request_counter` | `ecs_node_requests_total{node,outcome="failed"}` | counter |
| `statDataHead_performance_internal_throughput` / `total_read_requests_size` | `ecs_node_request_bytes_total{node,op="read"}` | counter |
| `statDataHead_performance_internal_throughput` / `total_write_requests_size` | `ecs_node_request_bytes_total{node,op="write"}` | counter |

**`monitoring_vdc` — cluster-wide, already per-second**

| Measurement / field | Metric | Type |
|---|---|---|
| `cq_performance_transaction` / `succeed_request_counter` | `ecs_cluster_requests_per_second{outcome="success"}` | gauge |
| `cq_performance_transaction` / `failed_request_counter` | `ecs_cluster_requests_per_second{outcome="failed"}` | gauge |
| `cq_performance_throughput` / `total_read_requests_size` | `ecs_cluster_request_bytes_per_second{op="read"}` | gauge |
| `cq_performance_throughput` / `total_write_requests_size` | `ecs_cluster_request_bytes_per_second{op="write"}` | gauge |

These are rates by the bucket's definition, so they are gauges and must never be
`rate()`d — the same rule the dashboard-sourced per-second metrics already follow.

`cq_performance_latency` (`p50`/`p99`, tag `id`) is **deferred**. The `id` tag's
value space is undocumented, and a label whose values we cannot predict would be
frozen into the metric's schema on first emission. It waits for traces.

Deliberately out of scope for v4.1.0, available for later: `disk`, `diskio`,
`nstat`, `system`, `linux_sysctl_fs`, the `_head` / `_namespace` / `_method`
breakdowns, `dtquery_dt_status_detailed_type`, and the `cq_*` health summaries.

### Arbitration

ADR-0011 requires that exactly one source emit a given metric name per cycle.
The guide makes most of that requirement moot: Flux carries dimensions the
dashboard fields do not (`interface` on `net`, `outcome` on the transaction
counters where the dashboard has `op`), and under ADR-0006 a differing label-key
set forces a different metric name anyway. Flux is therefore *additive* for
everything except three names where the shapes genuinely coincide:

- `ecs_node_cpu_utilization_percent{node}`
- `ecs_node_memory_utilization_percent{node}`
- `ecs_node_memory_used_bytes{node}`

For those three, `Registry(cl)` decides ownership once, at construction, before
any request is issued: when `collectFlux` is set, `Nodes` skips them and `Flux`
emits them. One place in the codebase answers "who emits this name".

`ecs_node_nic_bandwidth` and `ecs_node_nic_utilization_percent` stay
dashboard-only — Flux's `net` cannot fill them, because the per-interface
dimension makes it a different series shape.

`collectDT` is untouched and remains the only source of per-node
`ecs_node_dt_*`. `collectFlux` and `collectDT` are independent; either, both, or
neither. An operator on a segmented network runs `collectFlux` alone and gets
cluster DT health without opening 9101, at the cost of the per-node breakdown.

### Per-cycle flow

1. `GET /vdc/nodes` — build the node mapper.
2. For each of the eight measurements: POST the query, decode the response.
3. Resolve each row's `host` tag to a `nodename`.
4. Emit samples, typed gauge or counter per the table.

### Node mapping

Flux tags rows with `host` (an FQDN in the guide's example) and `node_id` (a
UUID). Neither is guaranteed to equal the `/vdc/nodes` `nodename` that every
other collector uses as the `node` label, and series that do not join are worse
than absent (ADR-0011).

The mapper indexes each inventory node under several candidate keys — `nodename`,
`nodename` truncated at the first `.`, `mgmtIP`, `dataIP` — and looks up the Flux
`host` both whole and truncated at the first `.`, case-insensitively. First match
wins.

A row whose `host` matches nothing emits **no sample**, and increments
`ecs_collector_unmapped_nodes{collector="flux"}`. Without that counter, a cluster
whose tag space we guessed wrong would report a healthy collector producing no
data — the failure mode hardest to notice and likeliest to occur, since this is
the one assumption no document settles.

### Degradation

| Condition | Behaviour |
|---|---|
| Endpoint unreachable, 401, 403 | `ecs_collector_up{collector="flux"}=0`, no samples, every other collector unaffected |
| One measurement absent or renamed | warning logged once per cycle, samples absent, collector stays up |
| Column missing, or a cell that will not parse | that sample absent (ADR-0007) |
| `host` unmappable | sample dropped, `ecs_collector_unmapped_nodes` incremented |

The response body is never logged at `--trace` without the auth-path skip the
family tracing rule requires.

### Testing

- `fluxjson` against real trace fixtures, plus hostile inputs: reordered columns,
  a missing column, a cell of the wrong type, empty `Values`, extra columns,
  `Series` empty.
- Collector against `ecsclient.Mock`.
- Both export paths asserted — registry gather and OTLP `ManualReader` — with the
  counter type included, since this is the first sample type either path has had
  to distinguish.
- `TestLabelKeyConsistency` stays green.
- A new test asserting no metric name is emitted by two sources in one cycle,
  with `collectFlux` on and off.
- Node-mapper tests covering FQDN-versus-short-name, IP-keyed hosts, and the
  unmapped path incrementing the counter.

`docs/metrics.md` gains the mapping table above **before** any Flux code is
written, per ADR-0011's second open question.

## Open, pending live traces

Everything above is structure; these are values only a real 4.3 response settles.

1. What `host` actually contains on a production cluster, next to `/vdc/nodes`
   `nodename`. Decides whether the mapper's candidate-key list is sufficient.
2. `cq_performance_latency`'s `id` tag value space — gates the deferred latency
   metrics.
3. Whether `dtquery_dt_status` is populated in practice, not merely documented.
4. What a query against a non-existent measurement returns: HTTP error, or 200
   with empty `Series`. Decides how "measurement renamed" is detected.
5. Whether any cell is ever `null` rather than a string.

## Related

- [ADR-0011](../../adr/0011-flux-collector-for-unreachable-metrics.md) — the accepted direction and its constraints.
- [ADR-0006](../../adr/0006-metric-naming-units-and-label-invariant.md) — why differing label keys force a different metric name.
- [ADR-0007](../../adr/0007-obs-4-1-api-alignment.md) — absent, never zero.
- [ADR-0002](../../adr/0002-prometheus-snapshot-model.md) — one pass per cycle.
- [ADR-0009](../../adr/0009-modular-resource-collectors.md) — the collector interface and per-collector degradation.
- ObjectScale 4.3 admin guide, "Advanced Monitoring → Flux API" and "Flux API field descriptions".
- ObjectScale 4.3 release notes, OBS04J-596.
