# Swagger validation findings, dashboard panels, and Windows build — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record the OBS-swagger validation findings as an ADR, add Grafana panels for valuable uncharted metrics, and add a Windows (amd64+arm64) release build to the family standard and this repo.

**Architecture:** Three independent, documentation/config-only workstreams. No Go source changes. WS1 adds an ADR + index entry. WS3 appends panels to the dashboard JSON at the bottom (no gridPos churn on existing panels). WS4 edits the family standard (`~/.claude/skills/exporter-standards/references/cicd.md`) and this repo's `.goreleaser.yaml`.

**Tech Stack:** Markdown ADRs, MkDocs (`--strict`), Grafana dashboard JSON (schemaVersion 39, datasource uid `prometheus`, template var `$cluster`), GoReleaser v2.

**Spec:** `docs/superpowers/specs/2026-06-14-swagger-validation-and-windows-build-design.md`

**Note on WS2:** dropped — the reported docs drift was a grep artifact; `docs/metrics.md` documents the metrics via shorthand. No task.

---

## File structure

| File | Workstream | Action |
|---|---|---|
| `docs/adr/0008-swagger-4.2-validation-findings.md` | WS1 | Create |
| `docs/adr/index.md` | WS1 | Modify (add row) |
| `docs/adr/0007-obs-4-1-api-alignment.md` | WS1 | Modify (cross-link) |
| `grafana/dashboards/obs-overview.json` | WS3 | Modify (append panels) |
| `~/.claude/skills/exporter-standards/references/cicd.md` | WS4 | Modify (standard) |
| `.goreleaser.yaml` | WS4 | Modify (add windows) |

---

## WS1 — Document API risks (ADR-0008)

### Task 1: Create ADR-0008

**Files:**
- Create: `docs/adr/0008-swagger-4.2-validation-findings.md`

- [ ] **Step 1: Inspect an existing ADR for the house format**

Run: `sed -n '1,30p' docs/adr/0007-obs-4-1-api-alignment.md`
Expected: see the heading/status/context/decision structure to mirror (title `# N. Title`, `## Status`, `## Context`, `## Decision`/`## Consequences`).

- [ ] **Step 2: Write the ADR**

Create `docs/adr/0008-swagger-4.2-validation-findings.md` with this content (adjust the heading style only if Step 1 shows a different convention):

```markdown
# 8. Swagger 4.2 validation findings — three live-verify items

## Status

Accepted (2026-06-14). Tracks open verification items; supersedes nothing.

## Context

We validated the implementation against the bundled management-API swagger
`docs/swagger/6972-4.1.0.json`. Two facts shape what the validation can prove:

- The artifact is titled **"OBS MGT REST API 4.2"** — a superset of the 4.1.0.0
  target (see ADR-0007).
- Every response body in the swagger has an **empty schema** (`type: object`,
  `properties: {}`), consistent with the payload weirdness recorded in ADR-0007.
  Response-field→metric mappings therefore remain fixture-derived and cannot be
  checked against the spec.

All seven management endpoints the exporter calls exist in the swagger with matching
methods and auth, **except** as noted below. The Grafana dashboard references only
emitted metrics (no broken panels), and `docs/metrics.md` is complete.

Three discrepancies were found that the swagger *can* express (request shape / path)
but which we cannot resolve from the 4.2 artifact alone, because ADR-0007 establishes
the swagger is unreliable on payloads and the target is 4.1. Changing them blind risks
breaking a working integration. They are recorded here for verification against a live
4.1 cluster using the client's `Trace` mode (`ecsclient.Config.Trace`), which logs
method/path/status/body without leaking the auth token.

## Findings

### F1 (HIGH) — billing request body shape

`internal/ecs/metering.go` sends `billingBulkReq{ID}` → JSON `{"id":[...]}` to
`POST /object/billing/namespace/info`. The swagger documents the body as
`{"namespace_list":{"id":[...]}}` (declared `application/xml`). Our tests pass because
`cmd/mockecs` returns the billing fixture without validating the request body.

**Impact if real:** `ecs_namespace_used_bytes`, `_objects`, and `_mpu_*` are silently
absent on a live cluster.

**Disposition:** verify with `Trace` against a live 4.1 cluster. If the wrapper is
required, wrap the request struct and update `cmd/mockecs` + fixtures, then remove this
item.

### F2 (MEDIUM) — `/vdc/nodes` absent in the 4.2 swagger

`internal/ecs/info.go` (→ `ecs_cluster_info` version) and `internal/ecs/dt.go` (node
enumeration) call `GET /vdc/nodes`. The 4.2 swagger does not list it; it lists
`/vdc/vdc/nodes` and `/vdc/nodes/geo` instead. This may be a 4.1→4.2 relocation or a
swagger path-doubling quirk.

**Impact if real:** `ecs_cluster_info` and the entire opt-in DT collector fail on the
target cluster.

**Disposition:** verify `GET /vdc/nodes` still resolves on the live 4.1 cluster. If it
404s, switch the path (and confirm the response still carries `node[].version` /
`mgmt_ip` / `data_ip`).

### F3 (LOW) — billing content-type

The client sends JSON (`ForceContentType("application/json")` for the response); the
swagger documents the billing request as `application/xml`. ECS likely tolerates JSON
(the client already compensates for ECS content-type quirks), but this is unverified.

**Disposition:** confirm the live cluster accepts the JSON request body; no change if
it does.

## Consequences

- No code changes are made now; the three items are tracked here until a live 4.1
  cluster is available for `Trace`-mode verification.
- When verified, fix or close each item and update this ADR's status.
```

