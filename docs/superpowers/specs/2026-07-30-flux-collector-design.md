# Flux collector, and the ping-metric correction

Date: 2026-07-30
Status: design approved, implementation split across two releases
Implements: [ADR-0011](../../adr/0011-flux-collector-for-unreachable-metrics.md)

## Context

ADR-0011 accepted an opt-in Flux collector as the route to metric families the
management API does not serve, and left six questions to the implementation PR.
All six are now **answered**, which is not the same as validated. The answers come
from the ObjectScale 4.3 admin guide, release notes and REST API reference; only
questions 1 (auth) and 3 (version skew) carry live-cluster confirmation from the
reporter. The bucket and measurement mapping, the response envelope, and the
`host`-tag node identity are documentation-derived and remain unconfirmed against
a running cluster — see "Open, pending live traces" below, which is the
authoritative statement of validation status for this design.

The reporter also found a defect while reviewing the DT collector. The object
port's ping payload is

```xml
<PingList xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <PingItem><Name>LOAD_FACTOR</Name><Value>1</Value></PingItem>
  <PingItem><Name>MAINTENANCE_MODE</Name><Status>OFF</Status><Text>Data Node is Available</Text></PingItem>
</PingList>
```

and `internal/ecs/dt.go` decodes it as `xml:"PingItem>Value"` into a scalar. Go's
XML decoder keeps the *first* match, so `ecs_node_active_connections` reports the
`Value` of whichever `PingItem` happens to appear first.

The metric's **name** is correct. The 4.3 REST reference documents `GET ?ping` as
returning a load factor, and states plainly: *"The Load Factor value is the number
of active Jetty connections on that node."* An early reading of this defect held
that the metric was meaningless and had to be removed; the reference contradicts
that, and the reporter's observed value of `1` reads as a genuinely idle node
rather than a constant.

The **decode** is the defect. The reference documents `PingItem` as `0-*`
elements with no guaranteed ordering, and the `&item=load-factor` query parameter
changes which items are present at all. A positional decode therefore reports the
first item's `Value` whatever that item is — correct today by luck of ordering,
silently wrong the moment ordering changes or LOAD_FACTOR is absent. Matching by
`Name` removes the coincidence.

Because the name survives, this correction is **not breaking**: no series is
removed or renamed, and the existing Grafana panel keeps working.

### Answers to ADR-0011's open questions

| # | Question | Answer | Source |
| --- | --- | --- | --- |
| 1 | Auth and config surface | Same 4443, same `X-SDS-AUTH-TOKEN` from `/login`, role `SYSTEM_MONITOR` or `SYSTEM_ADMIN`. One bool flag, no `flux:` block. | reporter + admin guide, "Flux API" |
| 2 | Bucket/measurement mapping | Table below. Three buckets, not two. | admin guide, "Flux API field descriptions" |
| 3 | Version skew | Warning plus absent series, never a hard collector failure. | reporter; the 4.3 guide confirms `net` has no `utilization` field. Its presence in 3.8 is the reporter's recollection, unverified either way — which is itself the argument for tolerating absence rather than asserting a schema |
| 4 | Response parsing | Structured JSON (`Series`/`Datatypes`/`Columns`/`Values`) via `accept:application/json`. Annotated CSV is offered but not used. | admin guide worked example |
| 5 | Whether DT moves to Flux | Partly. Flux DT is cluster-scoped; per-node DT stays on `collectDT`. | admin guide tag listings |
| 6 | The "DT Query Services" REST section | Does not exist as an API surface. In the 4.3 REST reference it is a navigation category whose sole child is *Data Migration* — an orphaned heading from Dell's doc generator, with no methods beneath it. No service in the reference exposes DT counters. `dtquery` is a *process* in the admin guide's system-process table that "provides REST APIs to get Directory Table details" — internal to the cluster, not published on 4443. | 4.3 REST reference navigation tree; admin guide Table 26 |

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

### What the 4.3 REST reference settles

The full 4.3 REST API reference closes the two remaining "is there another way?"
questions, both negatively:

- **No DT endpoint exists on 4443.** The reference's service list contains no DT
  or directory-table service, and the "DT Query Services" heading is an empty
  navigation category (see Q6 above). Flux is not merely the best source for DT
  counters; it is the only one.
