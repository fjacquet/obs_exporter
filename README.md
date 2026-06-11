# obs_exporter

[![CI](https://github.com/fjacquet/obs_exporter/actions/workflows/ci.yml/badge.svg)](https://github.com/fjacquet/obs_exporter/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/fjacquet/obs_exporter?include_prereleases&sort=semver)](https://github.com/fjacquet/obs_exporter/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/fjacquet/obs_exporter)](https://goreportcard.com/report/github.com/fjacquet/obs_exporter)
[![Go Version](https://img.shields.io/github/go-mod/go-version/fjacquet/obs_exporter)](go.mod)
[![License](https://img.shields.io/github/license/fjacquet/obs_exporter)](LICENSE)
[![Docs](https://img.shields.io/badge/docs-mkdocs-blue)](https://fjacquet.github.io/obs_exporter/)

Prometheus + OTLP exporter for **Dell EMC ECS / ObjectScale** object-storage
clusters, built against the ObjectScale **4.1.0.0** management REST API.

A single exporter polls every configured cluster on an interval and serves the
latest snapshot at `/metrics` — backend API load is independent of how many
Prometheus servers scrape it. An optional OTLP gRPC push reads the same snapshot.

> **v2.0.0 is a breaking change** (new metric names, new scrape model, new
> configuration). Coming from `prometheus-emcecs-exporter` v1.x? Read the
> [migration guide](docs/migration-v2.md).

## Quick start

```bash
export ECS01_PASSWORD='...'
cat > config.yaml <<'YAML'
clusters:
  - name: ecs-prod-01
    host: ecs01.example.com
    username: ecs-monitor
    password: "${ECS01_PASSWORD}"
YAML
./obs_exporter --config config.yaml      # serves :9438/metrics and /health
```

Prometheus side:

```yaml
scrape_configs:
  - job_name: ecs
    static_configs:
      - targets: ["obs-exporter.example.net:9438"]
```

No hardware? `make demo` brings up mock ECS → exporter → Prometheus → Grafana with
a provisioned dashboard at <http://localhost:3000> (admin/admin).

## Install

- **Binaries**: [GitHub Releases](https://github.com/fjacquet/obs_exporter/releases)
  (linux/darwin × amd64/arm64, checksums, CycloneDX SBOM)
- **Homebrew**: `brew install --cask fjacquet/tap/obs_exporter`
- **Container**: `ghcr.io/fjacquet/obs_exporter:latest` (multi-arch, non-root)
- **Source**: `make cli` (Go 1.26.4+)

## What it exports

- **Cluster**: node/disk health counts, unacknowledged alerts by severity,
  capacity, transaction latency/bandwidth/TPS, per-code transaction errors.
- **Replication groups**: traffic, pending repo/journal/XOR backlog, RPO
  timestamp + lag.
- **Nodes** (documented dashboard API): health, per-node capacity, CPU, memory,
  NIC, per-node transaction stats.
- **Namespaces**: hard/soft quota, usage, object counts, multipart-upload backlog
  (one bulk billing call per cycle).
- **Meta**: `ecs_up` / `ecs_collector_up` per cluster, build info; `/health` JSON.
- Opt-in (`collectDT: true`): legacy node-local DT and active-connection stats.

Full catalog: [docs/metrics.md](docs/metrics.md).

## Configuration

YAML with `${ENV_VAR}` interpolation and `passwordFile` secrets, multi-cluster,
SIGHUP + file-watch hot reload. See
[docs/getting-started/configuration.md](docs/getting-started/configuration.md).

## Development

```bash
make ci        # fmt-check, vet, lint, test -race, govulncheck, build
make sure      # quicker local gate
make demo      # end-to-end Compose stack
```

Architecture decisions are recorded in [docs/adr/](docs/adr/index.md).

## Lineage & license

Originally forked from
[paychex/prometheus-emcecs-exporter](https://github.com/paychex/prometheus-emcecs-exporter)
by [Mark DeNeve](https://github.com/xphyr); v2 is a ground-up rewrite. Licensed
under the [Apache 2.0 license](LICENSE).
