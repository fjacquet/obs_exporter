# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Custom labels: an optional top-level `labels:` block declares label keys
  with default values, and a cluster's own `labels:` block may override a
  declared key's value (never introduce a new key). Applied to every exported
  sample, `ecs_up` and `ecs_collector_up` included. See ADR-0014.

### Changed

- `/health` always answers 200, never 503. The JSON body's per-cluster
  `ok`/`err` fields are unchanged and remain the way to tell whether a
  cluster is degraded — read the body, not the status code. See ADR-0015.
  Not a breaking change: the path and JSON shape are unchanged.

## [3.4.0] - 2026-07-31

### Added

- `/livez` and `/readyz`: probe endpoints that always answer 200, with no
  dependency on cluster reachability or the collection cycle. `/health` is
  unchanged. See ADR-0013.

### Changed

- The chart's default `livenessProbe` and `readinessProbe` now point at
  `/livez` and `/readyz` instead of `/health`. A fresh install or an upgrade
  without pinned probe overrides gets the fix automatically; anyone who
  already overrode the probes by hand (per the prior `kubernetes.md` advice)
  is unaffected.

## [3.3.0] - 2026-07-31

This release confronts the Flux collector, shipped undated in 3.2.0, with a
real ObjectScale 4.3.0.0.142978 capture (`flux-capture`) and corrects what the
capture contradicted.

### Behaviour changes an operator would notice

- **`ecs_node_transaction_latency_milliseconds` (the gauge) disappears when
  `collectFlux` is on.** Flux serves that measurement as a histogram, and one
  metric name cannot be both a gauge and a histogram family (ADR-0006); the
  histogram — `ecs_node_transaction_latency_milliseconds_bucket{node,op,le}`
  and `..._count` — replaces it. There is no `_sum`, so `histogram_quantile()`
  works but average latency cannot be reconstructed. Any alert or dashboard
  panel on the gauge, on a `collectFlux` cluster, goes silent; `grafana/dashboards/obs-performance.json`'s
  "Node transaction latency" panel now charts both the gauge and a p95
  estimate from the histogram, distinguished in the legend, so it still draws
  regardless of which family a given cluster emits.
