# obs_exporter

Prometheus + OTLP exporter for **Dell EMC ECS / ObjectScale** object-storage
clusters, built against the ObjectScale **4.1.0.0** management REST API (and
compatible with the ECS 3.x dashboard API surface it extends).

## How it works

A single exporter process polls every configured cluster on a fixed interval and
publishes an immutable snapshot. Both export paths read that snapshot — Prometheus
scrapes never hit the ECS API directly, and backend load is independent of how
many Prometheus servers scrape you.

```text
collection loop (every `collection.interval`)
   └─ per cluster: cluster, replication, nodes, info, metering[, dt] collectors
        → immutable Snapshot → SnapshotStore
                                  ├── /metrics  (Prometheus)
                                  └── OTLP push (optional, gRPC)
```

## Highlights

- **Multi-cluster**: one process, many clusters; every series carries a `cluster` label.
- **Dual export**: Prometheus `/metrics` plus optional OTLP gRPC push.
- **OBS 4.1 API**: bulk namespace billing (one POST instead of N GETs), documented
  per-node dashboard stats, replication-group RPO lag.
- **Graceful degradation**: a failing cluster or collector yields `ecs_up=0` /
  `ecs_collector_up=0` instead of breaking the scrape.
- **Hot reload**: SIGHUP or config-file change rebuilds the collection loop without
  dropping `/metrics`.
- **Session hygiene**: ECS caps auth tokens per user; the exporter logs out of every
  cluster on shutdown and re-authenticates on token expiry.

## Where next

- [Installation](getting-started/installation.md)
- [Configuration](getting-started/configuration.md)
- [Quick start](getting-started/quickstart.md) — includes a no-hardware demo stack
- [Metrics reference](metrics.md)
- [Migrating from v1](migration-v2.md) — **v2 is a breaking change**
