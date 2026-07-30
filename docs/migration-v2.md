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

The right-hand column gives the **current** (v3) names, so a v1 user upgrading
today has one hop to make rather than two. If you are coming from v2 instead, use
[Migrating to v3](migration-v3.md), which lists what changed between v2 and v3.

| v1 | current (v3) |
| --- | --- |
| `emcecs_collection_success` | `ecs_up{cluster}` |
| `emcecs_collector_build_info` | `obs_exporter_build_info` |
| `emcecs_request_errors_total` | removed, no equivalent — nothing counts request errors cumulatively. `ecs_collector_up{cluster,collector}` is a current-state health signal, not a counter |
| `emcecs_cluster_version` | `ecs_cluster_info{version}` |
| `emcecs_cluster_alerts_critical` / `_error` / `_info` / `_warning` | `ecs_cluster_alerts_unacknowledged{severity="…"}` |
| `emcecs_cluster_good_nodes` / `_bad_nodes` | `ecs_cluster_nodes{state="good"}` / `{state="bad"}` (+ `maintenance`, and the total `ecs_cluster_nodes_installed`) |
| `emcecs_cluster_good_disks` / `_bad_disks` | `ecs_cluster_disks{state="good"}` / `{state="bad"}` (+ `maintenance`, `ready_to_replace`, and the total `ecs_cluster_disks_installed`) |
| `emcecs_cluster_space_total` / `_space_free` | `ecs_cluster_disk_space_total_bytes` / `ecs_cluster_disk_space_bytes{type="free"}` (+ `allocated`, `reserved`) |
| `emcecs_cluster_transaction_read_latency` / `_write_latency` | `ecs_cluster_transaction_latency_milliseconds{op="read"}` / `{op="write"}` |
| `emcecs_cluster_transaction_read_bandwidth` / `_write_bandwidth` | `ecs_cluster_transaction_bandwidth_mb_per_second{op="read"}` / `{op="write"}` |
| `emcecs_cluster_transaction_read_per_second` / `_write_per_second` | `ecs_cluster_transactions_per_second{op="read"}` / `{op="write"}` |
| `emcecs_cluster_transaction_error` | `ecs_cluster_transactions_total{outcome="error"}` |
| `emcecs_cluster_transaction_error_detail{errorcode,errorproto,category}` | `ecs_cluster_transaction_errors{code,protocol,category}` |
| `emcecs_cluster_transaction_success` | `ecs_cluster_transactions_total{outcome="success"}` |
| `emcecs_cluster_replication_ingress_traffic` / `_egress_traffic` | `ecs_cluster_replication_traffic{direction="ingress"}` / `{direction="egress"}`, plus per-group `ecs_replication_group_traffic{rg,direction}` |
| `emcecs_cluster_data_replication_pending` | `sum without(rg) (ecs_replication_group_chunks_pending_bytes{kind="repo"})` — the v3 series is per group, so the sum is what reproduces the v1 cluster-level figure |
| `emcecs_cluster_journal_replication_pending` | `sum without(rg) (ecs_replication_group_chunks_pending_bytes{kind="journal"})` |
| `emcecs_cluster_chunks_pending_xor` | `sum without(rg) (ecs_replication_group_chunks_pending_bytes{kind="xor"})` |
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