- [ ] **Step 3: Verify the ADR renders under strict MkDocs**

Run: `uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict 2>&1 | tail -20`
Expected: build succeeds (exit 0), no warnings about the new file.

- [ ] **Step 4: Commit**

```bash
git add docs/adr/0008-swagger-4.2-validation-findings.md
git commit -m "docs(adr): record swagger 4.2 validation findings (F1-F3)"
```

### Task 2: Wire ADR-0008 into the index and cross-link from ADR-0007

**Files:**
- Modify: `docs/adr/index.md`
- Modify: `docs/adr/0007-obs-4-1-api-alignment.md`

- [ ] **Step 1: Inspect the index format**

Run: `cat docs/adr/index.md`
Expected: a list/table of ADRs. Note the exact format of the existing rows (e.g. `- [ADR-0007: ...](0007-....md)` or a markdown table row).

- [ ] **Step 2: Add the ADR-0008 entry**

Append an entry matching the existing format exactly. If the index is a bullet list:

```markdown
- [ADR-0008: Swagger 4.2 validation findings](0008-swagger-4.2-validation-findings.md)
```

If it is a table, add the equivalent row using the same columns as ADR-0007's row.

- [ ] **Step 3: Add a cross-link in ADR-0007**

Run: `tail -15 docs/adr/0007-obs-4-1-api-alignment.md`
Then append a line at the end of ADR-0007 (under its last section, or as a new
`## Related` section if none exists):

```markdown

## Related

- [ADR-0008: Swagger 4.2 validation findings](0008-swagger-4.2-validation-findings.md) —
  open live-verify items (billing body, `/vdc/nodes`, content-type).
```

If ADR-0007 already has a `## Related` / `## See also` section, add the bullet there
instead of creating a duplicate section.

- [ ] **Step 4: Verify strict build still passes**

