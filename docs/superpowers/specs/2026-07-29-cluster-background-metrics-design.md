# Cluster background-process metrics — design

Date: 2026-07-29
Status: approved, not yet implemented
Branch base: `main` @ v2.7.1. PR #19 is merged and released, so the corrected
`docs/metrics.md` and the v2.7.1 changelog entry this work builds on are already
in `main`.

## Context

The PR #18 contributor supplied sanitized `Trace` and debug logs from a live
ObjectScale **4.3.0.0** cluster: 5 nodes, 54 namespaces, 61 requests, all 200.
Comparing the real `GET /dashboard/zones/localzone` payload against what
`internal/ecs/cluster.go` decodes shows how much is being left on the floor:

| Payload | Live keys | Collected | Not collected |
| --- | --- | --- | --- |
| `localzone` | 97 | 24 | **73** |
| node instance | 39 | 15 | **24** |

Among the uncollected keys are four families with real operational value, all
populated with non-zero data on that cluster:

- **Recovery** — `recoveryBadChunksTotalSizeCurrent` was 10992 bytes of corrupted
  chunks awaiting recovery. A durability signal, arguably the most alert-worthy
  value in the payload, and currently invisible.
- **Garbage collection** — 73.5 GB pending, 8.65 TB reclaimed, 646 GB
  unreclaimable. A GC backlog that stops draining is capacity silently
  disappearing.
- **Allocation breakdown** — 3.94 TB user data vs 3.45 TB system metadata vs
  6.24 TB local protection. Today only the aggregate is exported, so "what is my
  allocated space actually holding?" has no answer.
- **Erasure coding** — coded ratio at 99.998%, plus EC rate and backlog sizes.

All four come from the **same endpoint the exporter already calls once per cycle**,
so collecting them costs no additional request.

### Measurements that shaped this design

Two facts were verified against the live payload rather than assumed.

**`combined` is exactly `user + system`**, to the byte, on all four GC measures:

| Measure | user | system | combined | user+system |
| --- | --- | --- | --- | --- |
| Pending | 73 509 753 023 | 13 958 630 400 | 87 468 383 423 | ✅ equal |
| Reclaimed | 8 650 562 995 967 | 2 585 836 281 600 | 11 236 399 277 567 | ✅ equal |
| Unreclaimable | 646 517 695 252 | 7 918 838 400 | 654 436 533 652 | ✅ equal |
| TotalDetected | 9 370 590 444 242 | 2 607 713 750 400 | 11 978 304 194 642 | ✅ equal |

So `combined` is a pure aggregate and is not exported: `sum without(scope)`
reproduces it exactly.

**Allocation components do NOT sum to allocated.** Sum of the five components is
13 634 410 654 125 against `diskSpaceAllocatedCurrent` of 15 634 270 027 200 —
a 2.0 TB gap, 12.8% unaccounted. The breakdown is not exhaustive, which the
documentation must say plainly or users will compute wrong percentages.

**`Series.Latest()` needs no change.** It is key-agnostic (`points.go:22-46`): a
point's value is its single non-`t` key that parses as a number. The new value keys
`Capacity` and `Rate` therefore work as-is.

## Goals

- Export the four families as gauges from the existing local-zone response.
- Keep `internal/ecs/cluster.go` from becoming a catch-all as it nearly doubles.
- Ground the tests in a real payload, so a mistyped JSON tag cannot pass.

## Non-goals

- Chunk inventory (~20 keys: `chunksRepo*`, `chunksL0/L1Btree*`,
  `chunksL0/L1Journal*`, `chunksXor*`, `chunksGeo*`). Low operational value for 20
  more metric names.
- `gc*ReclaimedOverTimeRange` and `gc*ReclaimedPerInterval`. The window is
  documented nowhere; exported, they would be read as Prometheus counters and yield
  false rates.
- `allocatedCapacityForecast` — a projection, not a measurement.
- Node-level storage-pool label, the `storagepools` / `rglinks*` endpoints, the
  Flux collector, and the `ecs_cluster_nodes{state}` rename. Each is its own
  spec cycle.

## Metrics

