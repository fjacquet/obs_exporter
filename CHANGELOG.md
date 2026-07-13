# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [2.6.0] - 2026-07-14

### Added
- `insecureSkipVerify` now accepts a native boolean or a `${OBS1_SKIP_CERTIFICATE}`
  env reference, resolved at startup, matching the `${OBS1_*}` pattern already used
  for host/username/password.

## [2.5.6] - 2026-07-10

### Security
- Bumped Go to 1.26.5 to patch GO-2026-5856 (crypto/tls).

### Fixed
- Restored multi-arch GHCR container image publishing via GoReleaser `dockers_v2`.

## [2.5.5] - 2026-07-03

### Added
- Test coverage for the `obs_exporter_build_info` metric (build-info release).

## [2.5.4] - 2026-07-03

### Added
- systemd deployment assets: service unit, environment file, and deployment guide.

## [2.5.3] - 2026-07-01

### Changed
- MkDocs now uses the brand icon as its favicon and logo.
- Documented handling of special characters in the monitoring password.

## [2.5.2] - 2026-06-20

### Changed
- Migrated CI to the `fjacquet/ci` make-based reusable workflows.
- Made the `security` workflow advisory to match the central default.

## [2.5.1] - 2026-06-16

### Added
- Helm chart with a lockstep publishing workflow.

## [2.5.0] - 2026-06-14

### Added
- Node Exporter Full (1860) companion Grafana dashboard.

## [2.4.0] - 2026-06-14

### Changed
- Split the Grafana overview into a layered on-call dashboard set.

## [2.3.1] - 2026-06-14

### Added
- ADR-0009 (modular collectors) and ADR-0010 (mockecs harness).

## [2.3.0] - 2026-06-14

### Added
- Windows amd64/arm64 release builds with zip archives.
- Grafana charts for namespace MPU, node transactions, disk attention, and DT.
- OBS 4.2 management API Swagger spec plus ADR-0008 recording its validation findings.

## [2.2.0] - 2026-06-12

### Added
- Native `.env` loading at startup (no-override semantics).

## [2.1.0] - 2026-06-11

### Added
- `${ENV}` expansion in the `host` and `username` config fields.

### Changed
- Adopted the `OBS1_*` env prefix and parameterized the sample cluster entry.

## [2.0.0] - 2026-06-11

Full rewrite — **breaking change** for every v1 user. See
[docs/migration-v2.md](docs/migration-v2.md) for the metric rename table and the
new scrape configuration.

### Changed
- **Architecture**: SNMP-style `/query?target=` on-demand scraping replaced by a
  background snapshot collection loop over config-defined clusters; Prometheus
  scrapes a plain `/metrics`. Every series carries a `cluster` identity label.
- **Metric names**: `emcecs_*` → `ecs_*`, unit-explicit (`_bytes`,
  `_milliseconds`, `_mb_per_second`, …); alerts and transaction errors moved to
  label dimensions (`severity`, `code`/`protocol`/`category`).
- **API**: realigned to the Dell ObjectScale 4.1.0.0 management REST API; namespace
  billing now uses the bulk `POST /object/billing/namespace/info` (one call per
  cycle instead of one per namespace).
- **Configuration**: flags/`ECSENV_*` env vars replaced by a YAML file with
  `${ENV_VAR}` / `passwordFile` secrets, SIGHUP + file-watch hot reload.
- Module path is now `github.com/fjacquet/obs_exporter`; Go 1.26, resty/v2, cobra.

### Added
- OTLP gRPC metric push (optional, `otlp.endpoint`).
- Per-node dashboard metrics from the documented
  `/dashboard/zones/localzone/nodes` endpoint (CPU, memory, NIC, capacity,
  per-node transactions).
- Per-replication-group metrics incl. `replicationRpoLag`; namespace usage
  (`ecs_namespace_used_bytes`) alongside quota; `ecs_up`/`ecs_collector_up` health
  metrics; `/health` endpoint.
- Grafana overview dashboard + end-to-end Compose demo stack with a mock ECS
  (`make demo`).
- GoReleaser releases (binaries, checksums, CycloneDX SBOM, Homebrew cask),
  multi-arch GHCR image, SHA-pinned CI, dependabot, MkDocs site, ADRs.

### Removed
- `/query` endpoint, `emcecs_*` metric names, Travis CI.
- Always-on node DT scraping — now opt-in per cluster (`collectDT: true`).

## [1.0.0] - 2018-05-17
Initial release - [Mark DeNeve](https://github.com/xphyr)

## [1.1.0] - 2018-09-24
Changes to authentication system to cut down on login/logouts that occur - [Mark DeNeve](https://github.com/xphyr)

## [1.2.0] - 2019-07-13
Updates to project layout, and enhancement to http client usage to cut down on memory usage.
Also changed to use go modules by default and have removed all vendored dependencies
Node info is now gathered over port 9021 to enable SSL. If your ECS arrays are behind a firewall be sure to update your rules to allow port 9021 instead of 9020
Loging has been updated to only use Logrus and time format has been updated to be human readable.
[Mark DeNeve](https://github.com/xphyr)
