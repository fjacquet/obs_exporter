# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

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
