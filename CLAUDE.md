# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Prometheus + OTLP exporter for Dell EMC ECS / ObjectScale clusters, targeting the
ObjectScale 4.1.0.0 management REST API. Module `github.com/fjacquet/obs_exporter`,
Go 1.26.4. Part of Fred's exporter family — the `exporter-standards` skill is the
canonical reference for stack/CI/architecture rules; ADRs in `docs/adr/` record
this repo's instances of those decisions.

## Commands

```bash
make ci             # CI gate: fmt-check, vet, golangci-lint, go test -race, govulncheck, build
make sure           # quicker local gate (fmt-check, vet, test, build)
make test           # go test ./...
go test ./internal/ecs -run TestClusterCollect   # single test
make tools          # install pinned golangci-lint, cyclonedx-gomod, govulncheck
make cli            # build bin/obs_exporter (ldflags inject main.version)
make release-snapshot  # GoReleaser dry-run into dist/
make demo           # Compose stack: mockecs → exporter → Prometheus → Grafana (:3000)
```

Docs build (matches `docs.yml`): `uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict`.

## Architecture

Snapshot model (ADR-0002): `main.go` starts the HTTP server **before** the first
collection cycle, then a background loop polls every configured cluster each
`collection.interval` and pointer-swaps an immutable `Snapshot` into a
`SnapshotStore`. Both export paths only read snapshots:

- `internal/ecs/prometheus.go` — *unchecked* Prometheus collector (Describe sends
  nothing; label-key schema enforced per name at scrape time).
- `internal/ecs/otlp.go` — observable gauges + periodic gRPC reader; instruments
  are registered lazily after each cycle via `Collector.PostCycle`.

`internal/ecs/` holds one file per resource collector (`cluster.go`,
`replication.go`, `nodes.go`, `info.go`, `metering.go`, `dt.go`), each owning its
endpoint path and response structs (`ResourceCollector` interface, wired in
`resource.go` per cluster-level feature flags). Collectors emit cluster-agnostic
`Sample`s; the loop stamps the `cluster` identity label (`Sample.WithCluster`).

`internal/ecsclient/` is the hand-rolled resty/v2 management client (ADR-0003:
goobjectscale rejected — covers none of our endpoints). Auth: basic-auth `GET
/login` → cached `X-SDS-AUTH-TOKEN`, re-login once on 401, logout on `Close()`
(ECS caps tokens per user — never leak sessions). Transport retry excludes 4xx
(ADR-0004).

`internal/config/` — YAML with `${ENV_VAR}`/`passwordFile` secrets; `watcher.go`
reloads on SIGHUP + fsnotify (watches the parent dir, not the file inode). On
reload `main.go`'s `collectorRunner` rebuilds clients/loop and swaps; the
`SnapshotStore` is never replaced (ADR-0005).

`cmd/mockecs/` — fake ECS API serving embedded fixtures over self-signed TLS for
the Compose demo; demo-only, never published.

## Load-bearing constraints

- **ECS payload weirdness** (ADR-0007): dashboard stats are time-series arrays
  `[{"t":…, "<Space|Bytes|Percent|…>": …}]` with string-typed numbers and
  inconsistent value keys. Always parse via `Series`/`Num` in
  `internal/ecs/points.go`; "current" = newest point by `t`. Unparseable values
  must yield *absent* samples, never zeros.
- **Label-key invariant** (ADR-0006): one metric name = one ordered label-key set
  across all series. `TestLabelKeyConsistency` fails on drift; keep it passing.
- **Naming**: `ecs_<object>_<metric>[_<unit>]`, unit-explicit where the API
  documents a unit; per-second values are gauges (never `rate()` them). Update
  `docs/metrics.md` AND the Grafana dashboard when adding/renaming metrics.
- **Metering is batched**: namespace usage comes from one bulk
  `POST /object/billing/namespace/info` per cycle — don't reintroduce per-namespace
  billing GETs.
- No inline `nosemgrep`/`//nolint` suppressions — restructure instead (semgrep
  blocks on findings).
- GitHub Actions stay SHA-pinned with explicit `# vX.Y.Z` comments (dependabot
  bumps both).

## Testing

Fixtures in `internal/ecs/testdata/` mirror the OBS 4.1 reference examples
(`cmd/mockecs/fixtures/` are copies — keep in sync). Collector tests run against
`ecsclient.Mock`; the client is tested against an httptest TLS server; export
paths are asserted via **both** the Prometheus registry gather and the OTLP
`ManualReader`.

## Known repo state

- Default branch is still `master`; the family standard is `main`. Workflows
  trigger on both until the rename happens.
