# The Flux collector

ObjectScale 4.3 dashboard payloads omit per-node performance fields the API
reference documents, and the directory-table stats live on a network a
segmented cluster does not route. This collector reads both from the
cluster's own monitoring store instead.

Queries the cluster's Flux/InfluxDB monitoring store
(`POST /flux/api/external/v2/query`) for metric families the management REST
API does not serve. It is reachable on the same management port and reuses
the same `X-SDS-AUTH-TOKEN` session as every other collector — no new
credentials or config beyond the flag. Off by default; the cluster account
must hold `SYSTEM_MONITOR` or `SYSTEM_ADMIN`. See
[ADR-0011](../adr/0011-flux-collector-for-unreachable-metrics.md) and its linked
[design spec](../superpowers/specs/2026-07-30-flux-collector-design.md) for the
full rationale.

Three buckets divide the metrics by scope and by whether values arrive
pre-rated:

- `monitoring_op` — per-node system state (CPU, memory, network), plus one
  cluster-scoped measurement.
- `monitoring_main` — per-node cumulative counters that **restart from
  zero** when the datahead service restarts.
- `monitoring_vdc` — VDC-wide values **already expressed as per-second
  rates**.

Every query below issues once per cycle, cluster-wide, closed with `last()`
and no host filter — one request per measurement, never per node and never
narrower. That shape is forced, not chosen: the external Flux API enforces a
whitelist of exactly six operations and refuses anything else, `mean`
included:

```
operation 'mean' is not allowed. Allowed operations:
influxDBFrom, filter, range, last, drop, keep
```

`last()` is the only terminal operator on offer, so a rate, an average or a
grouped rollup computed by the store itself is off the table for every
measurement here — each one is Prometheus's job, downstream of what this
collector publishes. Confirmed live 2026-07-31.

