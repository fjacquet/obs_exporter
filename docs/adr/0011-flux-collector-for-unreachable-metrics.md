# Opt-in Flux collector for metrics the management API does not serve

## Status

Accepted as direction (2026-07-30) — implementation deferred, pending the design
questions below. Supersedes the "out of scope" position implied by
[ADR-0008](0008-swagger-4.2-validation-findings.md) and by the deferred ADR-0011
placeholder in the tolerant-HAL-decode plan.

## Context

Two families of metrics cannot be collected through the ObjectScale management
API (port 4443) that every current collector uses, and both were confirmed on a
live 4.3 cluster rather than inferred from documentation:

1. **Per-node performance and cluster transaction fields.** The reference
   documents `nodeCpuUtilization`, `nodeMemoryUtilization*`, the NIC bandwidth
   fields and the cluster-level `transaction*` fields, and `nodes.go` /
   `cluster.go` parse all of them. On a real 4.3 cluster the dashboard payloads
   simply do not carry them (ADR-0008, "two things worth recording"), and a
   second trace taken on 2026-07-30 against ObjectScale 4.3.0.0.142978 with
   v2.8.1 confirms it: all twelve per-node performance fields are absent from a
   40-key node instance, and the 98-key cluster payload contains no key beginning
   `transaction` at all. The cycle reported `ok=true` with 324 samples, so this
   is field absence, not a collection failure. The absent-never-zero rule means
   the exporter is silent about them rather than wrong, but silent is still
   uncovered. The reporter's search for another management endpoint serving these
   fields came up empty; the Flux API is the only source found.

2. **Directory-table stats on a network-segmented cluster.** The opt-in DT
   collector scrapes port 9101 on each node. On the segmentation Dell recommends
   for production — separate management, data, replication and private fabric
   networks, in place since ECS 3.8 — that port listens on a private link-local
   VLAN (169.254.0.0/16), reachable only from the node itself or across the
   fabric, never from a routed network. This was verified with `ss` and `curl` on
   a production-layout cluster: no listener on the management address at all. The
   2.8.2 fix moved the object-port ping to `data_ip`, which restored
   `ecs_node_active_connections`, but no address the exporter can reach answers on
   9101. An exporter running outside the cluster therefore cannot collect DT
   counts by scraping, on the topology most production deployments use.

Both are available from the cluster's own monitoring store: ObjectScale runs
InfluxDB, fed by Telegraf plugins, queryable through
`POST /flux/api/external/v2/query` with the `SYSTEM_MONITOR` role. Per the
reporter, on 4.3:

- bucket `monitoring_op`, measurement `cpu`, field `usage_user` — per-node CPU
- bucket `monitoring_op`, measurement `mem`, fields `used`, `used_percent`,
  `total` — per-node memory
- bucket `monitoring_op`, measurement `dtquery_dt_status` — DT total / unknown /
  unready, plus per-node distribution via `dtquery_dt_dist_host_dt_node_id`.
  Present since 3.8, so this is not a 4.x-only path.

A query without a host filter, closed with `|> last()`, returns the newest value
**per node** in one call — one request covers the whole cluster, which fits the
snapshot model's one-pass-per-cycle shape rather than forcing an N+1 per node.

The argument against was, and remains, coupling: InfluxDB is ObjectScale's
internal implementation detail, with no compatibility promise across releases,
and the exporter would grow a second protocol, a second auth surface and a
columnar response format to parse. Weighed against that, the Flux path is the
only known route to two metric families that the alternative leaves permanently
uncovered, and the DT case is not a nice-to-have: it is a collector that
currently cannot work as shipped on the recommended topology.

## Decision

Accept an **opt-in Flux collector** as the path for metrics the management API
does not serve. It is a `ResourceCollector` like any other (ADR-0009), off by
default, enabled per cluster by an explicit flag, and it must degrade to
`ecs_collector_up{collector="flux"}=0` without affecting any other domain when
InfluxDB is unreachable, unauthorized, or has drifted.

Constraints that follow from the existing ADRs and are not up for
re-litigation in the implementation:

