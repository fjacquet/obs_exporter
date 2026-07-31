# Metrics reference

New to Prometheus metrics, or unsure why one of these is missing?
[Reading the metrics](reading.md) covers gauges versus counters, why a metric
can be absent rather than zero, and how the collection interval relates to your
scrape interval.

Every domain metric carries the `cluster` identity label (one exporter process can
serve many clusters). Metrics are gauges holding the latest snapshot value by
default. Eight names end in `_total`, and three of them are the exception: the
Flux collector's `ecs_node_requests_total`, `ecs_node_request_bytes_total` and
`ecs_node_network_bytes_total` export as Prometheus `TYPE counter` (and as OTLP
observable counters) because the value is cumulative, and `rate()` is for those.
The other five — `ecs_cluster_transactions_total`,
`ecs_cluster_gc_reclaimed_bytes_total`, `ecs_cluster_gc_detected_bytes_total`,
`ecs_node_dt_total` and `ecs_cluster_dt_total` — emit as gauges, so `_total` is
a strong hint, not a guarantee; the [Flux collector
mapping tables](flux.md) spell out each
metric's type explicitly. Per-second values (TPS, bandwidth, and the metrics
named `…_rate` — `ecs_cluster_ec_rate`, `ecs_cluster_recovery_rate`) are
already rates — aggregate them with `sum`/`avg`, **never `rate()`**.

Sources: `/dashboard/zones/localzone` (cluster), `…/replicationgroups`
(replication), `…/nodes` (node), `/vdc/nodes` (info), `/object/namespaces` +
`/object/billing/namespace/info` (namespace).

## Exporter / meta

| Metric | Labels | Description |
| --- | --- | --- |
| `obs_exporter_build_info` | `version`, `goversion` | constant `1`, build identity |
| `ecs_up` | `cluster` | `1` when the last cycle produced domain samples for the cluster |
| `ecs_collector_up` | `cluster`, `collector` | per-collector success (`cluster`, `replication`, `nodes`, `info`, `metering`, `quotas`, `dt`, `flux`) |
| `ecs_collector_unmapped_nodes` | `cluster`, `collector` | Flux rows whose `host` tag joined no inventory node this cycle; always emitted, including `0`. Housekeeping only — excluded from the domain-sample count that drives `ecs_up` |

## Cluster (VDC-wide)

| Metric | Labels | Description |
| --- | --- | --- |
| `ecs_cluster_info` | `version` | constant `1`, ECS software version |
| `ecs_cluster_nodes` | `state` (`good`/`bad`/`maintenance`) | node count in each health state |
| `ecs_cluster_nodes_installed` | | nodes in the cluster, whatever their state |
| `ecs_cluster_disks` | `state` (`good`/`bad`/`maintenance`/`ready_to_replace`) | disk count in each state |
| `ecs_cluster_disks_installed` | | disks in the cluster, whatever their state |
| `ecs_cluster_alerts_unacknowledged` | `severity` (`critical`/`error`/`info`/`warning`) | unacknowledged alert counts |
| `ecs_cluster_disk_space_bytes` | `type` (`allocated`/`free`/`reserved`) | cluster capacity by what holds it |
| `ecs_cluster_disk_space_total_bytes` | | online capacity; equals the sum of the three types above |
| `ecs_cluster_disk_space_offline_total_bytes` | | capacity on offline disks; not part of the online total |
| `ecs_cluster_transaction_latency_milliseconds` | `op` (`read`/`write`) | transaction latency |
| `ecs_cluster_transaction_bandwidth_mb_per_second` | `op` | transaction bandwidth (MB/s, as reported by the dashboard API) |
| `ecs_cluster_transactions_per_second` | `op` | transactions per second |
| `ecs_cluster_transactions_total` | `outcome` (`error`/`success`) | cumulative transaction counts |
| `ecs_cluster_transaction_errors` | `code`, `protocol`, `category` | error counts split by HTTP code and protocol (e.g. `404`/`S3`) |
| `ecs_cluster_replication_traffic` | `direction` (`ingress`/`egress`) | cluster-level replication traffic (unit as reported by the dashboard API) |
| `ecs_cluster_replication_rpo_lag_seconds` | | VDC-wide RPO lag (seconds); zone-level counterpart of the per-group metric |
| `ecs_cluster_replication_rpo_timestamp_seconds` | | VDC-wide unix timestamp of the recovery point |

!!! warning "`_installed` is not the sum of the states"
    ECS documents five node health states and publishes a count for only three
    (`good`, `bad`, `maintenance`), so a node that is `suspect` or `notaccessible`
    appears in `ecs_cluster_nodes_installed` and in **no** `ecs_cluster_nodes`
    series. `ecs_cluster_nodes_installed - sum by (cluster) (ecs_cluster_nodes)`
    is therefore a useful "unaccounted-for nodes" alert, not a bug — keep the
    `by (cluster)`, since a bare `sum()` drops the label the subtraction matches
    on and yields an empty result. The same holds for disks.
    This is also why the totals are separate metric names: folding them in as
    another `state` would make `sum()` double-count.

## Cluster background processes

