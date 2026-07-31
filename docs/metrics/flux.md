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

!!! warning "Rate direction is not the same in every bucket"
    The `monitoring_vdc`-sourced metrics (`ecs_cluster_requests_per_second`,
    `ecs_cluster_request_bytes_per_second`) are already rates and must
    **never** be wrapped in `rate()`. `ecs_node_network_bytes_total`,
    `ecs_node_requests_total` and `ecs_node_request_bytes_total` are the
    opposite: counters that reset on datahead restart, and — like any other
    `_total` metric — **must** be `rate()`d.

!!! note "Sole source for three names"
    When `collectFlux` is enabled it becomes the **sole source** of
    `ecs_node_cpu_utilization_percent`, `ecs_node_memory_utilization_percent`
    and `ecs_node_memory_used_bytes` — the [dashboard-sourced node
    collector](index.md#nodes-dashboard) stops emitting them so exactly one source
    owns each name (ADR-0006). Every other metric this collector emits uses a
    **name no other collector emits** (`ecs_node_network_bytes_total`,
    `ecs_node_requests_total`, `ecs_node_request_bytes_total`,
    `ecs_cluster_dt_*`, `ecs_cluster_requests_per_second`,
    `ecs_cluster_request_bytes_per_second`), so there is no shared name for
    it to collide on. An extra label on a shared name would *not* be safe —
    ADR-0006 requires one label-key set per name, and a second source adding
    a label the first does not carry is exactly the drift it forbids.

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

!!! note "Cluster-wide, and not a replacement for `collectDT`"
    `dtquery_dt_status` is tagged `process, tag` only — no `host`, no
    `node_id` — so these three are cluster totals, not a per-node breakdown.
    The per-node `ecs_node_dt_total` / `_unready` / `_unknown` from the
    [opt-in DT collector](index.md#node-dt-opt-in-collectdt-true) are unaffected and
    stay the only source of per-node DT counts. `collectFlux` and
    `collectDT` are independent flags — either, both, or neither.

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
