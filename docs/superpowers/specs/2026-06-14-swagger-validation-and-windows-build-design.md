# Design: Swagger validation findings, doc/dashboard sync, and Windows build

Date: 2026-06-14
Status: Approved (brainstorming)

## Purpose

Validate the `obs_exporter` implementation against the ObjectScale management REST
API swagger (`docs/swagger/6972-4.1.0.json`), reconcile the metric documentation and
Grafana dashboard with what the code actually emits, and add a Windows release build
to the family.

## Background

- The swagger artifact is titled **"OBS MGT REST API 4.2"** — a superset of the
  4.1.0.0 target named in CLAUDE.md / ADR-0007.
- Every response body in the swagger has an **empty schema** (`type: object`,
  `properties: {}`), consistent with ADR-0007's "ECS payload weirdness". The spec can
  therefore validate **paths, HTTP methods, auth, and request bodies**, but *not*
  response-field→metric mappings (those are reverse-engineered from fixtures).
- DT-collector endpoints (`/stats/dt/DTInitStat`, `/?ping`) are node-local
  diagnostic ports, not management API — out of scope for swagger validation.

## Validation results

Endpoints validated against the swagger (7 management paths used by the code):

| Endpoint | Method | Swagger | Verdict |
|---|---|---|---|
| `/login` | GET (basic auth → `X-SDS-AUTH-TOKEN`) | present, `basic` security | match |
| `/dashboard/zones/localzone` | GET | present | match |
| `/dashboard/zones/localzone/nodes` | GET | present | match |
| `/dashboard/zones/localzone/replicationgroups` | GET | present | match |
| `/object/namespaces` (+ `/namespace/{ns}/quota`) | GET | present | match |
| `/object/billing/namespace/info` | POST | present (path) | body mismatch — F1/F3 |
| `/vdc/nodes` | GET | **absent** | F2 |

Grafana dashboard (`grafana/dashboards/obs-overview.json`) vs emitted metrics:

- Zero broken panels — every metric the dashboard references is emitted.
- Zero documented-but-nonexistent metrics in `docs/metrics.md`.
- 32 emitted metrics are uncharted (mostly meta or duplicates — a subset is worth
  charting, see WS3).
- **F4 (docs drift) — RETRACTED.** An initial full-name `grep` diff reported 28
  emitted metrics as undocumented. On verification this was a measurement artifact:
  `docs/metrics.md` documents those metrics via collapsed shorthand
  (`/ _good_disks / _bad_disks`, `_write_…`, `ecs_node_dt_total / _unready / _unknown`)
  that does not match a full-name grep. The metric documentation is complete; there is
  no drift. WS2 is therefore dropped.

## Scope

Three independent workstreams (WS2 retracted — see F4 above). Decision (2026-06-14):
F1/F2/F3 are documented as risks requiring live-cluster verification; **no blind code
changes** to `metering.go`, `info.go`, or `dt.go`.

### WS1 — Document API risks (no code change)

New ADR `docs/adr/0008-swagger-4.2-validation-findings.md` recording the validation
pass and three open items requiring live-4.1 verification via the client `Trace` mode.
Each item documents impact, swagger evidence, and a verify-then-fix disposition.

- **F1 (HIGH) — billing request body shape.** `metering.go:35` sends
  `billingBulkReq{ID}` → `{"id":[...]}`. Swagger 4.2 documents the body as
  `{"namespace_list":{"id":[...]}}`. Tests pass because `cmd/mockecs` returns the
  fixture without validating the request body. Risk: on a live cluster, all
  `ecs_namespace_used_bytes` / `_objects` / `_mpu_*` metrics may be silently absent.
- **F2 (MEDIUM) — `/vdc/nodes` absent in 4.2.** Used by `info.go` (→ `ecs_cluster_info`
  version) and `dt.go` (node enumeration). The 4.2 spec instead lists `/vdc/vdc/nodes`
  and `/vdc/nodes/geo`. Could be a 4.1→4.2 relocation or a swagger path-doubling
  quirk. Risk: if broken on the target cluster, `ecs_cluster_info` and the entire DT
  collector fail.
