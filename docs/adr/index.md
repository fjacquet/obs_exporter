# Architecture Decision Records

| ADR | Decision |
|---|---|
| [0001](0001-ci-supply-chain-hardening.md) | CI/CD supply-chain hardening: SHA-pinned actions, GoReleaser, SBOM, Semgrep |
| [0002](0002-prometheus-snapshot-model.md) | Background snapshot collection model with dual Prometheus + OTLP export |
| [0003](0003-hand-rolled-client-over-goobjectscale.md) | Hand-rolled resty/v2 client instead of the goobjectscale SDK |
| [0004](0004-token-auth-retry-policy.md) | X-SDS-AUTH-TOKEN session auth, re-login on 401, retry excludes 4xx |
| [0005](0005-config-hot-reload-rebuild-and-swap.md) | Config hot reload via SIGHUP + file watch, rebuild-and-swap |
| [0006](0006-metric-naming-units-and-label-invariant.md) | `ecs_` prefix, unit-explicit names, `cluster` identity label, label-key invariant |
| [0007](0007-obs-4-1-api-alignment.md) | ObjectScale 4.1 API alignment: bulk billing, dashboard nodes, opt-in DT |