Run: `uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict 2>&1 | tail -20`
Expected: build succeeds, no broken-link warnings.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/index.md docs/adr/0007-obs-4-1-api-alignment.md
git commit -m "docs(adr): index and cross-link ADR-0008"
```

---

## WS3 — Dashboard panels

All new panels append at the **bottom** of `grafana/dashboards/obs-overview.json`
(current max `gridPos.y + h` = 68; max panel `id` = 30) so no existing panel's
`gridPos` or `id` changes. Datasource is `{"type":"prometheus","uid":"prometheus"}`;
every query filters `{cluster=~"$cluster"}`. Layout uses 8-wide panels (3 per 24-wide
row), `h: 8`; row markers are `h: 1`, full width.

New panels (ids 31–42):

| id | y | x | type | title | metric(s) | unit |
|---|---|---|---|---|---|---|
| 31 | 68 | 0 | timeseries | Namespace MPU usage | `ecs_namespace_mpu_used_bytes` | bytes |
| 32 | 68 | 8 | timeseries | Namespace MPU parts | `ecs_namespace_mpu_parts` | — |
| 33 | 76 | 0 | row | Node transactions | — | — |
| 34 | 77 | 0 | timeseries | Node transactions/s | `ecs_node_transactions_read_per_second`, `_write_…` | ops |
| 35 | 77 | 8 | timeseries | Node transaction bandwidth | `ecs_node_transaction_read_bandwidth_mb_per_second`, `_write_…` | MBs |
| 36 | 85 | 0 | row | Disk maintenance & replacement | — | — |
| 37 | 86 | 0 | timeseries | Cluster disks needing attention | `ecs_cluster_maintenance_disks`, `ecs_cluster_ready_to_replace_disks` | — |
| 38 | 86 | 8 | timeseries | Node disks needing attention | `ecs_node_maintenance_disks`, `ecs_node_ready_to_replace_disks` | — |
| 39 | 94 | 0 | row | Directory Tables (opt-in) | — | — |
| 40 | 95 | 0 | stat | DT scrape up | `ecs_node_dt_up` | — |
| 41 | 95 | 8 | timeseries | Directory table counts | `ecs_node_dt_total`, `_unready`, `_unknown` | — |
| 42 | 95 | 16 | timeseries | Active connections | `ecs_node_active_connections` | — |

### Task 3: Append the new panels

**Files:**
- Modify: `grafana/dashboards/obs-overview.json`

- [ ] **Step 1: Confirm the append point and ids are still current**

Run:
```bash
jq '{maxBottom:([.panels[]|(.gridPos.y+.gridPos.h)]|max), maxId:([.panels[].id]|max), count:(.panels|length)}' grafana/dashboards/obs-overview.json
```
Expected: `{"maxBottom":68,"maxId":30,"count":30}`. If different, adjust the `y` and
`id` values below by the same offset before editing.

- [ ] **Step 2: Append the 12 panel objects via jq**

Run this exact command (it appends to `.panels` and writes back atomically):

```bash
cd grafana/dashboards
jq '.panels += [
  {"id":31,"type":"timeseries","title":"Namespace MPU usage","datasource":{"type":"prometheus","uid":"prometheus"},"gridPos":{"h":8,"w":8,"x":0,"y":68},"fieldConfig":{"defaults":{"custom":{},"unit":"bytes"},"overrides":[]},"options":{},"targets":[{"datasource":{"type":"prometheus","uid":"prometheus"},"expr":"ecs_namespace_mpu_used_bytes{cluster=~\"$cluster\"}","legendFormat":"{{cluster}} {{namespace}}","refId":"A"}]},
  {"id":32,"type":"timeseries","title":"Namespace MPU parts","datasource":{"type":"prometheus","uid":"prometheus"},"gridPos":{"h":8,"w":8,"x":8,"y":68},"fieldConfig":{"defaults":{"custom":{}},"overrides":[]},"options":{},"targets":[{"datasource":{"type":"prometheus","uid":"prometheus"},"expr":"ecs_namespace_mpu_parts{cluster=~\"$cluster\"}","legendFormat":"{{cluster}} {{namespace}}","refId":"A"}]},
  {"id":33,"type":"row","title":"Node transactions","collapsed":false,"gridPos":{"h":1,"w":24,"x":0,"y":76},"panels":[]},
  {"id":34,"type":"timeseries","title":"Node transactions/s","datasource":{"type":"prometheus","uid":"prometheus"},"gridPos":{"h":8,"w":8,"x":0,"y":77},"fieldConfig":{"defaults":{"custom":{},"unit":"ops"},"overrides":[]},"options":{},"targets":[{"datasource":{"type":"prometheus","uid":"prometheus"},"expr":"ecs_node_transactions_read_per_second{cluster=~\"$cluster\"}","legendFormat":"read {{cluster}} {{node}}","refId":"A"},{"datasource":{"type":"prometheus","uid":"prometheus"},"expr":"ecs_node_transactions_write_per_second{cluster=~\"$cluster\"}","legendFormat":"write {{cluster}} {{node}}","refId":"B"}]},
  {"id":35,"type":"timeseries","title":"Node transaction bandwidth","datasource":{"type":"prometheus","uid":"prometheus"},"gridPos":{"h":8,"w":8,"x":8,"y":77},"fieldConfig":{"defaults":{"custom":{},"unit":"MBs"},"overrides":[]},"options":{},"targets":[{"datasource":{"type":"prometheus","uid":"prometheus"},"expr":"ecs_node_transaction_read_bandwidth_mb_per_second{cluster=~\"$cluster\"}","legendFormat":"read {{cluster}} {{node}}","refId":"A"},{"datasource":{"type":"prometheus","uid":"prometheus"},"expr":"ecs_node_transaction_write_bandwidth_mb_per_second{cluster=~\"$cluster\"}","legendFormat":"write {{cluster}} {{node}}","refId":"B"}]},
  {"id":36,"type":"row","title":"Disk maintenance & replacement","collapsed":false,"gridPos":{"h":1,"w":24,"x":0,"y":85},"panels":[]},
  {"id":37,"type":"timeseries","title":"Cluster disks needing attention","datasource":{"type":"prometheus","uid":"prometheus"},"gridPos":{"h":8,"w":8,"x":0,"y":86},"fieldConfig":{"defaults":{"custom":{}},"overrides":[]},"options":{},"targets":[{"datasource":{"type":"prometheus","uid":"prometheus"},"expr":"ecs_cluster_maintenance_disks{cluster=~\"$cluster\"}","legendFormat":"maintenance {{cluster}}","refId":"A"},{"datasource":{"type":"prometheus","uid":"prometheus"},"expr":"ecs_cluster_ready_to_replace_disks{cluster=~\"$cluster\"}","legendFormat":"ready-to-replace {{cluster}}","refId":"B"}]},
  {"id":38,"type":"timeseries","title":"Node disks needing attention","datasource":{"type":"prometheus","uid":"prometheus"},"gridPos":{"h":8,"w":8,"x":8,"y":86},"fieldConfig":{"defaults":{"custom":{}},"overrides":[]},"options":{},"targets":[{"datasource":{"type":"prometheus","uid":"prometheus"},"expr":"ecs_node_maintenance_disks{cluster=~\"$cluster\"}","legendFormat":"maintenance {{cluster}} {{node}}","refId":"A"},{"datasource":{"type":"prometheus","uid":"prometheus"},"expr":"ecs_node_ready_to_replace_disks{cluster=~\"$cluster\"}","legendFormat":"ready-to-replace {{cluster}} {{node}}","refId":"B"}]},
  {"id":39,"type":"row","title":"Directory Tables (opt-in)","collapsed":false,"gridPos":{"h":1,"w":24,"x":0,"y":94},"panels":[]},
  {"id":40,"type":"stat","title":"DT scrape up","datasource":{"type":"prometheus","uid":"prometheus"},"gridPos":{"h":8,"w":8,"x":0,"y":95},"fieldConfig":{"defaults":{"custom":{}},"overrides":[]},"options":{},"targets":[{"datasource":{"type":"prometheus","uid":"prometheus"},"expr":"ecs_node_dt_up{cluster=~\"$cluster\"}","legendFormat":"{{cluster}} {{node}}","refId":"A"}]},
  {"id":41,"type":"timeseries","title":"Directory table counts","datasource":{"type":"prometheus","uid":"prometheus"},"gridPos":{"h":8,"w":8,"x":8,"y":95},"fieldConfig":{"defaults":{"custom":{}},"overrides":[]},"options":{},"targets":[{"datasource":{"type":"prometheus","uid":"prometheus"},"expr":"ecs_node_dt_total{cluster=~\"$cluster\"}","legendFormat":"total {{cluster}} {{node}}","refId":"A"},{"datasource":{"type":"prometheus","uid":"prometheus"},"expr":"ecs_node_dt_unready{cluster=~\"$cluster\"}","legendFormat":"unready {{cluster}} {{node}}","refId":"B"},{"datasource":{"type":"prometheus","uid":"prometheus"},"expr":"ecs_node_dt_unknown{cluster=~\"$cluster\"}","legendFormat":"unknown {{cluster}} {{node}}","refId":"C"}]},
  {"id":42,"type":"timeseries","title":"Active connections","datasource":{"type":"prometheus","uid":"prometheus"},"gridPos":{"h":8,"w":8,"x":16,"y":95},"fieldConfig":{"defaults":{"custom":{}},"overrides":[]},"options":{},"targets":[{"datasource":{"type":"prometheus","uid":"prometheus"},"expr":"ecs_node_active_connections{cluster=~\"$cluster\"}","legendFormat":"{{cluster}} {{node}}","refId":"A"}]}
]' obs-overview.json > obs-overview.json.tmp && mv obs-overview.json.tmp obs-overview.json
cd ../..
```

- [ ] **Step 3: Verify the JSON is valid and the panels landed**

Run:
```bash
jq '{count:(.panels|length), newTitles:[.panels[]|select(.id>=31)|.title]}' grafana/dashboards/obs-overview.json
```
Expected: `count` = 42, and `newTitles` lists the 12 titles from the table above.

- [ ] **Step 4: Verify every new panel references an emitted metric**

Run:
```bash
for m in ecs_namespace_mpu_used_bytes ecs_namespace_mpu_parts ecs_node_transactions_read_per_second ecs_node_transactions_write_per_second ecs_node_transaction_read_bandwidth_mb_per_second ecs_node_transaction_write_bandwidth_mb_per_second ecs_cluster_maintenance_disks ecs_cluster_ready_to_replace_disks ecs_node_maintenance_disks ecs_node_ready_to_replace_disks ecs_node_dt_up ecs_node_dt_total ecs_node_dt_unready ecs_node_dt_unknown ecs_node_active_connections; do
  grep -rqlF "\"$m\"" $(find internal -name '*.go' ! -name '*_test.go') && echo "OK  $m" || echo "MISSING $m";
