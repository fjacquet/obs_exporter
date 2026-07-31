# Flux collector, confronted with a live 4.3 cluster

Date: 2026-07-31
Status: design approved, targets v3.3.0
Implements: [ADR-0011](../../adr/0011-flux-collector-for-unreachable-metrics.md),
amends [ADR-0004](../../adr/0004-token-auth-retry-policy.md)
Supersedes the "Open, pending live traces" section of the
[2026-07-30 Flux collector design](2026-07-30-flux-collector-design.md)

## Context

The Flux collector shipped in v3.2.0 against documentation. ADR-0011 said so
plainly: the bucket and measurement mapping, the response envelope, and the
`host`-tag node identity were read from the ObjectScale 4.3 admin guide and REST
reference, never observed on a running cluster, and a future maintainer should
treat that ADR as recording a deferred verification rather than a closed one.

On 2026-07-31 the live-cluster reporter delivered that verification: a capture
from a 4.3 acceptance cluster (`4.3.0.0.142978`, five nodes, `SYSTEM_MONITOR`),
comprising a written report, 135 sanitized trace files covering roughly 66
measurements in both JSON and CSV, and a same-run dashboard comparison. A second
message the same day added a review of the shipped v3.2.0 code against those
traces.

The mapping survives intact. What the traces change is narrower and sharper than
a redesign: three defects in how the collector behaves around its queries, two
metric families the traces prove are reachable and the shipped code does not
collect, and one architectural constraint that turns several of the collector's
design choices from preferences into the only available shape.

**Cluster caveat, carried from the report.** This is an acceptance cluster: real
internal S3 traffic but no production load, no active replication, at rest. The
*shape* of every payload is representative; transaction *values* are not, and the
replication, rebalance and recovery measurements are empty for want of activity.
Separately, `*_Process_status` and `diskio` are empty for reasons unrelated to
activity — probably not collected on that build. Nothing in this design depends
on any of them.

## What the traces confirm, requiring no change

Recorded because "we checked and it was right" is worth as much as a defect, and
because ADR-0011 currently claims these are unverified:

- **The response envelope.** `fluxjson.go` decodes the real payload as written:
  columns resolved by name, every cell a JSON string including numbers, an
  unreadable cell yielding an absent sample. No change.
- **The absent-measurement shape.** A query for a measurement the store does not
  carry answers HTTP 200 with `Series:[{Datatypes:null,Columns:null,Values:null}]`,
  which `rows()` already flattens to nothing, producing a warning and absent
  samples rather than an error.
- **Every measurement, field and tag in the shipped table**, including
  `cq_performance_transaction` and `cq_performance_throughput`, which an early
  reading of the attachment list had wrongly suspected of being absent — the
  reporter confirmed both emit. The `cpu` measurement carries only a `cpu-total`
  row per node on this build, so the `cpu == "cpu-total"` filter is correct and
  costs nothing.
- **The node join.** Flux `host` equals `/vdc/nodes` `nodename` equals the
  dashboard's `displayName`, five identical strings. No transformation.
- **`dtquery_dt_status` is cluster-scoped** — tagged `process, tag`, no `host`,
  no `node_id` — exactly as ADR-0011's correction states.
- **The `-15m` window.** Every measurement in the table writes points five
  minutes apart, so the window carries two to three points of margin. The store
  has slower classes (10–25 minutes, and sparse or event-driven for the internal
  GC, EC, chunk, rebalance and recovery measurements), but nothing this exporter
  queries through Flux is in one — and those slower domains are the ones the
  exporter already sources from the dashboard, so it is unlikely ever to want
  them here. Should a measurement outside the five-minute class ever be added,
  the window becomes a per-query setting rather than a constant.

## The constraint that justifies the architecture

The external Flux API is a whitelist of six operations, and says so when refused:

```
operation 'mean' is not allowed. Allowed operations:
influxDBFrom, filter, range, last, drop, keep
```

No server-side aggregation or transformation of any kind — no `mean`,
`aggregateWindow`, `derivative`, `elapsed`, `group`, `sort`. Three of the
collector's existing choices stop being choices:

- `last()` is the only available terminal operator, so "newest point per series,
  one request per measurement" is the only shape a query can take;
- every rate must be computed by Prometheus, because the store will not compute
  one;
- and the Flux `schema` package being unsupported (no runtime introspection)
  means the measurement-to-metric mapping is necessarily hard-wired.