- **Flux-sourced series now go absent within ten minutes of a node falling
  silent, instead of holding a stale value.** The exported Prometheus samples
  do not retain the source timestamp — Flux rows carry `_time`, but Prometheus
  stamps a scraped sample at scrape time, not at `_time` — so the collector
  previously read `last()` over a fifteen-minute window and republished
  whatever it found, however old. Rows are now dated from `_time` and dropped
  past ten minutes (twice the live cluster's five-minute write cadence). A row
  that cannot be dated is dropped too.

### Added

- Per-query failure tolerance: a Flux measurement that fails to compile (a
  malformed query, or one the running Flux version rejects) no longer takes
  the other nine measurements down with it. A permission refusal or transport
  error still fails the whole cycle fast, on the first query.
- Per-node directory-table counts, `ecs_node_dt_total`, from Flux's
  `dtquery_dt_dist_host_dt_node_id` measurement. Arbitrated against the
  existing DT collector (`internal/ecs/dt.go`, port 9101): whichever one runs
  owns the name, so the same metric works whether or not that port is
  reachable.
- Warn-once logging: a Flux measurement that returns no rows for one cluster
  logs a warning on its first occurrence and steps down to debug afterward,
  instead of one warning per cycle for as long as the condition persists.
- `cmd/mockecs` now serves the Flux query endpoint from fixtures, so the demo
  stack and any manual verification exercise the same code path production
  does.
- `flux-capture`, a CLI subcommand that replays the exporter's own Flux
  queries against a live cluster and writes the responses as fixtures —
  proof against the queries actually issued, not a hand-written approximation
  of them.
- `--trace` now logs each Flux query's own body as a logrus field, so the ten
  otherwise-identical POSTs to `/flux/api/external/v2/query` a cycle issues
  are distinguishable in the trace log, previously indistinguishable there.

### Fixed

- `ecsclient.APIError` decodes the ECS error envelope
  (`{code,description,retryable}`) instead of retrying on HTTP status class
  alone. ObjectScale answers both a permission refusal and an invalid Flux
  query with HTTP 500; the client was retrying a refusal three times per
  measurement per cycle for an outcome that could never change. A body that
  does not decode to the envelope makes no claim and still retries as before.
- `internal/ecs/testdata/flux_*.json` fixtures replaced with the live 4.3
  capture; the discriminating power a hand-transcribed `dt_status` fixture
  had lost (every value distinct enough to catch a swapped field) is restored.

### Known limitations — resolved

- 3.2.0 noted the Flux collector's bucket/measurement mapping and `host`-tag
  node identity were derived from the admin guide and unconfirmed against a
  running cluster, and that `cmd/mockecs` did not serve the Flux endpoint.
  Both are addressed by this release: `flux-capture` confirmed the mapping
  against a live 4.3.0.0.142978 cluster, and `cmd/mockecs` now serves the
  endpoint.

## [3.2.0] - 2026-07-30

Non-breaking: no metric is removed or renamed, and no existing series changes
type. A 3.1.0 was drafted while this work was split across two phases; both
phases shipped in one merge, so they are released together here and 3.1.0 was
never cut.

### Added

- Opt-in `collectFlux` collector querying the cluster's Flux monitoring store
  for per-node CPU, memory and network metrics, per-node request counters, and
  cluster-wide DT and transaction metrics the management API does not serve.
  Off by default; requires `SYSTEM_MONITOR` or `SYSTEM_ADMIN`. It reuses the
  management port and session, so it needs no additional network access.
- `ecs_node_maintenance_mode`, from the object-port ping's `MAINTENANCE_MODE`
  item. A node reporting `UNKNOWN` yields an absent sample, never 0.
- `ecs_collector_unmapped_nodes{collector="flux"}` reports Flux rows whose host
  tag matched no node in the inventory. Published every cycle including as 0, so
  a flat zero means the mapping is working rather than that the metric is absent.
- Grafana panels for every new metric, and an "Exporter health" row on the
  overview carrying `ecs_collector_up` and `ecs_collector_unmapped_nodes` —
  neither of which had ever been shown, so a degraded collector was invisible.
- `collectFlux` documented on the operator paths: the annotated config example,
  the per-cluster flag table, the installation prerequisites, the README feature
  list, and the chart's commented values.

### Fixed

- The object-port ping is decoded by item `Name` rather than by position.
  `PingItem` is documented as `0-*` elements with no guaranteed ordering, so
  `ecs_node_active_connections` previously read whichever item came first. Its
  name and meaning are unchanged — the 4.3 REST reference defines `LOAD_FACTOR`
  as the node's active Jetty connection count.
- Two samples sharing a metric name **and** label values no longer fail the whole
  Prometheus `Gather`. The registry errors on a duplicate and `promhttp`'s
  default error handling turns that into HTTP 500 for the entire `/metrics`
  endpoint, across every cluster; the duplicate is now dropped so one bad series
  costs one series.
- `ecs_up` can reach 0 again on a cluster with `collectFlux` enabled. The
  unmapped-nodes housekeeping sample was counted as a domain sample, so a cycle
  in which every collector returned no data still reported the cluster up.

### Changed

- When `collectFlux` is enabled it becomes the sole source of
  `ecs_node_cpu_utilization_percent`, `ecs_node_memory_utilization_percent` and
  `ecs_node_memory_used_bytes`. Every other Flux metric is additive.
- Internal: samples carry a gauge/counter type, honoured by both the Prometheus
  and OTLP export paths. Cumulative Flux counters export as counters; no
  pre-existing series changes type.

### Known limitations

- The Flux collector's bucket and measurement mapping, its response envelope, and
  the `host`-tag node identity are derived from the ObjectScale 4.3 admin guide
  and have not been confirmed against a running cluster. `cmd/mockecs` does not
  yet serve the Flux endpoint. See ADR-0011's Consequences.

## [3.0.0] - 2026-07-30

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
  - node-local scrapes — `ecs_node_dt_up` → `ecs_node_scrape_up{endpoint}`
- **One label key per dimension.** `kind` and `purpose` both named the same
  "which partition of these bytes" semantic as `type`, chosen independently at
  each call site, so no query could group across the families. `type` is now the
  one key: `ecs_replication_group_chunks_pending_bytes{kind}` →`{type}` and
  `ecs_cluster_disk_space_allocated_component_bytes{purpose}` → `{type}`. The
  metric names are unchanged; the label key is not. ADR-0012 records the key
  vocabulary so the next consolidation picks from it instead of inventing a
  synonym.

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
- **`ecs_node_scrape_up{node,endpoint="dt"|"object"}`** replaces
  `ecs_node_dt_up`. The DT collector scrapes two node-local ports that a
  segmented network puts on different addresses, so one up-signal could not
  describe both — and the object-port ping had **no** signal at all: its failure
  showed only as an absent `ecs_node_active_connections`. Each endpoint now
  reports its own reachability. On the recommended production layout you will see
  `{endpoint="dt"}=0` beside `{endpoint="object"}=1`, which is the topology being
  reported accurately rather than a new failure.
- **A `quotas` collector**, split out of `metering`, so
  `ecs_collector_up{collector="quotas"}` exists. Quota reads previously failed
  silently inside metering: a cluster whose every quota GET was denied looked
  identical to one with `collectQuotas: false` — no samples either way, metering
  still reporting up. `collectQuotas` (default `true`, requires
  `collectMetering`) now gates the collector. The split costs one extra namespace
  listing per cycle and gives up the overlap between the quota fan-out and the
  billing POST, since collectors run in sequence within a cluster; both are small
  against the per-namespace GETs they guard.
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