done
```
Expected: every line `OK` (all 15 metrics are emitted by the collectors).

- [ ] **Step 5: Commit**

```bash
git add grafana/dashboards/obs-overview.json
git commit -m "feat(grafana): chart namespace MPU, node transactions, disk attention, DT"
```

---

## WS4 — Windows build (family-wide)

### Task 4: Update the family standard (cicd.md)

**Files:**
- Modify: `~/.claude/skills/exporter-standards/references/cicd.md` (around line 45)

> This file is **outside** the repo. Editing it changes the family standard so sibling
> exporters inherit the Windows target on their own cycle.

- [ ] **Step 1: Confirm the current text**

Run: `sed -n '45,47p' ~/.claude/skills/exporter-standards/references/cicd.md`
Expected:
```
- `builds`: `CGO_ENABLED=0`, `goos: [linux, darwin]`, `goarch: [amd64, arm64]`, `-trimpath`,
  `ldflags: -s -w -X main.version={{ .Version }}`, `mod_timestamp: {{ .CommitTimestamp }}` (reproducible).
- `archives`: `tar.gz`, include `LICENSE README.md config.yaml`.
```

- [ ] **Step 2: Edit the `builds` and `archives` bullets**

Change the `goos` list to include `windows`, and note the zip override on `archives`.
Replace:
```
- `builds`: `CGO_ENABLED=0`, `goos: [linux, darwin]`, `goarch: [amd64, arm64]`, `-trimpath`,
  `ldflags: -s -w -X main.version={{ .Version }}`, `mod_timestamp: {{ .CommitTimestamp }}` (reproducible).
