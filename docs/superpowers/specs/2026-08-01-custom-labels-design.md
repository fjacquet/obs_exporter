# Custom labels: global keys, per-cluster values — design

**Date:** 2026-08-01
**Status:** approved, not implemented
**Origin:** Benjamin (`DisasteR`), proposal 2 of the mail sent 2026-07-31.

## Problem

A single obs_exporter process serves many clusters, so Prometheus sees one scrape
target. Target relabeling therefore cannot attach a per-cluster label: every
series from the process would get the same value. Only the exporter knows which
cluster a sample came from, so only the exporter can attach labels that vary per
cluster.

Global labels (`env=prod` on everything) are the weaker half — an operator can
already get those from target relabeling. Per-cluster values are the load-bearing
part of the proposal.

## Configuration

A top-level `labels:` block declares the key set together with default values. A
cluster may override the **value** of a declared key; it may never introduce a
new key.

```yaml
labels:
  env: prod
  site: geneva
  owner: ${TEAM_NAME}

clusters:
  - name: obs-prod-01
    host: obs1.example.com
    labels:
      site: zurich
```

Types:

- `Config.Labels map[string]string` (`yaml:"labels"`)
- `Cluster.Labels map[string]string` (`yaml:"labels"`)

### Validation

Performed in `config.Load`, after `${ENV}` interpolation, before returning:

1. Every global key matches `^[a-zA-Z_][a-zA-Z0-9_]*$`. Keys starting with `__`
   are rejected — Prometheus reserves that prefix.
2. Every global value is non-empty after interpolation.
3. A cluster label key absent from the global block is an error:
   `cluster %q: unknown label key %q (declare it in the top-level labels block)`.
4. Every cluster value is non-empty after interpolation.
5. A cluster carrying `labels:` when no global block exists hits rule 3.

Label values go through the same `interpolate` helper as `host`, `username` and
`password`, so `owner: ${TEAM_NAME}` works and an unset variable fails at load
rather than at scrape.

### Resolution

`func (c Config) EffectiveLabels(cl Cluster) []Label` returns the global map with
the cluster's overrides applied, **sorted by key**. `config.Label` is a local
`{Key, Value string}` type; `config` does not import `ecs`. `main.go`'s
`buildTargets` converts to `[]ecs.Label`.

Sorting is not cosmetic: ADR-0006 makes the ordered key set part of a metric's
schema, so the order must not depend on YAML authoring order or Go map iteration
order.

### Why keys are declared globally

This is the only model where "no value is ever empty" holds by construction. A
union-of-keys model would either emit empty label values for clusters that did
not set a key — noise in dashboards, misleading `group by` results — or force
every cluster to repeat every key. Declaring the key set once keeps the key set
uniform across clusters, which is exactly what ADR-0006 requires.

### Hot reload

No dedicated code. The config watcher (SIGHUP + fsnotify) already reloads the
file and `main.go`'s `collectorRunner` rebuilds clients and targets, so new
labels take effect from the next collection cycle. The previous snapshot keeps
the old labels until then — the same latency as changing `collectDT`, consistent
with ADR-0005.

## Injection

One injection point. `ecs.Target` gains `Labels []Label`, pre-sorted by
`buildTargets`. `Sample.WithCluster` is generalised:

```go
// WithIdentity returns a copy with a leading {cluster=name} label followed by
// the configured custom labels. A custom label whose key the sample already
// carries is skipped: the collector's own dimension wins.
func (s Sample) WithIdentity(name string, extra []Label) Sample
```

`WithCluster` remains as `WithIdentity(name, nil)`, so existing collectors and
tests are untouched.

Resulting order: `cluster`, then the custom labels sorted by key, then the
collector's own labels. All three call sites in `collector.go` (lines 97, 99 and
123) pass `target.Labels`, so `ecs_collector_up` and `ecs_up` carry the custom
labels too. Applying them to every sample without exception is deliberate: an
`env` label missing from `ecs_up` would break joins and `group by (env)` in
dashboards.

### Collisions

