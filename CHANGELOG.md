# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Removed — BREAKING
- **Around thirty metric names are gone**, consolidated into one name plus a
  label wherever they were one measurement split across several names
  (ADR-0012). `docs/migration-v3.md` carries the complete old→new table; the
  short version:
  - counts by state — `ecs_cluster_{good,bad,maintenance}_nodes`,
    `ecs_cluster_{good,bad,maintenance,ready_to_replace}_disks` and the
    `ecs_node_*` equivalents → `ecs_cluster_nodes{state}`,
    `ecs_cluster_disks{state}`, `ecs_node_disks{state}`
  - capacity — `disk_space_{allocated,free,reserved}_bytes` →
    `ecs_cluster_disk_space_bytes{type}`, `ecs_node_disk_space_bytes{type}`
  - transactions — the `read`/`write` name pairs for latency, bandwidth and TPS →
    `{op="read"|"write"}`, cluster and node; `transaction_errors_total` and
    `transaction_successes_total` → `ecs_cluster_transactions_total{outcome}`
  - replication — `replication_{ingress,egress}_traffic` →
    `…_traffic{direction}`, cluster and per group; the three
    `chunks_*_pending_*_bytes` → `ecs_replication_group_chunks_pending_bytes{kind}`
  - GC — `gc_pending_bytes` and `gc_unreclaimable_bytes` →
    `ecs_cluster_gc_bytes{scope,state}`
  - NIC — `nic_{received,transmitted}_bandwidth` → `ecs_node_nic_bandwidth{direction}`

### Changed — BREAKING
- **`ecs_cluster_nodes`, `ecs_cluster_disks` and `ecs_node_disks` changed
  meaning.** They still exist and still return data, but they are now the
  per-state breakdown, not the total. The totals moved to
  `ecs_cluster_nodes_installed`, `ecs_cluster_disks_installed` and
  `ecs_node_disks_installed`. A dashboard or alert querying the old names keeps
  working and silently starts answering a different question — grep for these
  three before upgrading. Note the totals are deliberately **not** the sum of the
  state series: ECS documents five node health states and publishes a count for
  three, so `installed - sum(by state)` is a real "unaccounted for" signal.
- All six bundled Grafana dashboards were rewritten against the new names (47
  query references). Pull them together with the binary.
- The consolidation moves series between names; it does not add or drop any. A
  full `--once --debug` cycle against the reference payload still emits exactly
  127 samples, the same count as 2.8.1.
- **DT node labels now match the other collectors.** `ecs_node_dt_*` and
  `ecs_node_active_connections` were labeled `node="<management IP>"` while every
  other per-node metric used the dashboard's `displayName`, so the two sets could
  not be joined in a query. They now use the `/vdc/nodes` `nodename`, which on a
  live 4.3 cluster is exactly the dashboard's `displayName` for all five nodes.
  Nodes whose inventory entry has no `nodename` still fall back to an IP.
  **Breaking for `collectDT` users** who select or join on the old IP-valued
  label; `collectDT` is off by default, so clusters that never enabled it are
  unaffected.
- Per-namespace quota requests now run concurrently (8 in flight) instead of one
  after another. Emitted sample order still follows the namespace inventory, so
  `--once --debug` output stays diffable between cycles.

### Added
- ADR-0012 records the consolidation rule and the **sum-safety rule** that bounds
  it — a whole and its parts never share a metric name — with the identities that
  forced three exceptions, each verified on a live 4.3 payload:
  `disk_space_total = allocated + free + reserved` (delta 0),
  `gc_detected = pending + unreclaimable + reclaimed` (delta 0 on both scopes),
  and `ec_coded ⊂ ec_applicable`. So `disk_space_total_bytes`, the two GC
  counters, and the `ec_applicable`/`ec_coded` pair keep separate names —
  the last being the one item of the proposal that was declined.
- `docs/migration-v3.md`: full rename table, the three meaning changes called out
  first, query-rewrite patterns, and the aggregations the consolidation makes
  possible (`topk` across states, `state!="good"`, `sum without(kind)`).
- `collectQuotas` (per cluster, default `true`) disables the per-namespace quota
  fetch while keeping the rest of metering. The management API has no bulk quota
  endpoint, so quotas are the only part of a collection cycle that scales with
  namespace count — one GET per namespace. On a 55-namespace 4.3 cluster with no
  quotas configured, those 55 requests produced zero samples every cycle.
- A HAL payload carrying **both** `_instances` and `instances` with different
  contents now logs a warning naming the cluster and path. The `_instances`
  preference is unchanged and still correct; what changes is that discarding the
  other array is no longer silent. Never observed in the field — from ECS 3.8 to
  ObjectScale 4.3 only `_instances` has been seen — but it was the one remaining
  way this decoder could drop data without saying so.

### Fixed
- The opt-in DT collector scraped both of its node-local ports at the node's
  `mgmt_ip`. The object port (9021) answers on the **data** network, so on any
  cluster that separates management from data traffic — the layout Dell
  recommends for production, and the one a 4.3 site confirmed with `ss` and
  `curl` — every `ecs_node_active_connections` scrape silently failed. The ping
  now targets `data_ip` from `/vdc/nodes`, falling back to `mgmt_ip` when the
  inventory publishes no data address; DT stats keep using `mgmt_ip`.

### Documentation
- ADR-0011 accepts an **opt-in Flux collector** as the direction for the metrics
  the management API does not serve: the per-node CPU/memory/NIC and cluster
  `transaction*` fields the dashboard documents but does not populate (re-confirmed
  on 4.3.0.0.142978 — all twelve absent from a 40-key node instance, no
  `transaction*` key in a 98-key cluster payload), and the DT counts that a
  segmented network puts out of reach. Implementation is deferred; the ADR records
  the constraints it must meet and the questions it must answer.