- `archives`: `tar.gz`, include `LICENSE README.md config.yaml`.
```
with:
```
- `builds`: `CGO_ENABLED=0`, `goos: [linux, darwin, windows]`, `goarch: [amd64, arm64]`, `-trimpath`,
  `ldflags: -s -w -X main.version={{ .Version }}`, `mod_timestamp: {{ .CommitTimestamp }}` (reproducible).
- `archives`: `tar.gz`, include `LICENSE README.md config.yaml`; Windows uses a `zip`
  `format_override` (`.exe`-in-`tar.gz` is wrong on Windows).
```

- [ ] **Step 3: Verify the edit**

Run: `sed -n '45,48p' ~/.claude/skills/exporter-standards/references/cicd.md`
Expected: the `goos` line now reads `[linux, darwin, windows]` and the archives bullet
mentions the Windows `zip` override.

- [ ] **Step 4: No commit**

This file lives outside the git repo; there is nothing to commit here. Proceed to Task 5.

### Task 5: Add Windows to `.goreleaser.yaml`

**Files:**
- Modify: `.goreleaser.yaml` (the `builds` and `archives` blocks)

- [ ] **Step 1: Add `windows` to the build `goos`**

In `.goreleaser.yaml`, in the `builds:` entry (`id: obs_exporter`), replace:
```yaml
    goos: [linux, darwin]
    goarch: [amd64, arm64]
