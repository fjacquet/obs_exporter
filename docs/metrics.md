# Metrics reference

Every domain metric carries the `cluster` identity label (one exporter process can
serve many clusters). All metrics are exported as gauges holding the latest
snapshot value; per-second values (TPS, bandwidth, and the metrics named
`…_rate` — `ecs_cluster_ec_rate`, `ecs_cluster_recovery_rate`) are already rates
— aggregate them with `sum`/`avg`, **never `rate()`**. Metrics suffixed `_total`
are the cumulative counters, and those are the ones `rate()` is for.

Sources: `/dashboard/zones/localzone` (cluster), `…/replicationgroups`
(replication), `…/nodes` (node), `/vdc/nodes` (info), `/object/namespaces` +
`/object/billing/namespace/info` (namespace).

## Exporter / meta

| Metric | Labels | Description |
| --- | --- | --- |
| `obs_exporter_build_info` | `version`, `goversion` | constant `1`, build identity |
| `ecs_up` | `cluster` | `1` when the last cycle produced domain samples for the cluster |
| `ecs_collector_up` | `cluster`, `collector` | per-collector success (`cluster`, `replication`, `nodes`, `info`, `metering`, `dt`) |

## Cluster (VDC-wide)

| Metric | Labels | Description |
| --- | --- | --- |
| `ecs_cluster_info` | `version` | constant `1`, ECS software version |
| `ecs_cluster_nodes` / `_good_nodes` / `_bad_nodes` / `_maintenance_nodes` | | node counts |
| `ecs_cluster_disks` / `_good_disks` / `_bad_disks` / `_maintenance_disks` / `_ready_to_replace_disks` | | disk counts |
| `ecs_cluster_alerts_unacknowledged` | `severity` (`critical`/`error`/`info`/`warning`) | unacknowledged alert counts |
| `ecs_cluster_disk_space_total_bytes` / `_free_bytes` / `_allocated_bytes` / `_reserved_bytes` / `_offline_total_bytes` | | cluster capacity |
| `ecs_cluster_transaction_read_latency_milliseconds` / `_write_…` | | transaction latency |
| `ecs_cluster_transaction_read_bandwidth_mb_per_second` / `_write_…` | | transaction bandwidth (MB/s, as reported by the dashboard API) |
| `ecs_cluster_transactions_read_per_second` / `_write_…` | | transactions per second |
| `ecs_cluster_transaction_errors_total` / `_successes_total` | | cumulative error/success counts |
| `ecs_cluster_transaction_errors` | `code`, `protocol`, `category` | error counts split by HTTP code and protocol (e.g. `404`/`S3`) |
| `ecs_cluster_replication_ingress_traffic` / `_egress_traffic` | | cluster-level replication traffic (unit as reported by the dashboard API) |
| `ecs_cluster_replication_rpo_lag_seconds` | | VDC-wide RPO lag (seconds); zone-level counterpart of the per-group metric |
| `ecs_cluster_replication_rpo_timestamp_seconds` | | VDC-wide unix timestamp of the recovery point |

## Cluster background processes

From the same local-zone dashboard response as the cluster metrics above — no
additional API call.

| Metric | Labels | Description |
| --- | --- | --- |
| `ecs_cluster_gc_pending_bytes` | `scope` (`user`/`system`) | space detected as reclaimable but not yet reclaimed |
| `ecs_cluster_gc_reclaimed_bytes_total` | `scope` | space reclaimed since the cluster was built — a lifetime counter, not a backlog |
| `ecs_cluster_gc_unreclaimable_bytes` | `scope` | space detected but not reclaimable |
| `ecs_cluster_gc_detected_bytes_total` | `scope` | space detected by GC over the cluster's lifetime; equals pending + unreclaimable + reclaimed |
| `ecs_cluster_gc_enabled` | `scope` | `1` when that GC scope is enabled, `0` when explicitly disabled. The flag is assumed to scope the same subsystem as the byte series above — inferred from the API's field naming (`gcUserDataIsEnabled` / `gcSystemMetadataIsEnabled`), not documented by Dell |
| `ecs_cluster_recovery_bad_chunks_bytes` | | corrupted chunk data still awaiting recovery. The cluster may report a long-stale computation here: on a real 4.3 cluster this field's timestamp was 55 days older than every other field in the same response |
| `ecs_cluster_recovery_rate` | | recovery throughput (unit as reported by the dashboard API). Already a rate — never wrap in `rate()` |
| `ecs_cluster_recovery_complete_time_estimate` | | estimated time to finish recovery (unit as reported by the dashboard API) |
| `ecs_cluster_ec_applicable_bytes` | | sealed data eligible for erasure coding |
| `ecs_cluster_ec_coded_bytes` | | sealed data already erasure-coded |
| `ecs_cluster_ec_coded_ratio_percent` | | coded share of applicable data |
| `ecs_cluster_ec_rate` | | erasure-coding throughput (unit as reported by the dashboard API). Already a rate — never wrap in `rate()` |
| `ecs_cluster_ec_complete_time_estimate` | | estimated time to finish coding (unit as reported by the dashboard API) |
| `ecs_cluster_disk_space_allocated_component_bytes` | `purpose` | allocated space broken down by what holds it |

