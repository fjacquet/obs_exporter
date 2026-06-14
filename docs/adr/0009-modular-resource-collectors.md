# Modular resource collectors

## Status

Accepted (2026-06-14)

## Context

The exporter reads several unrelated metric domains from each ECS cluster —
cluster/zone dashboard, replication groups, nodes, version info, namespace
metering, and the opt-in DT/connection internals. Each domain has its own
endpoint path(s), its own response shape (ADR-0007's time-series weirdness is not
uniform across them), and its own availability story: metering is gated on a
per-cluster flag, DT is off by default, and any one domain can fail (bad
endpoint, permissions, 4.1-vs-4.2 drift) without the others being affected.

Folding all of this into one monolithic collect function would mean an API change
in one domain risks the whole cycle, feature flags become scattered conditionals,
and a single failing endpoint can zero out unrelated metrics. The family standard
prescribes a modular collector decomposition (ppdd ADR-0002).

## Decision

Define a `ResourceCollector` interface (`internal/ecs/resource.go`) — `Name()`
plus `Collect(ctx, client) ([]Sample, error)` — and implement it once per metric
domain, one file each (`cluster.go`, `replication.go`, `nodes.go`, `info.go`,
`metering.go`, `dt.go`). Each implementation **owns its endpoint path and JSON
structs**, so an API change is localized to one file and one fixture set.

- Collectors emit **cluster-agnostic** `Sample`s. The collection loop stamps the
  `{cluster=…}` identity label via `Sample.WithCluster` (ADR-0006), so a collector
  never needs to know which cluster it runs against.
- `Registry(cluster)` returns the ordered collector set for one cluster, honoring
  its **per-cluster feature flags** — `Metering{}` only when metering is enabled,
  `NewDT(cl)` only when `collectDT` is set. Feature gating lives in this one place,
  not sprinkled through the collectors.
- The loop (`internal/ecs/collector.go`) runs each collector independently and
  emits `ecs_collector_up{collector=…}` per domain. A failing collector logs,
  sets its own `up=0`, and is skipped — other domains still publish. The cluster
  is marked down (`ecs_up=0`) only when **all** collectors fail or the cycle
  yields **zero** domain samples; partial failure degrades gracefully.

## Consequences

- An endpoint change, a new 4.x payload quirk, or a fixture update touches exactly
  one collector file and its testdata — blast radius is one domain.
- Per-domain observability: operators see precisely which domain is failing via
  `ecs_collector_up`, distinct from a whole-cluster `ecs_up=0`.
- Adding a metric domain = one new file implementing the interface + one line in
  `Registry`; optional domains cost one feature-flag branch.
- Collectors are unit-tested in isolation against `ecsclient.Mock`; the registry's
  flag wiring is tested separately.

## Related

- [ObjectScale 4.1 API alignment](0007-obs-4-1-api-alignment.md) — the
  per-domain payload shapes each collector owns.
- [Metric naming, units and label invariant](0006-metric-naming-units-and-label-invariant.md)
  — the `cluster` identity label the loop stamps onto each collector's samples.
- ppdd ADR-0002 — the family-canonical modular-collector decision.
