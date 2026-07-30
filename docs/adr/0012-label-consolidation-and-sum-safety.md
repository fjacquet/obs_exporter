# Label consolidation, and the sum-safety rule that bounds it

## Status

Accepted (2026-07-30). Breaking; ships in 3.0.0. Refines
[ADR-0006](0006-metric-naming-units-and-label-invariant.md), which fixed the
label-key invariant but said nothing about *when* a distinction belongs in the
name and when it belongs in a label.

## Context

The exporter grew one metric name per measured quantity, so a distinction that is
really one dimension of one measurement ended up encoded in the name:
`ecs_cluster_good_disks`, `_bad_disks`, `_maintenance_disks`,
`_ready_to_replace_disks` are four names for one measurement — a disk count —
split by state.

That has costs in ordinary use. A "disks by state" panel needs four queries
instead of one; `topk` across states is impossible; a new state added by ECS
means a new metric name, a dashboard edit and an alert edit rather than a new
series appearing on its own.

The exporter already uses the label form in several places —
`ecs_cluster_alerts_unacknowledged{severity}`,
`ecs_cluster_transaction_errors{code,protocol,category}`, the GC `{scope}` label,
and `ecs_cluster_disk_space_allocated_component_bytes{purpose}` sitting directly
beside `disk_space_*_bytes` in the name-split form. So the codebase disagreed
with itself, and the reporter who raised this was reading the same `/metrics`
output we were.

The counter-pressure is that collapsing names into labels can silently break
aggregation. If a metric family contains both a whole and its parts, `sum()` over
that family double-counts, and nothing in Prometheus warns about it. Three of the
proposed consolidations had exactly that shape, which we could check because a
4.3 cluster's payload was available:

| Proposed family | Identity measured on the live payload |
| --- | --- |
| `disk_space_bytes{type}` incl. `total` | `total = allocated + free + reserved`, delta 0 bytes |
| `gc_bytes_total{kind}` incl. `detected` | `detected = pending + unreclaimable + reclaimed`, delta 0 on both scopes |
| `ec_bytes{kind}` | `coded ⊂ applicable` — the API's own ratio is coded/applicable |

## Decision

**Consolidate a set of metric names into one name plus a label when, and only
when, the series are the same measurement in the same unit and of the same type
(gauge vs counter), differing only along one dimension — and no member of the
family is an aggregate of the others.**

The second clause is about double-counting, not about `sum()` being the right
operator. Whether summing a family means anything stays metric-specific:
`sum` over `ecs_cluster_disks{state}` is a disk count, while `sum` over
`ecs_cluster_transaction_latency_milliseconds{op}` is nonsense and `avg` is what
you want — that is a property of latency, not a reason to keep `read` and
`write` under separate names. What consolidation must never do is put a total
and its components under one name, because then even the *correct* operator
gives a wrong answer and nothing warns you.

Applied to this repo, that yields (full old→new table in
[Migrating to v3](../migration-v3.md)):

- `ecs_cluster_nodes{state}`, `ecs_cluster_disks{state}`, `ecs_node_disks{state}`
- `ecs_cluster_disk_space_bytes{type}`, `ecs_node_disk_space_bytes{type}`
- `ecs_cluster_transaction_latency_milliseconds{op}` and the bandwidth/TPS pairs,
  cluster and node
- `ecs_cluster_transactions_total{outcome}`
- `ecs_cluster_replication_traffic{direction}`,
  `ecs_replication_group_traffic{direction}`
- `ecs_cluster_gc_bytes{scope,state}`
- `ecs_node_nic_bandwidth{direction}`
- `ecs_replication_group_chunks_pending_bytes{type}`
- `ecs_node_scrape_up{endpoint}`, replacing `ecs_node_dt_up`. The DT collector
  scrapes two ports that a segmented network puts on different addresses, so one
  up-signal could not describe both: the object-port ping had no signal at all
  and its failure showed only as an absent series. One name plus `{endpoint}`
  makes each failure visible and leaves room for a third port.

