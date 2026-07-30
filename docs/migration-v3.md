# Migrating to v3

v3.0.0 consolidates metric families that were one measurement split across
several names into one name plus a label. The reasoning, and the sum-safety rule
that decides which families were left alone, is in
[ADR-0012](adr/0012-label-consolidation-and-sum-safety.md).

Nothing else changes: same endpoints, same config file, same `cluster` label,
same absent-never-zero behaviour. The bundled Grafana dashboards are updated in
the same release — if you run them from this repo, pull them together with the
binary.

## Read this first: two metrics changed meaning

These two names still exist and still return data. A query against them keeps
working and starts answering a different question, so a search-and-replace for
removed names will not find them.

| Metric | v2 meaning | v3 meaning | v2 total is now |
| --- | --- | --- | --- |
| `ecs_cluster_nodes` | total nodes | node count **per state** (3 series) | `ecs_cluster_nodes_installed` |
| `ecs_cluster_disks` | total disks | disk count **per state** (4 series) | `ecs_cluster_disks_installed` |
| `ecs_node_disks` | total disks on the node | per-node disk count **per state** | `ecs_node_disks_installed` |

If you alerted on `ecs_cluster_disks < N`, that alert now compares against a
per-state series and will fire wrongly. Point it at `_installed`.

## Full rename table

### Cluster

| v2 | v3 |
| --- | --- |
| `ecs_cluster_good_nodes` | `ecs_cluster_nodes{state="good"}` |
| `ecs_cluster_bad_nodes` | `ecs_cluster_nodes{state="bad"}` |
| `ecs_cluster_maintenance_nodes` | `ecs_cluster_nodes{state="maintenance"}` |
| `ecs_cluster_nodes` | `ecs_cluster_nodes_installed` |
| `ecs_cluster_good_disks` | `ecs_cluster_disks{state="good"}` |
| `ecs_cluster_bad_disks` | `ecs_cluster_disks{state="bad"}` |
| `ecs_cluster_maintenance_disks` | `ecs_cluster_disks{state="maintenance"}` |
| `ecs_cluster_ready_to_replace_disks` | `ecs_cluster_disks{state="ready_to_replace"}` |
| `ecs_cluster_disks` | `ecs_cluster_disks_installed` |
| `ecs_cluster_disk_space_allocated_bytes` | `ecs_cluster_disk_space_bytes{type="allocated"}` |
| `ecs_cluster_disk_space_free_bytes` | `ecs_cluster_disk_space_bytes{type="free"}` |
| `ecs_cluster_disk_space_reserved_bytes` | `ecs_cluster_disk_space_bytes{type="reserved"}` |
| `ecs_cluster_transaction_read_latency_milliseconds` | `ecs_cluster_transaction_latency_milliseconds{op="read"}` |
| `ecs_cluster_transaction_write_latency_milliseconds` | `ecs_cluster_transaction_latency_milliseconds{op="write"}` |
| `ecs_cluster_transaction_read_bandwidth_mb_per_second` | `ecs_cluster_transaction_bandwidth_mb_per_second{op="read"}` |
| `ecs_cluster_transaction_write_bandwidth_mb_per_second` | `ecs_cluster_transaction_bandwidth_mb_per_second{op="write"}` |
| `ecs_cluster_transactions_read_per_second` | `ecs_cluster_transactions_per_second{op="read"}` |
| `ecs_cluster_transactions_write_per_second` | `ecs_cluster_transactions_per_second{op="write"}` |
| `ecs_cluster_transaction_errors_total` | `ecs_cluster_transactions_total{outcome="error"}` |
| `ecs_cluster_transaction_successes_total` | `ecs_cluster_transactions_total{outcome="success"}` |
| `ecs_cluster_replication_ingress_traffic` | `ecs_cluster_replication_traffic{direction="ingress"}` |
| `ecs_cluster_replication_egress_traffic` | `ecs_cluster_replication_traffic{direction="egress"}` |
| `ecs_cluster_gc_pending_bytes` | `ecs_cluster_gc_bytes{state="pending"}` |
| `ecs_cluster_gc_unreclaimable_bytes` | `ecs_cluster_gc_bytes{state="unreclaimable"}` |

`ecs_cluster_disk_space_total_bytes`, `_offline_total_bytes`,
`ecs_cluster_gc_reclaimed_bytes_total`, `_detected_bytes_total`,
`ecs_cluster_gc_enabled`, `ecs_cluster_transaction_errors{code,protocol,category}`,
the `ec_*` and `recovery_*` families, and everything under `ecs_namespace_*` are
**unchanged**.

