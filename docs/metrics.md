# Metrics reference

Every domain metric carries the `cluster` identity label (one exporter process can
serve many clusters). All metrics are exported as gauges holding the latest
snapshot value; per-second values (TPS, bandwidth) are already rates — aggregate
them with `sum`/`avg`, **never `rate()`**.

Sources: `/dashboard/zones/localzone` (cluster), `…/replicationgroups`
(replication), `…/nodes` (node), `/vdc/nodes` (info), `/object/namespaces` +
`/object/billing/namespace/info` (namespace).

## Exporter / meta

| Metric | Labels | Description |
|---|---|---|
| `obs_exporter_build_info` | `version`, `goversion` | constant `1`, build identity |
| `ecs_up` | `cluster` | `1` when the last cycle produced domain samples for the cluster |
| `ecs_collector_up` | `cluster`, `collector` | per-collector success (`cluster`, `replication`, `nodes`, `info`, `metering`, `dt`) |

## Cluster (VDC-wide)

| Metric | Labels | Description |
|---|---|---|
| `ecs_cluster_info` | `version` | constant `1`, ECS software version |
| `ecs_cluster_nodes` / `_good_nodes` / `_bad_nodes` / `_maintenance_nodes` | | node counts |
| `ecs_cluster_disks` / `_good_disks` / `_bad_disks` / `_maintenance_disks` / `_ready_to_replace_disks` | | disk counts |
| `ecs_cluster_alerts_unacknowledged` | `severity` (`critical`/`error`/`info`/`warning`) | unacknowledged alert counts |
| `ecs_cluster_disk_space_total_bytes` / `_free_bytes` / `_allocated_bytes` | | cluster capacity |
| `ecs_cluster_transaction_read_latency_milliseconds` / `_write_…` | | transaction latency |
| `ecs_cluster_transaction_read_bandwidth_mb_per_second` / `_write_…` | | transaction bandwidth (MB/s, as reported by the dashboard API) |
| `ecs_cluster_transactions_read_per_second` / `_write_…` | | transactions per second |
| `ecs_cluster_transaction_errors_total` / `_successes_total` | | cumulative error/success counts |
| `ecs_cluster_transaction_errors` | `code`, `protocol`, `category` | error counts split by HTTP code and protocol (e.g. `404`/`S3`) |
| `ecs_cluster_replication_ingress_traffic` / `_egress_traffic` | | cluster-level replication traffic (unit as reported by the dashboard API) |

## Replication groups

| Metric | Labels | Description |
|---|---|---|
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
|---|---|
| `ecs_node_healthy` | `1` when `healthStatus` is `Good` |
| `ecs_node_disks` / `_good_disks` / `_bad_disks` / `_maintenance_disks` / `_ready_to_replace_disks` | per-node disk counts |
| `ecs_node_disk_space_total_bytes` / `_free_bytes` / `_allocated_bytes` | per-node capacity |
| `ecs_node_cpu_utilization_percent` | CPU usage |
| `ecs_node_memory_utilization_percent` / `ecs_node_memory_used_bytes` | memory usage |
| `ecs_node_nic_received_bandwidth` / `_transmitted_bandwidth` / `_utilization_percent` | NIC stats (bandwidth unit as reported by the dashboard API) |
| `ecs_node_transaction_read_latency_milliseconds` / `_write_…` | per-node latency |
| `ecs_node_transaction_read_bandwidth_mb_per_second` / `_write_…` | per-node bandwidth |
| `ecs_node_transactions_read_per_second` / `_write_…` | per-node TPS |

## Namespaces (metering, `collectMetering: true`)

All with the `namespace` label.

| Metric | Description |
|---|---|
| `ecs_namespace_quota_hard_bytes` | hard (block) quota; absent when unset. ECS stores quota in GiB; exported as bytes |
| `ecs_namespace_quota_soft_bytes` | soft (notification) quota; absent when unset |
| `ecs_namespace_used_bytes` | total namespace usage (from bulk billing) |
| `ecs_namespace_objects` | object count |
| `ecs_namespace_mpu_used_bytes` / `ecs_namespace_mpu_parts` | incomplete multipart-upload usage |

## Node DT (opt-in, `collectDT: true`)

Legacy scraping of undocumented node-local endpoints (ports 9101/9021), labeled by
`node` (management IP).

| Metric | Description |
|---|---|
| `ecs_node_dt_up` | node-local scrape success |
| `ecs_node_dt_total` / `_unready` / `_unknown` | directory-table counts |
| `ecs_node_active_connections` | active connections (object-port ping) |