- `docs/metrics.md` documents the DT reachability limit: on a segmented cluster
  port 9101 listens on a private link-local VLAN, so an external exporter gets
  `ecs_node_dt_up=0` while `ecs_node_active_connections` works over the data
  network.

## [2.8.1] - 2026-07-30

### Fixed
- Time-series metrics no longer publish a stale reading as a live value. When the
  newest point of a dashboard series was unreadable (`"N/A"`, `null`), the parser
  fell back to the most recent point that *did* parse and exported that instead —
  and once it reaches Prometheus, nothing distinguishes a stale reading from a
  current one. The newest point is now chosen first and only then read, so an
  unreadable current reading yields an absent sample. This is the absent-never-zero
  rule of ADR-0007 applied to the time axis, and affects every `Series` metric.

### Changed
- Every collector now emits through the shared `appendSeries`/`appendNum`/
  `appendBool` helpers; the hand-rolled equivalents in `cluster.go`, `nodes.go`
  and `replication.go` are gone, so the absent-never-zero rule has one
  implementation instead of four. The helpers also copy the caller's label slice,
  which matters where one label set is built per instance and spread into a dozen
  appends. No metric, label or value changes: the 127 samples emitted from the
  reference payload are byte-identical before and after.

## [2.8.0] - 2026-07-29

### Added
- Cluster background-process metrics, all from the local-zone dashboard response
  the exporter already fetches once per cycle (no additional API call):
  - garbage collection — `ecs_cluster_gc_pending_bytes`,
    `_unreclaimable_bytes`, `_reclaimed_bytes_total`, `_detected_bytes_total`
    and `ecs_cluster_gc_enabled`, labelled `scope="user"|"system"`. Reclaimed
    and detected carry `_total` because they are lifetime counters: on a real
    4.3 cluster detected equalled pending + unreclaimable + reclaimed exactly,
    on both scopes. Combined figures are not exported: they equal
    `user + system` exactly, so `sum without(scope)` reproduces them without
    double-counting.
  - chunk recovery — `ecs_cluster_recovery_bad_chunks_bytes` (corrupted data
    awaiting recovery), `ecs_cluster_recovery_rate`,
    `ecs_cluster_recovery_complete_time_estimate`.
  - erasure coding — `ecs_cluster_ec_applicable_bytes`, `_coded_bytes`,
    `_coded_ratio_percent`, `ecs_cluster_ec_rate`,
    `ecs_cluster_ec_complete_time_estimate`.
  - allocated-space breakdown —
    `ecs_cluster_disk_space_allocated_component_bytes{purpose}`. Note this does
    **not** sum to `ecs_cluster_disk_space_allocated_bytes`; the breakdown is not
    exhaustive.
- Grafana dashboard "ObjectScale — Storage internals" covering the four families.
- The overview dashboard gains the two panels that belong on a landing page:
  corrupted data awaiting recovery, under *Health*, and allocated space by
  purpose, under *Capacity runway* — which until now showed total and free
  without saying what the allocated space actually holds. The purpose panel is
  deliberately unstacked: the components do not sum to the total.

## [2.7.1] - 2026-07-29

### Fixed
- The node and replication-group collectors now decode the HAL instance list
  under **either** `_embedded._instances` (what real ECS/ObjectScale clusters
  emit, confirmed from 3.8 through 4.3) or `_embedded.instances` (what the Dell
  REST API reference examples show), resolving the open question left in 2.7.0.
  Accepting only one spelling meant a cluster using the other emitted no
  `ecs_node_*` or `ecs_replication_group_*` metrics while `ecs_collector_up` still
  reported `1`. When neither key is present the collector now logs a warning, so
  a future payload change leaves a trace instead of failing silently.

## [2.7.0] - 2026-07-26

### Fixed
- Node and replication-group collectors now read the HAL list under the
  `_embedded._instances` key actually emitted by ECS/ObjectScale clusters
  (confirmed live against 4.3). They previously used `_embedded.instances` as
  shown in the Dell REST API reference, which no real cluster returns — so every
  `ecs_node_*` and `ecs_replication_group_*` metric was silently absent while the
  collectors still reported healthy. Some clusters may follow the documented
  `instances` form; supporting both keys is under discussion.
- Cluster-level `ecs_cluster_replication_ingress_traffic` / `_egress_traffic` are
  now decoded as time-series arrays (`[{"t":…,"Bandwidth":…}]`), the shape real
  clusters return (confirmed live against 4.3). They were typed as scalars and so
  silently dropped, even though the field was present. The per-RG equivalents were
  already correct.

### Added
- Cluster capacity gains `ecs_cluster_disk_space_reserved_bytes` and
  `ecs_cluster_disk_space_offline_total_bytes` (from `diskSpaceReservedCurrent` /
  `diskSpaceOfflineTotalCurrent` in the local-zone dashboard payload).
- VDC-wide replication RPO: `ecs_cluster_replication_rpo_lag_seconds` and
  `ecs_cluster_replication_rpo_timestamp_seconds`, complementing the existing
  per-group `ecs_replication_group_rpo_*` metrics.
- Per-node health state `ecs_node_health_state{state}` preserving the
  good/bad/maintenance distinction alongside the boolean `ecs_node_healthy`.

All new fields are sourced from the dashboard API and verified present and
type-stable across the ObjectScale 4.1, 4.2, and 4.3 management API references.

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
