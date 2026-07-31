# obs_exporter

[![CI](https://github.com/fjacquet/obs_exporter/actions/workflows/ci.yml/badge.svg)](https://github.com/fjacquet/obs_exporter/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/fjacquet/obs_exporter?include_prereleases&sort=semver)](https://github.com/fjacquet/obs_exporter/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/fjacquet/obs_exporter)](https://goreportcard.com/report/github.com/fjacquet/obs_exporter)
[![Go Version](https://img.shields.io/github/go-mod/go-version/fjacquet/obs_exporter)](go.mod)
[![License](https://img.shields.io/github/license/fjacquet/obs_exporter)](LICENSE)
[![Docs](https://img.shields.io/badge/docs-mkdocs-blue)](https://fjacquet.github.io/obs_exporter/)

Prometheus + OTLP exporter for **Dell EMC ECS / ObjectScale** object-storage
clusters. One process polls every cluster you configure on a fixed interval and
serves the latest snapshot at `/metrics`, so a scrape never reaches the
management API: load on the cluster follows your collection interval, not the
number of Prometheus servers scraping the exporter. An optional OTLP gRPC push
reads the same snapshot.

> **Coming from v1 (`prometheus-emcecs-exporter`), or from v2?** Each of those
> steps renames metrics. [Upgrading](docs/operate/upgrading.md) says which guide
> applies to the version you are on.

## Does it fit your cluster

Built against the ObjectScale **4.1.0.0** management REST API. The dashboard
endpoints and fields it reads are verified unchanged through **4.3**, and the
surface is backward compatible with the ECS **3.x** dashboards that 4.1 extends.

It needs one management account with monitoring (read) rights and network access
from the exporter host to the management port, 4443 by default. Nothing it does
writes to the cluster, and one process covers as many clusters as you list.

## What it exports

- **Cluster** — node and disk health counts, unacknowledged alerts by severity,
  capacity, and the transaction path: latency, bandwidth, rate and errors.
- **Replication groups** — traffic, pending repository/journal/XOR backlog, and
  RPO timestamp and lag.
- **Nodes** — per-node health, capacity, CPU, memory, NIC and transaction stats,
  from the documented dashboard API.
- **Namespaces** — hard and soft quota, usage, object counts and multipart-upload
  backlog, from one bulk billing call per cycle rather than one call per
  namespace.
- **The exporter itself** — whether each cluster and each collector succeeded on
  the last cycle, build information, and a `/health` JSON endpoint.
- **Two opt-in collectors** — `collectDT` reads legacy node-local directory-table
  and connection stats; `collectFlux` reads per-node CPU, memory, network and
  request counters from the cluster's own monitoring store, over the same
  management port and session as everything else. Flux covers fields ObjectScale
  4.3 no longer serves through the dashboard API, and needs the `SYSTEM_MONITOR`
  role.

Full catalog: [docs/metrics/index.md](docs/metrics/index.md).

## Quick start

With hardware: [First run](docs/getting-started/first-run.md) is a four-field
config file, one foreground command, and one `curl` that confirms the cluster
answered.

Without: `make demo` starts a fake ObjectScale cluster, the real exporter,
Prometheus and Grafana with every dashboard already provisioned — see
[Try it without hardware](docs/demo.md).

## Install

On macOS, `brew install --cask fjacquet/tap/obs_exporter`. Everywhere else —
release binaries with checksums and a CycloneDX SBOM, the multi-arch container
image on GHCR, or a build from source — see
[Installation](docs/getting-started/installation.md).

## Configuration

One YAML file: `${ENV_VAR}` interpolation and `passwordFile` for secrets, one
list entry per cluster, and per-cluster flags for the optional collectors. It
reloads on SIGHUP or when the file changes on disk, so adding a cluster or
rotating a password does not need a restart.

Every setting and every flag: [Configuration](docs/getting-started/configuration.md).

## Development

```bash
make ci        # fmt-check, vet, lint, test -race, govulncheck, build
make sure      # quicker local gate
make demo      # end-to-end Compose stack
```

Architecture decisions are recorded in [docs/adr/index.md](docs/adr/index.md).

## Lineage & license

Originally forked from
[paychex/prometheus-emcecs-exporter](https://github.com/paychex/prometheus-emcecs-exporter)
by [Mark DeNeve](https://github.com/xphyr); v2 is a ground-up rewrite. Licensed
under the [Apache 2.0 license](LICENSE).