- **Nothing on 4443 competes with Flux for the perf fields.** `MonitoringService`
  — the one service whose name suggests it might — exposes `getAuditEvents` and
  nothing else. `DashboardApiRouter` is the dashboard path the exporter already
  uses, and the fields in question are absent from its payloads on 4.3.

It also documents the ping payload the DT collector scrapes: `PingList` contains
`0-*` `PingItem` elements with `Name`, `Status`, `Text` and `Value`, all typed
String; `MAINTENANCE_MODE` reports `ON`, `OFF`, or `UNKNOWN`; and
`&item=load-factor` returns the load factor alone, avoiding "the performance
penalty of checking Maintenance Mode."

## Scope: two releases

The work splits cleanly on whether it depends on data we do not have. Neither
release is breaking, so neither needs a migration guide.

**v3.1.0 — no external dependency, shippable now.**
The ping-decode correction, plus `Sample.Type` as internal groundwork. The type
change exports no new behaviour — the zero value is gauge and every existing
sample stays one — but it touches `sample.go` and both export paths, so it is
worth landing on its own while the Flux half is blocked.

**v3.2.0 — needs live traces.**
The Flux collector. Entirely additive and opt-in.

## v3.1.0: the ping correction

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
| --- | --- | --- |
| `ecs_node_active_connections{node}` | item `Name=LOAD_FACTOR`, `Value` | item missing, or `Value` unparseable |
| `ecs_node_maintenance_mode{node}` | item `Name=MAINTENANCE_MODE`, `Status`: `OFF`→0, `ON`→1 | item missing, or `Status` is `UNKNOWN` or any other string |
| `ecs_node_scrape_up{node,endpoint="object"}` | unchanged | never |

`ecs_node_active_connections` keeps its name — the reference confirms it is
accurate — and gains a decode that cannot silently read the wrong item.

An unrecognised `MAINTENANCE_MODE` status yields an absent sample, not 0. The
reference documents `UNKNOWN` as a real third value alongside `ON` and `OFF`, so
this is not defensive coding against a hypothetical: under ADR-0007 a status the
cluster itself calls unknown must not be reported as "not in maintenance", which
is the one reading an operator would act on.

The doc comment at `dt.go:27-28` is rewritten: the `Value` it describes is right,
but it documents a scalar field that no longer exists.

**`&item=load-factor` considered and rejected.** It would avoid the documented
maintenance-mode penalty, but that penalty is described as a concern for
discovering load factor "with greater frequency" — a load balancer's polling
rate, not one scrape per node per collection interval. Maintenance mode is the
more operationally interesting of the two values, and dropping it to save a cost
nobody has measured on a real cluster is a poor trade. Revisit if a live cluster
shows the full ping is actually slow.

Fallout: `docs/metrics.md` and CHANGELOG. No migration guide, and no Grafana
change is required — though a `maintenance_mode` panel is worth adding.

Tests in `dt_test.go`, against the reporter's verbatim 4.3 payload:

- `active_connections` is the LOAD_FACTOR value, and stays correct when the two
  `PingItem` elements are **reversed** — the case the current decode fails and
  the whole reason for the change.
- `maintenance_mode` is 0 for `OFF`, 1 for `ON`, absent for `UNKNOWN`, absent for
  an unrecognised string.
- A `PingList` with zero items emits neither metric, and still emits
  `scrape_up{endpoint="object"}=1`.
- An item carrying `Status` but no `Value` does not emit a zero.

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

## v3.2.0: the Flux collector

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
| --- | --- | --- | --- |
| `cpu` / `usage_user` | `ecs_node_cpu_utilization_percent{node}` | gauge | filtered to `cpu == "cpu-total"` |
| `mem` / `used_percent` | `ecs_node_memory_utilization_percent{node}` | gauge | |
| `mem` / `used` | `ecs_node_memory_used_bytes{node}` | gauge | |
| `net` / `bytes_recv` | `ecs_node_network_bytes_total{node,interface,direction="received"}` | counter | |
| `net` / `bytes_sent` | `ecs_node_network_bytes_total{node,interface,direction="transmitted"}` | counter | |

**`monitoring_op` — cluster-scoped**