23 new series on the reference cluster. Naming follows ADR-0006:
`ecs_<object>_<metric>[_<unit>]`, unit-explicit **only** where the API documents a
unit. `recovery_rate`, `ec_rate` and the two `complete_time_estimate` fields carry
no documented unit, so they carry no unit suffix — matching the existing
`ecs_cluster_replication_ingress_traffic` and `ecs_node_nic_received_bandwidth`.

### `cluster_gc.go` — label `scope="user"|"system"`

| Metric | Source |
| --- | --- |
| `ecs_cluster_gc_pending_bytes` | `gc{User,System}PendingCurrent` |
| `ecs_cluster_gc_reclaimed_bytes` | `gc{User,System}ReclaimedCurrent` |
| `ecs_cluster_gc_unreclaimable_bytes` | `gc{User,System}UnreclaimableCurrent` |
| `ecs_cluster_gc_detected_bytes` | `gc{User,System}TotalDetectedCurrent` |
| `ecs_cluster_gc_enabled` | `gcUserDataIsEnabled` / `gcSystemMetadataIsEnabled` |

The API names the two booleans asymmetrically (`UserData` / `SystemMetadata`) while
the numeric series say `User` / `System`. The mapping onto `scope` needs a comment,
or someone will eventually "fix" the name.

### `cluster_recovery.go` — no labels

| Metric | Source |
| --- | --- |
| `ecs_cluster_recovery_bad_chunks_bytes` | `recoveryBadChunksTotalSizeCurrent` |
| `ecs_cluster_recovery_rate` | `recoveryRateCurrent` |
| `ecs_cluster_recovery_complete_time_estimate` | `recoveryCompleteTimeEstimate` |

### `cluster_ec.go` — no labels

| Metric | Source |
| --- | --- |
| `ecs_cluster_ec_applicable_bytes` | `chunksEcApplicableTotalSealSizeCurrent` |
| `ecs_cluster_ec_coded_bytes` | `chunksEcCodedTotalSealSizeCurrent` |
| `ecs_cluster_ec_coded_ratio_percent` | `chunksEcCodedRatioCurrent` |
| `ecs_cluster_ec_rate` | `chunksEcRateCurrent` |
| `ecs_cluster_ec_complete_time_estimate` | `chunksEcCompleteTimeEstimate` |

### `cluster_allocation.go` — label `purpose`

| Metric | Source |
|---|---|
| `ecs_cluster_disk_space_allocated_component_bytes` | `diskSpaceAllocated{UserData,SystemMetadata,GeoCache,GeoCopy,LocalProtection}Current` |

`purpose` is one of `user_data`, `system_metadata`, `geo_cache`, `geo_copy`,
`local_protection`. The metric name is deliberately distinct from the existing
`ecs_cluster_disk_space_allocated_bytes`: adding a label to a published metric
would break the ADR-0006 invariant and every existing query.

Label-key invariant holds per name — `{cluster, scope}` for GC,
`{cluster, purpose}` for allocation, `{cluster}` for the rest.

## Architecture

### Decoding

`localZoneResp` in `cluster.go` embeds four anonymous structs:

```go
type localZoneResp struct {
    // … existing fields unchanged
    gcFields
    recoveryFields
    erasureCodingFields
    allocationComponentFields
}
```

Go promotes anonymous embedded struct fields during JSON decoding, so the flat
payload decodes unchanged. Each struct is declared in its own family file, next to
its mapping function, so fields and emission cannot drift apart.

### Per-family contract

Each family file exposes one function with the same shape:

```go
func (f gcFields) samples() []Sample
```

No client, no context, no cluster identity — a pure struct-to-samples transform.
`cluster.go`'s `Collect` concatenates the four; the collection loop stamps the
`cluster` label as it already does via `Sample.WithCluster`. Each family is
testable in isolation with no mock and no HTTP.

Rejected alternatives:

- **Everything in `cluster.go`.** ~280 lines carrying two unrelated
  responsibilities (capacity/health/alerts, and background-process telemetry), with
  one correspondingly large test.
- **A separate `ResourceCollector` per family.** Each would re-fetch the same
  endpoint, quadrupling API calls per cycle against ADR-0009 and the snapshot
  model.

### New parsing primitive

`points.go` gains a `Bool` type, sibling to `Num`:

```go
// Bool is a flag the ECS API encodes as a quoted string ("true"/"false").
// Num deliberately refuses these; unparseable values leave Set false.
type Bool struct {
    Val bool
    Set bool
}
```

It belongs in `points.go` because it is the same class of payload weirdness as
`Num` and `Series`, and `Num`'s own doc comment (`points.go:48-50`) already
documents excluding booleans as a gap. Extending `Num` to map `true→1` was
rejected: it would silently change `Num` semantics everywhere it is already used.

### Data flow

Unchanged. One endpoint, one collector, one call per cycle. No new feature flag —
the fields come from a response already being downloaded, so collection costs
nothing extra. The snapshot model (ADR-0002) and the label-key invariant (ADR-0006)
are untouched.

### Absence handling

A missing or unparseable field yields an **absent** sample, never zero (ADR-0007).
This matters more here than elsewhere: `ecs_cluster_gc_pending_bytes` at 0 means
"nothing to reclaim", while absence means "this cluster does not report GC".
Conflating them manufactures false confidence.

Explicitly: a family being **entirely** absent raises **no** warning, unlike the HAL
key handling shipped in v2.7.1. A missing HAL key is shape drift on a known
contract; a missing GC block is a legitimate version difference — 4.1 and 4.2 may
not carry it. Warning on it would emit noise every cycle on older clusters.

## Testing

### Existing fixtures — additive

`internal/ecs/testdata/localzone.json` gains the four families' fields using the
**real shapes** observed in the trace (`Capacity` / `Space` / `Percent` / `Rate`
keys, string-encoded numbers, multiple `t` points) but with **distinct non-zero
values**. The live cluster is idle and returns zeros nearly everywhere; assertions
against zero cannot distinguish "parsed 0" from "absent", which is exactly the
invariant under protection.

Deliberate omissions to build into the fixture:

- `gcSystemMetadataIsEnabled` absent → `ecs_cluster_gc_enabled{scope="system"}`
  must be absent, not `0`.
- `geoCache` and `geoCopy` absent → only three `purpose` values are emitted; the
  series is not padded with zeros.
- `recoveryRateCurrent` carrying an unparseable value (`"N/A"`) → that series is
  absent while the rest of the family still emits.

`cmd/mockecs/fixtures/localzone.json` receives the identical copy (CLAUDE.md rule).

### New real-payload fixture

`internal/ecs/testdata/localzone-live-4.3.json`: the 97-key payload from the trace,
**re-sanitized** — `storagePoolId` replaced with a nil UUID, and the version string
`4.3.0.0.142978.ab620a08b0b8` reduced to `4.3.0.0`. The supplied trace masks names
and IPs but not those two; committing it as-is would publish a real cluster's
storage-pool UUID and build hash.

Exactly one test uses it, and it asserts **no values** — only shape:

- decodes without error;
- all four families produce samples, proving no JSON tag was misspelled against an
  authentic payload;
- no shape-drift warning is logged;
- every emitted series satisfies the label-key invariant.

This is the net that catches a typo in a struct tag — something hand-written
fixtures structurally cannot do, since they carry the same typos as the code.

### Per-family tests

One test file per family file, exercising the pure `samples()` function with a
table of cases: nominal values, absent field, unparseable value, and for GC both
scopes. No mock, no HTTP.

### Integration tests

- `TestClusterCollect` extended: the 23 new series appear with correct values and
  labels.
- `TestLabelKeyConsistency` must stay green without modification.
- Both export paths (`prometheus_test.go` registry gather, `otlp_test.go`
  `ManualReader`) cover at least one series per family, including a labelled one.
  That is the CLAUDE.md rule, and it is where label collisions surface.

## Documentation

`docs/metrics.md` gains the four families with two explicit notes:

- allocation components **do not sum** to `ecs_cluster_disk_space_allocated_bytes`
  — a 12.8% gap measured on a real cluster — so no percentages should be computed
  from them;
- `ecs_cluster_gc_*` has no `scope="combined"`: `sum without(scope)` reproduces the
  API's own aggregate, verified to the byte.

The Grafana dashboard is updated in the same change (CLAUDE.md rule on adding
metrics).

## Gate

`make ci` plus `uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict`.

## Open questions

None blocking.
