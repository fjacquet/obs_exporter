# Migrating from v1 (prometheus-emcecs-exporter)

v2.0.0 is a **full compatibility break**: new architecture, new metric names, new
scrape configuration. It targets the ObjectScale 4.1 management API (the surface is
backward compatible with ECS 3.x dashboards).

## Scrape model

v1 was an SNMP-exporter-style multi-target proxy: Prometheus passed the cluster as
`/query?target=…` and the exporter scraped it on demand.

v2 polls clusters from its **config file** on a background interval and serves the
latest snapshot at `/metrics`. Replace the v1 relabeling block with a plain scrape:

```yaml
scrape_configs:
  - job_name: ecs
    static_configs:
      - targets: ["obs-exporter.example.net:9438"]
```

The cluster identity moved from the Prometheus `instance` label to a `cluster`
label on every series. The separate `metering=1` scrape job is gone — metering runs
in the same cycle (disable per cluster with `collectMetering: false`).

## Metric rename table

| v1 | v2 |
|---|---|
| `emcecs_collection_success` | `ecs_up{cluster}` |
| `emcecs_collector_build_info` | `obs_exporter_build_info` |
| `emcecs_request_errors_total` | removed — use `ecs_collector_up{collector}` |
| `emcecs_cluster_version` | `ecs_cluster_info{version}` |
| `emcecs_cluster_alerts_critical` / `_error` / `_info` / `_warning` | `ecs_cluster_alerts_unacknowledged{severity="…"}` |
| `emcecs_cluster_good_nodes` / `_bad_nodes` | `ecs_cluster_good_nodes` / `ecs_cluster_bad_nodes` (+ new `_maintenance_nodes`, `_nodes`) |
| `emcecs_cluster_good_disks` / `_bad_disks` | `ecs_cluster_good_disks` / `ecs_cluster_bad_disks` (+ new maintenance / ready-to-replace) |
| `emcecs_cluster_space_total` / `_space_free` | `ecs_cluster_disk_space_total_bytes` / `_free_bytes` (+ new `_allocated_bytes`) |
| `emcecs_cluster_transaction_read_latency` / `_write_latency` | `ecs_cluster_transaction_read_latency_milliseconds` / `_write_latency_milliseconds` |
| `emcecs_cluster_transaction_read_bandwidth` / `_write_bandwidth` | `ecs_cluster_transaction_read_bandwidth_mb_per_second` / `_write_…` |
| `emcecs_cluster_transaction_read_per_second` / `_write_per_second` | `ecs_cluster_transactions_read_per_second` / `_write_per_second` |
| `emcecs_cluster_transaction_error` | `ecs_cluster_transaction_errors_total` |
| `emcecs_cluster_transaction_error_detail{errorcode,errorproto,category}` | `ecs_cluster_transaction_errors{code,protocol,category}` |
| `emcecs_cluster_transaction_success` | `ecs_cluster_transaction_successes_total` |
| `emcecs_cluster_replication_ingress_traffic` / `_egress_traffic` | `ecs_cluster_replication_ingress_traffic` / `_egress_traffic`, plus per-group `ecs_replication_group_…{rg}` |
| `emcecs_cluster_data_replication_pending` | `ecs_replication_group_chunks_repo_pending_replication_bytes{rg}` |
| `emcecs_cluster_journal_replication_pending` | `ecs_replication_group_chunks_journal_pending_replication_bytes{rg}` |
| `emcecs_cluster_chunks_pending_xor` | `ecs_replication_group_chunks_pending_xor_bytes{rg}` |
| `emcecs_cluster_last_replication_timestamp` | `ecs_replication_group_rpo_timestamp_seconds{rg}` |
| `emcecs_metering_namespacequota{ecsnamespace,type}` | `ecs_namespace_quota_hard_bytes{namespace}` / `_soft_bytes` (bytes, not KB) |
| `emcecs_metering_namespace_object_count{ecsnamespace}` | `ecs_namespace_objects{namespace}` (+ new `ecs_namespace_used_bytes`, MPU stats) |
| `emcecs_node_dtTotal` / `dtUnready` / `dtUnknown` | `ecs_node_dt_total` / `_unready` / `_unknown` — **opt-in** via `collectDT: true` |
| `emcecs_node_activeConnections` | `ecs_node_active_connections` — opt-in via `collectDT: true` |

New in v2 with no v1 equivalent: the whole per-node dashboard family
(`ecs_node_cpu_utilization_percent`, memory, NIC, per-node capacity and transaction
stats — from the documented `/dashboard/zones/localzone/nodes` endpoint),
`ecs_replication_group_rpo_lag_seconds`, `ecs_replication_group_zones`, and the
optional OTLP push path.

## Configuration

Flags/env vars (`-username`, `ECSENV_*`) are replaced by the YAML file with
`${ENV_VAR}` / `passwordFile` secrets — see
[Configuration](getting-started/configuration.md). The default port stays `9438`.