And the **sum-safety rule** that bounds it: *a whole and its parts never share a
metric name.* Where a total exists, it keeps its own name:

- `ecs_cluster_nodes_installed`, `ecs_cluster_disks_installed`,
  `ecs_node_disks_installed` — renamed from the bare plural, which now means the
  per-state breakdown.
- `ecs_cluster_disk_space_total_bytes`, `ecs_node_disk_space_total_bytes`,
  `ecs_cluster_disk_space_offline_total_bytes` — unchanged.
- `ecs_cluster_gc_reclaimed_bytes_total` and `ecs_cluster_gc_detected_bytes_total`
  stay separate names, both because detected is the sum of the other three and
  because they are counters: a family must never mix a counter and a gauge, or
  `rate()` would be valid on only some of its series.
- `ecs_cluster_ec_applicable_bytes` / `_coded_bytes` are **not** consolidated,
  which is the one item from the original proposal we declined. They look like one
  measurement split by a dimension, but coded is a subset of applicable, so the
  family would double-count. Revisit only with a payload from a cluster mid-EC
  that proves otherwise.

### One key per dimension

A consolidation also has to pick a label *key*, and picking one per call site
produced three keys for one semantic: `disk_space_bytes{type}`,
`chunks_pending_bytes{kind}` and `disk_space_allocated_component_bytes{purpose}`
all name disjoint partitions of a byte quantity. Query-side that is the cost that
matters — an alert author asking "bytes by partition, any family" could not write
one `by (…)` clause, and every dashboard variable had to hardcode which family
used which key.

**`type` is this exporter's key for "which partition of the quantity".** All
three families carry it. The other keys in use name genuinely different
dimensions and stay as they are: `state` (what condition a thing is in),
`op` (read vs write), `direction` (ingress vs egress), `outcome`, `scope`,
`severity`, `endpoint`. A new consolidation picks from this list or extends it
deliberately, rather than inventing a synonym.

Metrics that differ in unit, type, or measured quantity stay separate names:
`ec_coded_ratio_percent` (%), `ec_rate` and `recovery_rate`, the
`*_complete_time_estimate` pair (time), `recovery_bad_chunks_bytes` (single
member), the booleans `gc_enabled` / `node_healthy`, and
`replication_group_zones`.

`ecs_node_health_state{state}` is left as it is: it is a state *indicator*
(constant 1 on the current state), not a count, so it does not merge with the
count families.

## Consequences

- **Breaking.** Roughly thirty metric names disappear or change meaning. Three
  are meaning changes rather than renames and deserve the loudest warning:
  `ecs_cluster_nodes`, `ecs_cluster_disks` and `ecs_node_disks` still exist but no
  longer mean the total — a dashboard querying them keeps working and starts
  showing something else. `docs/migration-v3.md` leads with those three.
- All six bundled Grafana dashboards were rewritten in the same change. A
  dashboard that lags the exporter shows empty panels, not wrong numbers, except
  for the three meaning changes above.
- `_installed` counts are deliberately **not** the sum of the state series: ECS
  documents five node health states and publishes counts for three, so
  `installed - sum(by_state)` is a legitimate "unaccounted for" signal.
- Adding a state, type or direction is now a new series, not a new metric name, a
  docs row and a dashboard edit.
- Quotas became their own collector in the same release, which is not a naming
  change but follows the same reasoning: an optional collection needs its own
  `ecs_collector_up` or its failure is indistinguishable from being switched off.
- The label-key invariant test (ADR-0006) covers the consolidated families
  unchanged: one name, one ordered label-key set, still enforced.

## Related

- [Metric naming, units and label invariant](0006-metric-naming-units-and-label-invariant.md)
  — the invariant this refines.
- [Migrating to v3](../migration-v3.md) — the full old→new table.
- [ObjectScale 4.1 API alignment](0007-obs-4-1-api-alignment.md) — absent-never-zero,
  which is why an unpopulated field yields no series in a consolidated family
  rather than a zero one.