`purpose` is one of `user_data`, `system_metadata`, `geo_cache`, `geo_copy`,
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
    `sum(ecs_cluster_gc_pending_bytes)` double-count, so they are omitted:
    `sum without(scope) (ecs_cluster_gc_pending_bytes)` reproduces them.

## Replication groups

| Metric | Labels | Description |
| --- | --- | --- |
| `ecs_replication_group_ingress_traffic` / `_egress_traffic` | `rg` | per-group replication traffic |
| `ecs_replication_group_chunks_repo_pending_replication_bytes` | `rg` | repo data awaiting replication |
| `ecs_replication_group_chunks_journal_pending_replication_bytes` | `rg` | journal data awaiting replication |
| `ecs_replication_group_chunks_pending_xor_bytes` | `rg` | data pending XOR |
| `ecs_replication_group_rpo_timestamp_seconds` | `rg` | unix timestamp of the recovery point |
| `ecs_replication_group_rpo_lag_seconds` | `rg` | RPO lag (new in OBS 4.1) |
| `ecs_replication_group_zones` | `rg` | zone count of the group |

## Nodes (dashboard)

All with the `node` label (the node's display name).

| Metric | Description |
| --- | --- |
| `ecs_node_healthy` | `1` when `healthStatus` is `Good` |
| `ecs_node_health_state` (extra label `state`) | `1` for the node's current `healthStatus`; `state` is one of `good` / `suspect` / `bad` / `notaccessible` / `maintenance` (the five values the API documents), keeping e.g. bad vs maintenance distinguishable |
| `ecs_node_disks` / `_good_disks` / `_bad_disks` / `_maintenance_disks` / `_ready_to_replace_disks` | per-node disk counts |
| `ecs_node_disk_space_total_bytes` / `_free_bytes` / `_allocated_bytes` | per-node capacity |
| `ecs_node_cpu_utilization_percent` | CPU usage |
| `ecs_node_memory_utilization_percent` / `ecs_node_memory_used_bytes` | memory usage |
| `ecs_node_nic_received_bandwidth` / `_transmitted_bandwidth` / `_utilization_percent` | NIC stats (bandwidth unit as reported by the dashboard API) |
| `ecs_node_transaction_read_latency_milliseconds` / `_write_…` | per-node latency |
| `ecs_node_transaction_read_bandwidth_mb_per_second` / `_write_…` | per-node bandwidth |
| `ecs_node_transactions_read_per_second` / `_write_…` | per-node TPS |

!!! note "Availability varies by cluster and version"
    `ecs_node_cpu_utilization_percent`, `ecs_node_memory_*`, `ecs_node_nic_*` and
    the cluster-level `ecs_cluster_transaction*` metrics come from dashboard fields
    documented by the API reference that some clusters do not populate — their
    absence was confirmed at raw-API level on ObjectScale 4.3, where the keys are
    simply not present in the response. Missing fields yield absent series, never
    zeros, so these metrics may not appear at all on your cluster.

## Namespaces (metering, `collectMetering: true`)

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
    the cluster therefore gets `ecs_node_dt_up=0` and no DT counts, while
    `ecs_node_active_connections` still works over the data network. Covering
    those counts from outside the cluster needs a different source; see
    [ADR-0011](adr/0011-flux-collector-for-unreachable-metrics.md).

| Metric | Description |
| --- | --- |
| `ecs_node_dt_up` | node-local DT stats scrape success (port 9101, `mgmt_ip`) |
| `ecs_node_dt_total` / `_unready` / `_unknown` | directory-table counts |
| `ecs_node_active_connections` | active connections (object-port ping, port 9021, `data_ip`) |