A custom key that matches a dimension the collector already emits (`cluster`,
`collector`, `namespace`, `node`, `severity`, `code`, `protocol`, …) is skipped
for that sample; the collector's dimension wins.

This is schema-safe rather than a degradation: ADR-0006 guarantees one key set
per metric name, so the skip is uniform across every series of that name. The
invariant holds, and the only consequence is that the custom label is absent from
that metric family.

To keep the skip from being silent, `collectCluster` logs `WARN` once per
colliding key per cluster:

```
custom label %q collides with a collector dimension on metric %q; dropped for that metric family
```

Deduplication uses a `map[string]struct{}` on `Collector` guarded by a mutex —
`collectAll` runs clusters concurrently through an `errgroup`.

## Export paths

Neither path changes. `PromCollector.Collect` reads `s.Labels`; the OTLP exporter
converts the same slice through `attrsFor`. Custom labels are data-point
attributes on the OTLP side, not resource attributes: resource attributes are
per-process, so a per-cluster override could not live there, and splitting one
key across two levels depending on whether a cluster overrode it would leave
backends merging the two planes inconsistently.

`PromCollector`'s existing label-key-drift drop stays as the ADR-0006 backstop.
Because collisions are uniform per metric name, they never trigger it.

Cost: one extra allocation per sample in `WithIdentity` when `extra` is
non-empty, on a path that already copies the label slice.

## Grafana

Dashboard JSON cannot hardcode operator-defined keys. Each of the seven
`grafana/dashboards/obs-*.json` gains an **ad hoc filters** variable
(`"type": "adhoc"`, Prometheus datasource) beside the existing `cluster`
variable. Grafana appends the operator's chosen matchers to every panel query and
autocompletes keys and values from the datasource, so nothing is hardcoded and no
panel query changes. `node-exporter-full.json` is imported upstream and is left
alone.

Two limits belong in the documentation rather than being hidden:

- Ad hoc filters apply to panel queries, not to variable queries: the `cluster`
  picker still lists every cluster even when filtering on `env=prod`. Confirm the
  behaviour against the Grafana version targeted at implementation time — it has
  changed across releases.
- They filter; they do not group. `sum by (env)` stays the user's job.

## Testing

- `config_test.go`: invalid key, `__` prefix, empty value, undeclared cluster key,
  cluster labels with no global block, `${ENV}` interpolation in a value, and
  sorted order stable regardless of YAML order.
- `sample_test.go`: `WithIdentity` ordering (`cluster` → sorted custom →
  collector), collision skip, `extra == nil` identical to `WithCluster`, `Type`
  preserved.
- `collector_test.go`: a `Target` with labels yields them on `ecs_up` and
  `ecs_collector_up`; `TestLabelKeyConsistency` passes with custom labels active.
- `prometheus_test.go` and `otlp_test.go`: both export paths expose the same
  custom keys, per the repo rule that export behaviour is asserted through both.
- One test asserting the collision `WARN` fires once per key across two cycles.

## Documentation

Derived from `grep -rl collectDT`, keeping the files that describe configuration:

- `README.md`
- `config.yaml` — commented example
- `docs/getting-started/configuration.md` — dedicated section: the
  global-keys/per-cluster-values model, the collision rule, and why Prometheus
  relabeling cannot do this
- `docs/getting-started/first-run.md`
- `docs/deployment/kubernetes.md`
- `charts/obs-exporter/values.yaml`
- `docs/metrics/reading.md` — custom labels appear on every series
- `CHANGELOG.md`

Not `docs/metrics/index.md`: no new metric.

The chart passes the exporter config through as a free-form string rendered into
a Secret (`values.yaml:90`), so chart support is commented example lines inside
that `config` block — no template plumbing.

**ADR-0014, "Custom labels: global keys, per-cluster values"** records the
decision. It amends the ADR-0006 label-key invariant and belongs in the same
place.

## Out of scope

- Per-collector or per-metric label sets.
- OTLP resource attributes.
- Relabeling or dropping existing labels.
- The `/livez` and `/readyz` probes — Benjamin's other proposal, specified in
  `2026-07-31-probe-endpoints-design.md`.
