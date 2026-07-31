# Design decisions

An architecture decision record (ADR) is a short document that captures one
decision with lasting consequences: what was decided, why, and which
alternatives were rejected and why. Nothing here gets deleted or rewritten
when a later decision changes course — a superseded ADR stays published, with
a note pointing at whatever replaced it. The point is that a decision can be
revisited on its merits years later, instead of being reconstructed from a
commit message or rediscovered the hard way in production.

Most of these records exist for whoever maintains this exporter's code.
Three of them explain behaviour that would otherwise look like a bug to
whoever runs it — start here if that is you:

- **[ADR-0007 — ObjectScale 4.1 API alignment](0007-obs-4-1-api-alignment.md)**:
  why a metric is missing from a scrape instead of reading zero, whenever a
  cluster does not report that field. If you expected a value and see
  nothing, this is why — and why "absent" should not be read as "the
  exporter is broken."
- **[ADR-0004 — Token auth & retry policy](0004-token-auth-retry-policy.md)**:
  why the exporter logs out of every cluster it talks to on shutdown.
  ObjectScale caps how many session tokens a single account can hold at
  once, and an exporter that leaked sessions instead of releasing them would
  eventually lock that monitoring account out.
- **[ADR-0011 — Opt-in Flux collector for metrics the management API does not
  serve](0011-flux-collector-for-unreachable-metrics.md)**: why the Flux
  collector exists. ObjectScale 4.3 dashboard payloads omit per-node
  performance fields the API reference documents, and the per-node
  directory-table stats come from each node's port 9101, which the segmented
  network layout Dell recommends for production does not expose to an outside
  exporter. The Flux collector itself needs no firewall change: it uses the
  same management port and the same session as every other collector. It
  stays opt-in because it reads ObjectScale's internal InfluxDB monitoring
  store, an implementation detail with no compatibility promise across
  releases.

The full set, in order:

| ADR | Decision |
| --- | --- |
| [0001](0001-ci-supply-chain-hardening.md) | CI/CD supply-chain hardening: SHA-pinned actions, GoReleaser, SBOM, Semgrep |
| [0002](0002-prometheus-snapshot-model.md) | Background snapshot collection model with dual Prometheus + OTLP export |
| [0003](0003-hand-rolled-client-over-goobjectscale.md) | Hand-rolled resty/v2 client instead of the goobjectscale SDK |
| [0004](0004-token-auth-retry-policy.md) | X-SDS-AUTH-TOKEN session auth, re-login on 401, retry excludes 4xx |
| [0005](0005-config-hot-reload-rebuild-and-swap.md) | Config hot reload via SIGHUP + file watch, rebuild-and-swap |
| [0006](0006-metric-naming-units-and-label-invariant.md) | `ecs_` prefix, unit-explicit names, `cluster` identity label, label-key invariant |
| [0007](0007-obs-4-1-api-alignment.md) | ObjectScale 4.1 API alignment: bulk billing, dashboard nodes, opt-in DT |
| [0008](0008-swagger-4.2-validation-findings.md) | Swagger 4.2 validation findings: billing body, `/vdc/nodes`, content-type — all three verified live on 4.3 and closed, no code change needed |
| [0009](0009-modular-resource-collectors.md) | Modular `ResourceCollector` interface: one file per metric domain, per-cluster feature-flag wiring, per-collector degradation |
| [0010](0010-mockecs-demo-harness.md) | mockecs fake-API demo harness (demo-only, never published) and the duplicated-fixtures sync constraint |
| [0011](0011-flux-collector-for-unreachable-metrics.md) | Opt-in Flux collector accepted as direction for the performance fields the dashboard omits and the DT stats a segmented network hides; shipped, opt-in, in 3.2.0 |
| [0012](0012-label-consolidation-and-sum-safety.md) | Consolidate one-measurement-many-names families into one name plus a label, bounded by the sum-safety rule: a whole and its parts never share a metric name |