```
with:
```yaml
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
```

- [ ] **Step 2: Add the Windows zip override to the tar.gz archive**

In the `archives:` section, the `obs_exporter` archive currently reads:
```yaml
  - id: obs_exporter
    formats: [tar.gz]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    files:
      - LICENSE
      - README.md
      - config.yaml
```
Add a `format_overrides` key so Windows ships a zip (place it directly under `formats`):
```yaml
  - id: obs_exporter
    formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    files:
      - LICENSE
      - README.md
      - config.yaml
```
Leave the `obs_exporter_bin` (`formats: [binary]`) archive unchanged — GoReleaser
auto-appends `.exe` to the Windows binary.

- [ ] **Step 3: Validate the GoReleaser config**

Run: `goreleaser check`
Expected: `config is valid` (exit 0). `goreleaser` is called directly by the Makefile
(`release: goreleaser release --clean`), so it must be on PATH; if missing, install it
(`brew install goreleaser` or `go install github.com/goreleaser/goreleaser/v2@latest`).

- [ ] **Step 4: Dry-run the release and confirm Windows artifacts**

Run: `make release-snapshot`
Then:
```bash
ls dist/ | grep -iE 'windows'
```
Expected: Windows artifacts for both arches, e.g.
`obs_exporter_<ver>_windows_amd64.zip`, `obs_exporter_<ver>_windows_arm64.zip`, and the
matching raw `.exe` binaries.

- [ ] **Step 5: Confirm the cask is unaffected**

Run: `grep -A2 'homebrew_casks' .goreleaser.yaml | head -3`
Expected: unchanged (macOS-only; it selects the darwin tar.gz). No edit needed.

- [ ] **Step 6: Commit**

```bash
git add .goreleaser.yaml
git commit -m "feat(release): add windows amd64+arm64 build with zip archives"
```

---

## Final verification

- [ ] **Step 1: Full CI gate (guards against regressions)**

Run: `make ci`
Expected: all stages pass (fmt-check, vet, golangci-lint, go test -race, govulncheck, build).

- [ ] **Step 2: Strict docs build**

Run: `uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict 2>&1 | tail -10`
Expected: success, no warnings.

- [ ] **Step 3: GoReleaser config valid**

Run: `goreleaser check`
Expected: `config is valid`.

- [ ] **Step 4: Dashboard JSON valid**

Run: `jq -e '.panels|length==42' grafana/dashboards/obs-overview.json`
Expected: prints `true`, exit 0.

---

## Self-review notes

- **Spec coverage:** WS1 → Tasks 1–2; WS3 → Task 3; WS4 → Tasks 4–5. WS2 retracted (no task, by design). All in-scope spec items mapped.
- **No code changes:** consistent with the spec's "out of scope" (F1/F2/F3 documented, not fixed).
- **Append-only dashboard:** new ids 31–42 and y≥68 avoid touching existing panels; Step 1 of Task 3 guards the assumption.
