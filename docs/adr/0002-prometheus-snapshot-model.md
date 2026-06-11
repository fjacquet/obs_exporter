# Background snapshot collection model

## Status

Accepted (2026-06-10)

## Context

v1 scraped ECS on demand per Prometheus request (`/query?target=…`, SNMP-exporter
style). Every scrape hit the management API, so backend load scaled with the
number and frequency of scrapers, slow metering queries could exceed scrape
timeouts, and adding OTLP push would have doubled the API load. The family
standard (ppdd ADR-0001, pstore ADR-0002) prescribes a snapshot model.

## Decision

A single background **collection loop** polls every configured cluster on
`collection.interval` (clusters in parallel via errgroup, per-cluster
`collection.timeout`) and publishes an **immutable Snapshot** into a `SnapshotStore`
(RWMutex pointer-swap). Both export paths read the latest snapshot:

- Prometheus: an *unchecked* collector (`Describe` sends nothing) so the metric
  name set can vary; `Collect` walks the snapshot.
- OTLP: observable gauges whose callbacks read the snapshot; a periodic reader
  pushes on its own cadence.

The HTTP server starts **before** the first collection cycle (pstore ADR-0007):
login plus first poll can exceed a scrape timeout, and a blocked `/metrics` looks
like a dead exporter. Per-cluster failure degrades to `ecs_up=0` and per-collector
`ecs_collector_up=0` rather than failing the cycle.

## Consequences

- ECS API load is constant regardless of scraper count; scrapes are instant.
- Metric staleness is bounded by `collection.interval` — acceptable for
  capacity/health data; lower the interval if fresher data is needed.
- The breaking change for v1 users (scrape config + cluster identity moving into
  the exporter's own config) is documented in the migration guide.