From the same local-zone dashboard response as the cluster metrics above — no
additional API call.

| Metric | Labels | Description |
| --- | --- | --- |
| `ecs_cluster_gc_bytes` | `scope` (`user`/`system`), `state` (`pending`/`unreclaimable`) | current GC backlog: `pending` is detected and reclaimable, `unreclaimable` is detected and not. The two are disjoint |
| `ecs_cluster_gc_reclaimed_bytes_total` | `scope` | space reclaimed since the cluster was built — a lifetime counter, not a backlog |
| `ecs_cluster_gc_detected_bytes_total` | `scope` | space detected by GC over the cluster's lifetime; equals pending + unreclaimable + reclaimed |
| `ecs_cluster_gc_enabled` | `scope` | `1` when that GC scope is enabled, `0` when explicitly disabled. The flag is assumed to scope the same subsystem as the byte series above — inferred from the API's field naming (`gcUserDataIsEnabled` / `gcSystemMetadataIsEnabled`), not documented by Dell |
| `ecs_cluster_recovery_bad_chunks_bytes` | | corrupted chunk data still awaiting recovery. The cluster may report a long-stale computation here: on a real 4.3 cluster this field's timestamp was 55 days older than every other field in the same response |
| `ecs_cluster_recovery_rate` | | recovery throughput (unit as reported by the dashboard API). Already a rate — never wrap in `rate()` |
| `ecs_cluster_recovery_complete_time_estimate` | | estimated time to finish recovery (unit as reported by the dashboard API) |
| `ecs_cluster_ec_applicable_bytes` | | sealed data eligible for erasure coding. Kept a separate name from the one below on purpose: coded is a *subset* of applicable, so one `ec_bytes{type}` family would count the coded bytes twice under `sum()` |
| `ecs_cluster_ec_coded_bytes` | | sealed data already erasure-coded |
| `ecs_cluster_ec_coded_ratio_percent` | | coded share of applicable data |
| `ecs_cluster_ec_rate` | | erasure-coding throughput (unit as reported by the dashboard API). Already a rate — never wrap in `rate()` |
| `ecs_cluster_ec_complete_time_estimate` | | estimated time to finish coding (unit as reported by the dashboard API) |
| `ecs_cluster_disk_space_allocated_component_bytes` | `type` | allocated space broken down by what holds it |

`type` is one of `user_data`, `system_metadata`, `geo_cache`, `geo_copy`,
`local_protection`.

!!! warning "The allocation breakdown is not exhaustive"
    `ecs_cluster_disk_space_allocated_component_bytes` does **not** sum to
    `ecs_cluster_disk_space_allocated_bytes`. On a real ObjectScale 4.3 cluster the
    five components accounted for 87.2% of the allocated total. Do not compute
    percentages of the total from these components, and do not treat the remainder
    as a category — it is simply unreported.

!!! note "There is no combined scope"
    The API also reports combined GC figures, which equal `user + system` exactly
    (verified to the byte on a live cluster). Exporting them would make
    `sum(ecs_cluster_gc_bytes)` double-count, so they are omitted:
    `sum without(scope) (ecs_cluster_gc_bytes)` reproduces them. The same rule is
    why `detected` is not a `state` of `ecs_cluster_gc_bytes`: it is the sum of
    pending, unreclaimable and reclaimed.

## Replication groups

| Metric | Labels | Description |
| --- | --- | --- |
| `ecs_replication_group_traffic` | `rg`, `direction` (`ingress`/`egress`) | per-group replication traffic |
| `ecs_replication_group_chunks_pending_bytes` | `rg`, `type` (`repo`/`journal`/`xor`) | backlog awaiting replication (`repo`, `journal`) or XOR (`xor`). The three pools are disjoint, so `sum without(type)` is the total backlog |
| `ecs_replication_group_rpo_timestamp_seconds` | `rg` | unix timestamp of the recovery point |
| `ecs_replication_group_rpo_lag_seconds` | `rg` | RPO lag (new in OBS 4.1) |
| `ecs_replication_group_zones` | `rg` | zone count of the group |

## Nodes (dashboard)