This belongs in ADR-0011 as a constraint, not a note. It is also the reason a
future maintainer should not "optimise" a query by pushing work server-side:
there is no server-side.

`drop` and `keep` *are* allowed, which leaves one lever available for payload
size — `net` returns 220 rows of 11 columns per cycle. If it is ever used it must
be applied **after** `last()`, since dropping a column before the terminal
operator changes the group key and therefore what "last" means. Not part of this
design; recorded so the option is not rediscovered as a novelty.

## Design

### 1. Staleness: absent, never stale

`last()` returns the newest point in the window regardless of its age, and this
exporter emits samples without timestamps, so Prometheus stamps them at scrape
time. A node or service that stops writing therefore keeps a value that looks
current for up to the width of the window — fifteen minutes of a number that is
no longer true, indistinguishable from a fresh one.

Each row already carries `_time`. `fluxQuery.samples` drops any row whose
`_time` is older than `fluxMaxAge` (10 minutes, twice the observed five-minute
cadence), or whose `_time` cannot be parsed. The threshold is a package constant
with a per-query override field, empty everywhere today, for the same reason the
window would become per-query: a measurement in a slower cadence class would need
its own.

This is ADR-0007's absent-never-zero rule extended along the time axis, and it is
the one place this design adds a rule rather than correcting one.

No new metric counts dropped-stale rows. A node that goes silent makes its series
**disappear**, and `absent()` is the Prometheus idiom for alerting on exactly
that — which is what the absent-never-zero rule buys in the first place. The
count goes into the per-measurement debug line from section 7.

### 2. The overloaded HTTP 500

The store answers a permission refusal with HTTP 500 and a structured body:

```json
{"code":6401,"description":"Insufficient permissions","retryable":false}
```

and answers an invalid query with HTTP 500 and `{"error":"failed to compile
query: …"}`. The status code alone cannot tell a permanent refusal from a query
bug from a genuine transient failure. Today `client.go:88` retries anything
`>= 5xx` twice, so a `SECURITY_ADMIN`-only account produces three requests per
measurement per cycle, all refused, and `client.go:146` reports the outcome as
`status 500` with no cause.

`internal/ecsclient` gains an exported error type:

```go
type APIError struct {
    Status      int
    Code        int    // 6401 = Insufficient permissions
    Description string
    Retryable   bool
    Body        string // raw body when it does not decode
}
```

`call()` returns it in place of the current bare `fmt.Errorf`, and the retry
condition stops retrying a 5xx whose body says `retryable:false`. Both changes
serve every collector, not just Flux: `{code,description,retryable}` is the
general ECS error envelope, not a Flux peculiarity. This amends ADR-0004, whose
policy is currently expressed purely in terms of status classes.

### 3. Partial failure

A single failing query currently aborts `Collect` and zeroes
`ecs_collector_up{collector="flux"}`, so one renamed measurement or one query bug
costs the operator the other nine that worked. The collector already tolerates an
*empty* result per measurement for precisely this reason — the code comment says
"a rename must not take the other seven down with it". Errors get the same
treatment, split by blast radius:

- **Global** — authentication failure, a permission refusal (`retryable:false` /
  code 6401), or a transport failure reaching the cluster. Nothing else will
  work, so `Collect` returns the error immediately, without issuing the remaining
  queries, and `ecs_collector_up{collector="flux"}` goes to 0.
- **Per-query** — a 500 carrying a compile error, or a body that does not decode.
  Logged with its bucket and measurement, that measurement skipped, the rest of
  the table attempted. The collector fails only if *no* query succeeded.

### 4. Per-node directory tables

`dtquery_dt_dist_host_dt_node_id` is tagged `dt_node_id, process, tag` — no
`host`, which is what ADR-0011's correction observed and why it concluded Flux
could not report DT per node. The traces show the conclusion does not follow:
`dt_node_id` holds the node's **`data_ip`**, and the five per-node `count_i`
values sum to 1936, exactly the cluster `dtquery_dt_status.total` from the same
run. It is a per-node breakdown under a different column name.

- `fluxQuery` gains `hostTag string`, defaulting to `"host"`, set to
  `"dt_node_id"` for this one query. `nodeMapper` already indexes `DataIP`, so
  the join needs no change.
- `vdcNodesResp` gains `private_ip` and `data2_ip`, indexed alongside the
  existing keys. Cheap insurance: on a cluster whose `dtquery` reports the
  private address instead, the row joins rather than incrementing
  `ecs_collector_unmapped_nodes`. On the captured cluster `data2_ip` equals
  `data_ip`, which is harmless: `newNodeMapper` only blanks a key when two
  *different* nodes claim it, and here one node claims it twice.