| Measurement / field | Metric | Type |
| --- | --- | --- |
| `dtquery_dt_status` / `total` | `ecs_cluster_dt_total` | gauge |
| `dtquery_dt_status` / `unready` | `ecs_cluster_dt_unready` | gauge |
| `dtquery_dt_status` / `unknown` | `ecs_cluster_dt_unknown` | gauge |

**`monitoring_main` — per node, cumulative**

| Measurement / field | Metric | Type |
| --- | --- | --- |
| `statDataHead_performance_internal_transactions` / `succeed_request_counter` | `ecs_node_requests_total{node,outcome="success"}` | counter |
| `statDataHead_performance_internal_transactions` / `failed_request_counter` | `ecs_node_requests_total{node,outcome="failed"}` | counter |
| `statDataHead_performance_internal_throughput` / `total_read_requests_size` | `ecs_node_request_bytes_total{node,op="read"}` | counter |
| `statDataHead_performance_internal_throughput` / `total_write_requests_size` | `ecs_node_request_bytes_total{node,op="write"}` | counter |

**`monitoring_vdc` — cluster-wide, already per-second**

| Measurement / field | Metric | Type |
| --- | --- | --- |
| `cq_performance_transaction` / `succeed_request_counter` | `ecs_cluster_requests_per_second{outcome="success"}` | gauge |
| `cq_performance_transaction` / `failed_request_counter` | `ecs_cluster_requests_per_second{outcome="failed"}` | gauge |
| `cq_performance_throughput` / `total_read_requests_size` | `ecs_cluster_request_bytes_per_second{op="read"}` | gauge |
| `cq_performance_throughput` / `total_write_requests_size` | `ecs_cluster_request_bytes_per_second{op="write"}` | gauge |

These are rates by the bucket's definition, so they are gauges and must never be
`rate()`d — the same rule the dashboard-sourced per-second metrics already follow.

`cq_performance_latency` (`p50`/`p99`, tag `id`) is **deferred**. The `id` tag's
value space is undocumented, and a label whose values we cannot predict would be
frozen into the metric's schema on first emission. It waits for traces.

Deliberately out of scope for v3.2.0, available for later: `disk`, `diskio`,
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
`host` both whole and truncated at the first `.`, case-insensitively.

A candidate key claimed by **two different nodes** identifies neither, so it is
invalidated rather than resolved: the lookup fails, and the row is dropped and
counted like any other unmappable host. Resolving it to whichever node happened
to be indexed last would attribute that node's series to the wrong one, and a
wrong join is indistinguishable downstream from a right one — strictly worse
than the dropped row. One node re-registering *its own* key is not a collision
and still resolves; that happens routinely, since `mgmtIP` equals `dataIP` on a
flat network and an unqualified `nodename` equals its own truncated form.

A row whose `host` matches nothing emits **no sample**, and increments
`ecs_collector_unmapped_nodes{collector="flux"}`. Without that counter, a cluster
whose tag space we guessed wrong would report a healthy collector producing no
data — the failure mode hardest to notice and likeliest to occur, since this is
the one assumption no document settles.

### Degradation

| Condition | Behaviour |
| --- | --- |
| Any query fails at the transport or auth layer — unreachable, TLS failure, 401, 403, or any non-2xx | `Collect` returns an error, so `ecs_collector_up{collector="flux"}=0`, no samples, every other collector unaffected |
| A query returns 2xx but no rows, because the measurement is absent or was renamed | warning logged once per cycle for that measurement, its samples absent, the collector and its other seven queries stay up |
| Column missing, or a cell that will not parse | that sample absent (ADR-0007) |
| `host` unmappable | sample dropped, `ecs_collector_unmapped_nodes` incremented |

That split rests on an assumption no document settles: that a query naming a
measurement the cluster does not carry answers 2xx with an empty `Series`, rather
than an HTTP error. It is open question 4 below. If a real cluster answers with a
4xx instead, a single renamed measurement would fail the whole collector under the
table's first row — so the code must then learn to distinguish a per-measurement
4xx (warn, continue) from a transport or auth failure (fail the collector).

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
- ObjectScale 4.3 REST API reference (`OBS_4.3.0.0_REST_API`), "S3 Ping → Ping", the service list, and the navigation tree that closes Q6.