### Nodes

| v2 | v3 |
| --- | --- |
| `ecs_node_good_disks` | `ecs_node_disks{state="good"}` |
| `ecs_node_bad_disks` | `ecs_node_disks{state="bad"}` |
| `ecs_node_maintenance_disks` | `ecs_node_disks{state="maintenance"}` |
| `ecs_node_ready_to_replace_disks` | `ecs_node_disks{state="ready_to_replace"}` |
| `ecs_node_disks` | `ecs_node_disks_installed` |
| `ecs_node_disk_space_allocated_bytes` | `ecs_node_disk_space_bytes{type="allocated"}` |
| `ecs_node_disk_space_free_bytes` | `ecs_node_disk_space_bytes{type="free"}` |
| `ecs_node_nic_received_bandwidth` | `ecs_node_nic_bandwidth{direction="received"}` |
| `ecs_node_nic_transmitted_bandwidth` | `ecs_node_nic_bandwidth{direction="transmitted"}` |
| `ecs_node_transaction_read_latency_milliseconds` | `ecs_node_transaction_latency_milliseconds{op="read"}` |
| `ecs_node_transaction_write_latency_milliseconds` | `ecs_node_transaction_latency_milliseconds{op="write"}` |
| `ecs_node_transaction_read_bandwidth_mb_per_second` | `ecs_node_transaction_bandwidth_mb_per_second{op="read"}` |
| `ecs_node_transaction_write_bandwidth_mb_per_second` | `ecs_node_transaction_bandwidth_mb_per_second{op="write"}` |
| `ecs_node_transactions_read_per_second` | `ecs_node_transactions_per_second{op="read"}` |
| `ecs_node_transactions_write_per_second` | `ecs_node_transactions_per_second{op="write"}` |

`ecs_node_healthy`, `ecs_node_health_state{state}`,
`ecs_node_disk_space_total_bytes`, `ecs_node_cpu_utilization_percent`,
`ecs_node_memory_*`, `ecs_node_nic_utilization_percent` and the opt-in
`ecs_node_dt_*` family are **unchanged** — but see the v3 changelog for the DT
collector's `node` label, which changes value from an IP to the node name.

### Replication groups

| v2 | v3 |
| --- | --- |
| `ecs_replication_group_ingress_traffic` | `ecs_replication_group_traffic{direction="ingress"}` |
| `ecs_replication_group_egress_traffic` | `ecs_replication_group_traffic{direction="egress"}` |
| `ecs_replication_group_chunks_repo_pending_replication_bytes` | `ecs_replication_group_chunks_pending_bytes{kind="repo"}` |
| `ecs_replication_group_chunks_journal_pending_replication_bytes` | `ecs_replication_group_chunks_pending_bytes{kind="journal"}` |
| `ecs_replication_group_chunks_pending_xor_bytes` | `ecs_replication_group_chunks_pending_bytes{kind="xor"}` |

`ecs_replication_group_rpo_*` and `_zones` are **unchanged**.

## Rewriting queries

Most rewrites are mechanical, but two patterns need care.

**Adding the selector to an existing one.** The new label goes inside the
existing braces:

```promql
# v2
ecs_cluster_disk_space_free_bytes{cluster=~"$cluster"}
# v3
ecs_cluster_disk_space_bytes{type="free",cluster=~"$cluster"}
```

**Panels that plotted several names can collapse into one query.** Four queries
for four disk states become one, with the state in the legend:

```promql
# v3 — one query, one series per state
ecs_cluster_disks{cluster=~"$cluster"}
# legendFormat: {{state}} {{cluster}}
```

The bundled dashboards keep one query per series for now, so the migration is a
pure rename there; collapsing panels is an improvement you can make afterwards.

## What you can now do that you could not

```promql
# worst-off state per cluster, previously four separate queries
topk(1, ecs_cluster_disks{cluster=~"$cluster"})

# every non-good disk, including states ECS may add later
sum by (cluster) (ecs_cluster_disks{state!="good"})

# nodes ECS counts but does not classify (suspect / notaccessible have no count field)
ecs_cluster_nodes_installed - sum by (cluster) (ecs_cluster_nodes)

# total replication backlog per group
sum without (kind) (ecs_replication_group_chunks_pending_bytes)
```

## Sanity check after upgrading

Run one cycle and diff the emitted names against this page:

```bash
obs_exporter --config /etc/obs_exporter/config.yaml --once --debug | sort
```

Every name in the v3 column should appear (subject to what your cluster
populates — see the availability notes in [Metrics](metrics.md)); no name in the
v2 column should.
