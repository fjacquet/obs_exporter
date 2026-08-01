# Custom labels: global keys, per-cluster values

## Status

Accepted (2026-08-01)

## Context

One exporter process serves many clusters, so Prometheus sees a single scrape
target and target relabeling cannot attach a label that varies per cluster:
every series from the process would get the same value. Only the exporter knows
which cluster a sample came from. Operators asked for site, environment and
ownership labels that differ per cluster.

ADR-0006 makes the ordered label-key set part of a metric's schema, so any
label mechanism has to keep that key set uniform across clusters.

## Decision

- A top-level `labels:` block declares the label **keys** with default values.
- A cluster's `labels:` block may override a declared key's **value**; an
  undeclared key is a config-load error.
- Values are validated non-empty and support `${ENV_VAR}` interpolation; keys
  match `[a-zA-Z_][a-zA-Z0-9_]*` and may not start with `__`.
- Labels are stamped onto **every** sample, `ecs_up` and `ecs_collector_up`
  included, by `Sample.WithIdentity` in the collection loop — the same choke
  point that already stamps the `cluster` identity.
- Order is `cluster`, then the custom labels sorted by key, then the collector's
  own labels.
- A custom key colliding with a collector's own dimension is dropped for that
  metric family and logged once per key per cluster. The collector's dimension
  wins.
- OTLP carries them as data-point attributes, not resource attributes.

## Consequences

- The key set stays uniform and no value is ever empty, both by construction, so
  the ADR-0006 invariant holds without a completion pass.
- Operators cannot add a cluster-specific key; that is the price of the
  invariant.
- A colliding key silently loses that metric family's labelling, mitigated only
  by the log line — the collision is uniform per metric name, so it never
  produces mixed series schemas.
- Grafana dashboards cannot hardcode operator-defined keys; they ship an ad hoc
  filters variable instead.