All with the `node` label (the node's display name).

!!! note "Three of these names can come from a different source"
    `ecs_node_cpu_utilization_percent`, `ecs_node_memory_utilization_percent`
    and `ecs_node_memory_used_bytes` are emitted here only when
    `collectFlux` is off. See the [Flux collector](flux.md)
    page's "Sole source for three names" note for the arbitration rule and
    its trade-off.

| Metric | Description |
| --- | --- |
| `ecs_node_healthy` | `1` when `healthStatus` is `Good` |
| `ecs_node_health_state` (extra label `state`) | `1` for the node's current `healthStatus`; `state` is one of `good` / `suspect` / `bad` / `notaccessible` / `maintenance` (the five values the API documents), keeping e.g. bad vs maintenance distinguishable |
| `ecs_node_disks` (extra label `state`) | per-node disk count in each state (`good` / `bad` / `maintenance` / `ready_to_replace`) |
| `ecs_node_disks_installed` | disks on the node, whatever their state |
| `ecs_node_disk_space_bytes` (extra label `type`) | per-node capacity by `allocated` / `free` |
| `ecs_node_disk_space_total_bytes` | per-node capacity total |
| `ecs_node_cpu_utilization_percent` | CPU usage |
| `ecs_node_memory_utilization_percent` / `ecs_node_memory_used_bytes` | memory usage |
| `ecs_node_nic_bandwidth` (extra label `direction`: `received` / `transmitted`) | NIC throughput (unit as reported by the dashboard API) |
| `ecs_node_nic_utilization_percent` | NIC utilization |
| `ecs_node_transaction_latency_milliseconds` (extra label `op`: `read` / `write`) | per-node latency |
| `ecs_node_transaction_bandwidth_mb_per_second` (extra label `op`) | per-node bandwidth |
| `ecs_node_transactions_per_second` (extra label `op`) | per-node TPS |

!!! warning "Per-node capacity does not add up, by design"
    The per-node payload publishes no reserved series, so
    `sum by (cluster, node) (ecs_node_disk_space_bytes)` falls short of
    `ecs_node_disk_space_total_bytes` by the node's reserve — 10% of the total on
    a live 4.3 cluster. Keep the `by (cluster, node)`: a bare `sum()` collapses
    every node into one figure and no longer lines up with the per-node total. Do
    not read the difference as free space; `type="free"` is the free space.

!!! note "Availability varies by cluster and version"
    `ecs_node_cpu_utilization_percent`, `ecs_node_memory_*`, `ecs_node_nic_*` and
    the cluster-level `ecs_cluster_transaction*` metrics come from dashboard fields
    documented by the API reference that some clusters do not populate — their
    absence was confirmed at raw-API level on ObjectScale 4.3, where the keys are
    simply not present in the response. Missing fields yield absent series, never
    zeros, so these metrics may not appear at all on your cluster.

## Namespaces (`collectMetering: true`)

Usage comes from the `metering` collector (one bulk billing POST, whatever the
namespace count). Quota limits come from a separate `quotas` collector
(`collectQuotas`, default on), because there is no bulk quota endpoint and it
therefore costs one GET per namespace per cycle. They are separate collectors so
that `ecs_collector_up{collector="quotas"}` tells you when quota reads are
failing — otherwise a cluster whose quota reads are all denied would look
exactly like one with `collectQuotas: false`.


All with the `namespace` label.

| Metric | Description |
| --- | --- |
| `ecs_namespace_quota_hard_bytes` | hard (block) quota; absent when unset. ECS stores quota in GiB; exported as bytes |
| `ecs_namespace_quota_soft_bytes` | soft (notification) quota; absent when unset |
| `ecs_namespace_used_bytes` | total namespace usage (from bulk billing) |
| `ecs_namespace_objects` | object count |
| `ecs_namespace_mpu_used_bytes` / `ecs_namespace_mpu_parts` | incomplete multipart-upload usage |

## Node DT (opt-in, `collectDT: true`)

Legacy scraping of undocumented node-local endpoints, labeled by `node` — the
inventory's `nodename`, so these series join with the [node metrics](#nodes-dashboard) on
`{node=…}`. The two ports are scraped at **different addresses**, taken from
`/vdc/nodes`: the DT stats port at `mgmt_ip`, the object port at `data_ip`
(falling back to `mgmt_ip` when the inventory publishes no data address).

!!! warning "Reachability on a network-segmented cluster"
    Dell's recommended production layout separates management, data, replication
    and a private fabric. On it the DT stats port (9101) listens on a private
    link-local VLAN (169.254.0.0/16), reachable only from the node itself or
    across the fabric — never from a routed network. An exporter running outside
    the cluster therefore gets `ecs_node_scrape_up{endpoint="dt"}=0` and no DT
    counts, while `endpoint="object"` stays at 1 and
    `ecs_node_active_connections` keeps working over the data network — which is
    exactly why the two endpoints report separately. Covering
    those counts from outside the cluster needs a different source; see
    [ADR-0011](../adr/0011-flux-collector-for-unreachable-metrics.md).

The ping payload's items are matched by `Name`, because the API documents
`PingList` as `0-*` `PingItem` elements with no guaranteed order.

| Metric | Description |
| --- | --- |
| `ecs_node_scrape_up` (extra label `endpoint`: `dt` / `object`) | reachability of each node-local port, reported separately because they sit on different networks |
| `ecs_node_dt_total` / `_unready` / `_unknown` | directory-table counts (port 9101, `mgmt_ip`) |
| `ecs_node_active_connections` | active Jetty connections on the node, from the object-port ping's `LOAD_FACTOR` item (port 9021, `data_ip`) |
| `ecs_node_maintenance_mode` | 1 when the node reports `MAINTENANCE_MODE` `ON`, 0 when `OFF`. Absent when the node reports `UNKNOWN` — the exporter does not guess a node out of maintenance. |

## Flux collector (opt-in, `collectFlux: true`)

Per-node performance metrics the dashboard payload omits on some clusters, and
cluster-wide DT and transaction counters, read from the cluster's own Flux
monitoring store. See [the Flux collector](flux.md) for the full mapping
tables and arbitration rules.
