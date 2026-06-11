# Metric naming, units, and the label-key invariant

## Status

Accepted (2026-06-10)

## Context

v1 metric names (`emcecs_*`) were unit-ambiguous (`space_free` in bytes, quota "in
KB", latency unitless) and the cluster identity lived in the Prometheus `instance`
label. The family standard (pstore ADR-0005/0006, ppdd ADR-0006) prescribes
unit-explicit names, an identity label, and a per-name label-key invariant.

## Decision

- Prefix **`ecs_`**, pattern `ecs_<object>_<metric>[_<unit>]`.
- **Unit-explicit names where the unit is known**: `_bytes`, `_milliseconds`,
  `_mb_per_second`, `_per_second`, `_percent`, `_seconds`. Where the dashboard API
  does not document a unit (NIC bandwidth, replication traffic), the name stays
  neutral and the metrics reference says "as reported by the dashboard API" —
  honest beats guessed.
- **Per-second values are gauges** — they are already rates; aggregate with
  `sum`/`avg`, never `rate()`.
- Unit conversions happen at the edge: quota GiB→bytes, billing KB→bytes.
- **Identity label**: every domain sample carries `cluster`, stamped centrally by
  the collection loop (`Sample.WithCluster`), so one process serves many clusters.
- **Label-key invariant**: a metric name carries exactly one ordered label-key set
  across all its series. Enforced twice: a test
  (`TestLabelKeyConsistency`) fails the build on drift, and the unchecked
  Prometheus collector drops nonconforming samples at scrape time as a backstop.
- Enumerable classes use label dimensions (alerts by `severity`, transaction
  errors by `code`/`protocol`/`category`) instead of name proliferation.

## Consequences

- Breaking rename for every v1 user — the full table lives in the migration guide.
- Dashboards can rely on stable series schemas per metric name.