- **One request per measurement per cycle, not per node.** Queries close with
  `|> last()` and no host filter (ADR-0002's snapshot model; the same reasoning
  that made billing a bulk POST).
- **Absent, never zero** (ADR-0007). A measurement missing from the bucket, an
  unparseable column, or an empty result yields no sample. A Flux-sourced metric
  must not be distinguishable from a management-API one by having fake zeros.
- **Metric names and label keys are unchanged by the source.** `ecs_node_*` and
  `ecs_cluster_*` names already reserved by `nodes.go` / `cluster.go` for these
  fields are the names the Flux collector emits, with the same label keys, so a
  cluster that serves them from the dashboard and one that serves them from Flux
  produce the same series (ADR-0006's label-key invariant). Exactly one source
  may emit a given name per cycle.
- **The `node` label carries the same identifier as every other collector.** Flux
  tags nodes by `host`/`node_id`; those must be mapped to the `/vdc/nodes`
  `nodename` the other collectors use, or the series will not join. On the 4.3
  trace, `/vdc/nodes` `nodename` and the dashboard's `displayName` are the same
  five strings, which is what makes `nodename` the cluster-wide join key.
- **Never log the InfluxDB response body at `--trace` without the auth-path
  skip** applied to the token exchange, per the family tracing rule.

## Open questions (to answer in the implementation PR)

1. **Auth and config surface.** Does the `SYSTEM_MONITOR` role reuse the existing
   cluster credentials and `X-SDS-AUTH-TOKEN`, or does the Flux endpoint need its
   own token/org/bucket settings? This decides whether the config grows one flag
   or a whole `flux:` block.
2. **Bucket/measurement → metric mapping**, written as a table in
   `docs/metrics.md` before the code, including which of the currently-silent
   `nodes.go` / `cluster.go` fields the Flux path takes over.
3. **Version skew.** `monitoring_op` measurement names are undocumented and
   unversioned. What happens on a cluster where a measurement was renamed — a
   warning and absent samples (the HAL-shape precedent), or a hard collector
   failure?
4. **Response parsing.** Flux returns annotated CSV or columnar JSON; which, and
   does it need its own tolerant-parsing file the way `points.go` was needed for
   the dashboard time series?
5. **Whether DT stats move to Flux entirely.** If Flux covers
   `dtquery_dt_status`, the node-local 9101 scrape has no remaining topology
   where it is the better option, and `collectDT` could be narrowed to the object
   port ping or deprecated outright.
6. **The "DT Query Services" REST section.** The reference lists it; nobody has
   found an endpoint under it serving these counters. If one exists on 4443 it
   beats Flux for the DT half of this ADR, and that half should be revisited
   before it is implemented.

## Consequences

- The exporter gains a second data protocol and a dependency on an ObjectScale
  internal, accepted deliberately and confined to one opt-in collector file. A
  future ObjectScale release that reorganises the buckets breaks that collector
  and nothing else.
- Operators who do not enable it see no change: no new requests, no new config
  required, no new failure mode.
- The DT reachability caveat stays documented in `docs/metrics.md` until the Flux
  collector ships. `collectDT` keeps working as-is for flat-network clusters and
  for node-local exporter deployments.
- Two ADR-0008 findings stop being permanent caveats and become a tracked gap
  with an agreed route.

## Related

- [Swagger 4.2 validation findings](0008-swagger-4.2-validation-findings.md) — the
  live-4.3 evidence that the performance fields are absent from the dashboard.
- [Modular resource collectors](0009-modular-resource-collectors.md) — the
  interface and per-collector degradation this collector plugs into.
- [Snapshot model](0002-prometheus-snapshot-model.md) — why queries must be
  one-per-cycle and cluster-wide.
- [Metric naming, units and label invariant](0006-metric-naming-units-and-label-invariant.md)
  — why the source may not change a metric's name or label keys.
- [ObjectScale 4.1 API alignment](0007-obs-4-1-api-alignment.md) — absent-never-zero
  and the opt-in DT decision this revisits.