- Ownership is decided in `Registry` (`resource.go`), where `Nodes`' arbitration
  already lives: `Flux{DTOwnedByDT: cl.CollectDT}`. With `collectDT` on, the
  native collector keeps the name and Flux does not issue the query at all. With
  `collectDT` off, Flux emits `ecs_node_dt_total{node}`. One name, one owner,
  settled before any request.

`collectDT` wins because it is the richer source where it is reachable: it serves
`unready` and `unknown` per node as well, and Flux has no per-node breakdown of
either. So ADR-0011's consequence — "`collectFlux` is not a path to retiring
`collectDT`" — is narrowed rather than reversed: on the segmented topology where
`collectDT` cannot work at all, `collectFlux` now covers the per-node DT *count*,
and `unready`/`unknown` stay uncovered.

### 5. Request latency as a histogram

`statDataHead_performance_internal_latency` carries ten fields whose *names* are
bucket bounds (`0.0`, `1.0`, `4.814963904455889`, … `59999.999999999985`,
`+Inf`), tagged `host` and `id`. The values are cumulative and monotonic, with
`+Inf` equal to the last finite bound — a Prometheus histogram in every respect
except that no `_sum` is served.

`id` takes two values, `ttfb_read` and `ttlb_write`, and the rows carry
`tag=dashboard`: this measurement is the source of the read/write latency the ECS
dashboard displays itself, which is what justifies mapping them onto the `op`
dimension the exporter already uses.

| Emitted | Labels | Type |
| --- | --- | --- |
| `ecs_node_transaction_latency_milliseconds_bucket` | `node, op, le` | counter |
| `ecs_node_transaction_latency_milliseconds_count` | `node, op` | counter |

`le` carries the field name verbatim, which is already a valid Prometheus bound
including `+Inf`; `_count` is the `+Inf` bucket. No `Histogram` sample type is
introduced — buckets are ordinary counter samples carrying an `le` label, which
is what `histogram_quantile` consumes.
`prometheus.MustNewConstHistogram` is not usable here precisely because it
requires the `_sum` the store does not serve. The consequence for operators —
quantiles yes, average latency no — goes in `docs/metrics/flux.md`.

`fluxQuery` therefore gains a bucket mode, in which an unlisted field name
becomes an `le` value rather than being ignored. It is the only place in the
collector where a field name is data instead of a key.

**Family ownership.** `ecs_node_transaction_latency_milliseconds` already exists
as a gauge from the dashboard path (`fields_shared.go:30`), and Prometheus reads
`X_bucket` as belonging to a histogram named `X` — two owners for one family.
Resolved the way the collector already resolves this class of conflict: when
`collectFlux` is on, `Nodes` stops emitting
`ecs_node_transaction_latency_milliseconds`, exactly as it already stops emitting
the CPU and memory names. `ecs_cluster_transaction_latency_milliseconds` is
untouched — Flux has no cluster-level equivalent to contest it.

### 6. Log noise

Every absent measurement currently logs a warning every cycle, which on a cluster
that legitimately does not carry one produces a permanent stream the operator
cannot act on. `Flux` keeps a set of measurements already seen silent, held on
the collector value built by `Registry` — so a config reload rebuilds it and
re-warns once, which is the wanted behaviour. First silent cycle warns; later
ones log at debug; a measurement that starts answering again clears its entry, so
a later disappearance warns afresh.

### 7. Tracing, for the September validation round

The reporter validates this work on a live 4.3 on return in September, by email
only. Today `--trace` (`client.go:97`) logs cluster, method, URL, status and
body — which works for the dashboard, where each resource has its own URL, and
fails for Flux, where ten queries share one path and the request body is not
logged at all. A Flux trace would be ten indistinguishable blocks.

- **Attribution.** Flux calls carry their bucket and measurement in the log
  fields, and each measurement gets one debug line: rows read, samples emitted,
  rows dropped for an unresolvable host, rows dropped as stale.
- **`flux-capture` subcommand.** The CLI is already cobra (`main.go`). A
  subcommand runs the query table once against a named cluster from the config
  and writes one file per measurement plus a summary — the artifact the reporter
  assembled by hand, in one command. It accepts a free measurement name too, so
  an open question can be answered without hand-written `curl`.