- **F3 (LOW) — billing content-type.** Code sends JSON; swagger documents
  `application/xml`. The client already uses `ForceContentType` for ECS content-type
  quirks, so ECS likely tolerates JSON — unverified.

Cross-link the new ADR from ADR-0007; add it to `docs/adr/index.md`.

### WS2 — Documentation drift — RETRACTED

The initially-reported 28 undocumented metrics were a false positive of a full-name
`grep` diff against `docs/metrics.md`, which documents those metrics via collapsed
shorthand. Verification confirmed the metric documentation is complete. No work.

### WS3 — Dashboard panels

Add panels to `grafana/dashboards/obs-overview.json` for valuable uncharted metrics,
matching existing `schemaVersion`, datasource UID, and panel-style conventions:

- **Namespaces row:** "Namespace MPU usage" (`ecs_namespace_mpu_used_bytes`) and MPU
  parts (`ecs_namespace_mpu_parts`).
- **Nodes row:** node-level transactions — read/write per-second
  (`ecs_node_transactions_read_per_second`, `ecs_node_transactions_write_per_second`)
  and bandwidth (`ecs_node_transaction_read_bandwidth_mb_per_second`,
  `ecs_node_transaction_write_bandwidth_mb_per_second`).
- **Cluster health row:** maintenance / ready-to-replace disks (cluster + node):
  `ecs_cluster_maintenance_disks`, `ecs_cluster_ready_to_replace_disks`,
  `ecs_node_maintenance_disks`, `ecs_node_ready_to_replace_disks`.
- **New "Directory Tables (opt-in)" row:** `ecs_node_dt_up`, `ecs_node_dt_total`,
  `ecs_node_dt_unready`, `ecs_node_dt_unknown`, `ecs_node_active_connections`.

Meta metrics (`ecs_collector_up`, `ecs_cluster_info`) and pure duplicates remain
uncharted by design.

### WS4 — Windows build (family-wide)

Decision (2026-06-14): family-wide change covering windows/amd64 + windows/arm64.

- `exporter-standards/references/cicd.md` (line ~45): change the canonical
  `goos: [linux, darwin]` to `goos: [linux, darwin, windows]` and note that Windows
  archives use `zip` format (the `.exe`-in-`.tar.gz` convention is wrong on Windows).
  This makes the standard the source of truth; sibling repos inherit it later.
- `.goreleaser.yaml`:
  - `builds[0].goos`: add `windows` → matrix becomes `{linux, darwin, windows} ×
    {amd64, arm64}`. `CGO_ENABLED=0` keeps cross-compilation clean; GoReleaser
    auto-appends `.exe`.
  - `archives` (the `obs_exporter` tar.gz archive): add
    `format_overrides: [{goos: windows, formats: [zip]}]` so Windows ships a `.zip`
    bundling LICENSE/README/config.yaml. Linux/darwin stay `tar.gz`.
  - The raw-binary archive (`obs_exporter_bin`, `formats: [binary]`) needs no change.
  - `homebrew_casks` (macOS-only) and Docker images (`linux/amd64,linux/arm64`)
    are unaffected.

## Verification

- `make ci` green (fmt-check, vet, golangci-lint, go test -race, govulncheck, build).
  (No Go source changes in this work, but the gate guards against regressions.)
- `goreleaser check` passes; `make release-snapshot` produces Windows `.zip` archives
  and `.exe` binaries for both arches in `dist/`.
- `uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict` passes
  with the new ADR and expanded metrics page.
- Dashboard JSON parses (`jq . grafana/dashboards/obs-overview.json`).
- `TestLabelKeyConsistency` still passes (no metric/label changes, but guard against
  regressions).

## Out of scope

- Any change to `metering.go`, `info.go`, `dt.go`, fixtures, or `cmd/mockecs` (F1/F2/F3
  are documented, not fixed, pending live verification).
- Response-field-level validation (swagger response schemas are empty).
- Rollout of the Windows build to sibling exporter repos (the standard is updated; each
  repo applies it on its own cycle).