!!! note "Absent, never stale"
    `last()` returns the newest point in the window regardless of its age, and
    these samples carry no timestamp of their own — Prometheus stamps them at
    scrape time. Without a separate check, a node or service that stopped
    writing would keep a value that looks current for the full width of the
    query window. Every measurement here writes points five minutes apart on a
    live 4.3, so a row older than ten minutes is dropped even when it is the
    newest one available — two missed writes of slack, not zero. A node that
    stops emitting therefore goes absent within two cadence periods rather
    than holding a stale reading indefinitely. Alert with
    [`absent()`](reading.md#absent-never-zero), the same idiom this exporter's
    absent-never-zero rule already uses elsewhere — never on a zero, since a
    stale row is not zero, it is just old.

!!! warning "Rate direction is not the same in every bucket"
    The `monitoring_vdc`-sourced metrics (`ecs_cluster_requests_per_second`,
    `ecs_cluster_request_bytes_per_second`) are already rates and must
    **never** be wrapped in `rate()`. The three `_total` names this collector
    serves as counters are the opposite and **must** be `rate()`d, though they
    do not all reset for the same reason. `ecs_node_requests_total` and
    `ecs_node_request_bytes_total` come from `monitoring_main` and restart
    from zero when the datahead service restarts.
    `ecs_node_network_bytes_total` comes from `monitoring_op`/`net` and is an
    OS-level NIC byte counter, so it restarts when the node reboots rather
    than when a service does. None of that generalises to the `_total` suffix
    itself: [five of the eight `_total` names](index.md) this exporter
    publishes are gauges, and `rate()` over a gauge reads every decrease as a
    counter reset and invents a spike that never happened.

!!! note "Sole source for three names"
    When `collectFlux` is enabled it becomes the **sole source** of
    `ecs_node_cpu_utilization_percent`, `ecs_node_memory_utilization_percent`
    and `ecs_node_memory_used_bytes` — the [dashboard-sourced node
    collector](index.md#nodes-dashboard) stops emitting them so exactly one source
    owns each name (ADR-0006). Most of what else this collector emits uses a
    **name no other collector emits** (`ecs_node_network_bytes_total`,
    `ecs_node_requests_total`, `ecs_node_request_bytes_total`,
    `ecs_node_transaction_latency_milliseconds_bucket`/`_count`,
    `ecs_cluster_dt_*`, `ecs_cluster_requests_per_second`,
    `ecs_cluster_request_bytes_per_second`), so there is no shared name for
    it to collide on. An extra label on a shared name would *not* be safe —
    ADR-0006 requires one label-key set per name, and a second source adding
    a label the first does not carry is exactly the drift it forbids.

    The one exception is `ecs_node_dt_total`, which `collectDT` also emits.
    Unlike the three names above, ownership there is not exclusive to Flux: it
    is arbitrated by whether `collectDT` is on, not by `collectFlux` alone —
    see the "Cluster-wide totals, plus a per-node count Flux can serve after
    all" note below for the exact rule.

    Arbitration is unconditional on the flag, not on what the cluster
    actually still serves: enabling `collectFlux` against a cluster whose
    dashboard payload *does* still carry
    `ecs_node_cpu_utilization_percent`/`ecs_node_memory_*`, but whose Flux
    measurement names differ from the ones this collector queries, loses all
    three — Flux's empty result does not fall back to the dashboard's. That
    trade is deliberate: dynamic per-metric arbitration would make "who owns
    this name" a runtime fact instead of a fixed one, which is the invariant
    this note exists to protect.

!!! note "Two names, one measurement: cluster throughput"
    `ecs_cluster_request_bytes_per_second{op}` (Flux, `monitoring_vdc`) and the
    pre-existing `ecs_cluster_transaction_bandwidth_mb_per_second{op}`
    (dashboard API) measure the same thing — cluster-wide read/write
    throughput, same `op` dimension — from two different sources, in two
    different units (bytes/s vs. MB/s: compare them without converting and
    one will look broken). Both are exported whenever `collectFlux` is on;
    the arbitration above does not apply here, because arbitration stops two
    *sources* sharing one *name* — these are two different names describing
    one measurement, and nothing suppresses either. Prefer
    `ecs_cluster_transaction_bandwidth_mb_per_second`: it is the
    long-standing metric and the only one of the two available when
    `collectFlux` is off. Reach for the Flux-sourced one on a cluster whose
    dashboard payload omits the transaction fields — the gap this collector
    exists to cover. A disagreement between the two is not a bug to chase in
    the exporter; it means the dashboard and Flux sources have diverged on
    that cluster, which is worth investigating in its own right.

All per-node rows carry the `node` label, resolved from the Flux `host` tag
against the same `/vdc/nodes` inventory every other collector joins on. A row
whose `host` matches no inventory node emits no sample and increments
`ecs_collector_unmapped_nodes{collector="flux"}`.

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

**`monitoring_op` — per node, directory table**

| Measurement / field | Metric | Type | Note |
| --- | --- | --- | --- |
| `dtquery_dt_dist_host_dt_node_id` / `count_i` | `ecs_node_dt_total{node}` | gauge | joined on `dt_node_id` (the node's `data_ip`), not `host`; query skipped when `collectDT` is on |

!!! note "Cluster-wide totals, plus a per-node count Flux can serve after all"
    `dtquery_dt_status` is tagged `process, tag` only — no `host`, no
    `node_id` — so the three rows above are cluster totals, not a per-node
    breakdown; that much was always right. But
    `dtquery_dt_dist_host_dt_node_id` **does** carry a per-node breakdown of
    the same total, under `dt_node_id` rather than `host` — confirmed live
    2026-07-31, where the five per-node `count_i` values summed to exactly
    `dtquery_dt_status`'s cluster total. Flux emits it as
    `ecs_node_dt_total{node}`, the identical name and shape the [opt-in DT
    collector](index.md#node-dt-opt-in-collectdt-true) already serves — but
    only when `collectDT` is off. `collectDT` keeps the name, plus per-node
    `unready`/`unknown`, which Flux still has no per-node breakdown for,
    wherever it is reachable: it is the richer source, so it wins (ADR-0006).
    `collectFlux` and `collectDT` stay independent flags — either, both, or
    neither — and turning `collectFlux` on where `collectDT` cannot reach
    port 9101 is what newly makes `ecs_node_dt_total` available there at all.

**`monitoring_main` — per node, cumulative**

| Measurement / field | Metric | Type |
| --- | --- | --- |
| `statDataHead_performance_internal_transactions` / `succeed_request_counter` | `ecs_node_requests_total{node,outcome="success"}` | counter |
| `statDataHead_performance_internal_transactions` / `failed_request_counter` | `ecs_node_requests_total{node,outcome="failed"}` | counter |
| `statDataHead_performance_internal_throughput` / `total_read_requests_size` | `ecs_node_request_bytes_total{node,op="read"}` | counter |
| `statDataHead_performance_internal_throughput` / `total_write_requests_size` | `ecs_node_request_bytes_total{node,op="write"}` | counter |

**`monitoring_main` — per node, histogram**

| Measurement / field | Metric | Type | Note |
| --- | --- | --- | --- |
| `statDataHead_performance_internal_latency` (bucket-bound fields, `id=ttfb_read`) | `ecs_node_transaction_latency_milliseconds_bucket{node,op="read",le}` | counter | `le` is the field name verbatim — a valid Prometheus bound, including `+Inf` |
| `statDataHead_performance_internal_latency` (bucket-bound fields, `id=ttlb_write`) | `ecs_node_transaction_latency_milliseconds_bucket{node,op="write",le}` | counter | same |
| `statDataHead_performance_internal_latency` (`+Inf` bound) | `ecs_node_transaction_latency_milliseconds_count{node,op}` | counter | the `+Inf` bucket, restated as `_count` |

!!! note "No `_sum`, and the gauge it displaces"
    This measurement's field *names* are bucket bounds (`0.0`, `1.0`,
    `4.814963904455889`, … `59999.999999999985`, `+Inf`), not a fixed field
    set — cumulative counts, confirmed live 2026-07-31. The store serves no
    `_sum`, so `prometheus.MustNewConstHistogram` cannot be used; the buckets
    are published as ordinary counters carrying an `le` label, which is
    exactly what `histogram_quantile()` consumes. That gets you quantiles; it
    does not get you an average — there is no `_sum` to divide by `_count`,
    and none can be reconstructed from buckets alone.

    `ecs_node_transaction_latency_milliseconds` — the per-node latency
    **gauge** in [the reference](index.md#nodes-dashboard), sourced from the
    dashboard — is suppressed when `collectFlux` is on: `Nodes` stops
    emitting it, the same arbitration that already applies to CPU and memory
    (see "Sole source for three names" above). Unlike those three, Flux does
    not reproduce the gauge's shape — it replaces the name with the
    bucket/count family above, so a query for the plain gauge name goes empty
    on a `collectFlux` cluster rather than picking up a new source silently.

**`monitoring_vdc` — cluster-wide, already per-second**

| Measurement / field | Metric | Type | Note |
| --- | --- | --- | --- |
| `cq_performance_transaction` / `succeed_request_counter` | `ecs_cluster_requests_per_second{outcome="success"}` | gauge | confirmed in prose only — no payload attached |
| `cq_performance_transaction` / `failed_request_counter` | `ecs_cluster_requests_per_second{outcome="failed"}` | gauge | confirmed in prose only — no payload attached |
| `cq_performance_throughput` / `total_read_requests_size` | `ecs_cluster_request_bytes_per_second{op="read"}` | gauge | confirmed in prose only — no payload attached |
| `cq_performance_throughput` / `total_write_requests_size` | `ecs_cluster_request_bytes_per_second{op="write"}` | gauge | confirmed in prose only — no payload attached |

!!! note "What live-confirmed means here"
    Every measurement, field and tag across these tables is read directly
    from a real ObjectScale 4.3.0.0.142978 capture dated 2026-07-31 — except
    the two `cq_performance_*` rows just above, confirmed by the reporter in
    prose, with no payload attached. Nothing in these tables is read off the
    admin guide anymore.