- **A validation checklist page** saying what to run and what to send back, so
  the September round trip is a command rather than a campaign.

Sanitizing stays with the reporter: he has a working process, it is his data
policy rather than ours, and half-done automatic redaction is worse than none.

### 8. Fixtures and the demo harness

`cmd/mockecs` does not serve `POST /flux/api/external/v2/query`, so `make demo`
has never exercised this collector end to end.

`internal/ecs/testdata/flux/` receives the real traces for the measurements the
collector queries — `cpu`, `mem`, `net`, `dtquery_dt_status`,
`dtquery_dt_dist_host_dt_node_id`, `statDataHead_performance_internal_transactions`,
`_throughput` and `_latency` — as the JSON body extracted from each `.json.txt`,
plus the empty-envelope case. `cmd/mockecs` serves the endpoint, selecting a
fixture by the measurement name found in the request body and answering with the
empty envelope for anything else. `fixtures_sync_test.go` covers the copy as it
does for every other fixture.

Two of the ten queried measurements have **no attached trace**:
`cq_performance_transaction` and `cq_performance_throughput`. The reporter
confirmed in prose that both emit with the mapped fields, but sent no payload for
either, so their fixtures have to be written by hand from the envelope shape the
other `monitoring_vdc` captures establish. They are marked as synthesized in a
header comment, and requesting the two real captures goes on the September
checklist from section 7 — a hand-written fixture proves the collector's own
logic and nothing about the cluster.

Because fixtures are captured at a fixed instant and section 1 drops rows older
than ten minutes, the fixture timestamps have to be rewritten relative to test
time rather than replayed literally — otherwise every fixture-driven test would
correctly drop every row. Tests inject a clock; `mockecs` rewrites `_time` on the
way out so the demo stack shows live-looking data.

### 9. Documentation that follows the metric

`docs/metrics/flux.md` (the two new rows, the DT ownership note, the no-`_sum`
note, the staleness note), `docs/metrics/index.md`, ADR-0011 (questions 2, 4 and 5
move from documentation-derived to live-confirmed; the operation whitelist becomes
a recorded constraint; the DT consequence is narrowed), ADR-0004 (retry reads the
body), `grafana/dashboards/` for the latency histogram and per-node DT, and the
CHANGELOG.

## Testing

- `TestLabelKeyConsistency` stays green with `le` in play.
- `APIError` decodes; a 5xx with `retryable:false` is not retried; a 5xx without
  it still is.
- Global versus per-query failure: a 6401 aborts the cycle without issuing later
  queries; a compile-error 500 on one measurement leaves the others' samples
  intact; all-queries-failed still zeroes `ecs_collector_up`.
- `dt_node_id` joins to `node` via `data_ip`; DT arbitration tested both ways.
- Cumulative buckets map to the right `le` values and `_count` equals `+Inf`.
- A row older than `fluxMaxAge` yields no sample; one inside it does; an
  unparseable `_time` yields no sample.
- A silent measurement warns once, then logs at debug, then warns again after it
  returns.
- Both export paths — the Prometheus registry gather and the OTLP
  `ManualReader` — assert a sample of buckets, as the family standard requires.

## Open questions, for the September round

Carried, not resolved, and none of them blocks this work:

1. The unit of the latency bucket bounds. Milliseconds is assumed, consistent
   with `+Inf` at 60000 and with the existing metric name; the store does not
   document it.
2. The meaning of the generic `tag` column (`system` / `dashboard` / `dt`).
3. Whether `*_Process_status` and `diskio` are absent on that build or absent
   everywhere. Nothing here depends on them.
4. Everything the acceptance cluster cannot show: replication, rebalance and
   recovery under load, and representative transaction values.
5. Real captures for `cq_performance_transaction` and `cq_performance_throughput`,
   whose fixtures are hand-written for want of an attached trace (section 8).

## Related

- [ADR-0011](../../adr/0011-flux-collector-for-unreachable-metrics.md) — the
  collector this validates.
- [ADR-0004](../../adr/0004-token-auth-retry-policy.md) — the retry policy this
  amends.
- [ADR-0006](../../adr/0006-metric-naming-units-and-label-invariant.md) — why one
  name has one owner and one label-key set.
- [ADR-0007](../../adr/0007-obs-4-1-api-alignment.md) — absent, never zero, which
  section 1 extends to time.
- [2026-07-30 Flux collector design](2026-07-30-flux-collector-design.md) — the
  documentation-derived design this confronts with live evidence.
