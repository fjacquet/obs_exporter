# /health always-200 across the exporter family — design

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this design's plan task-by-task.

**Goal:** Extend ADR-0013's argument one step further — `/health` itself must
never answer non-200, in every repo in the exporter family, not just
obs_exporter. `/livez`/`/readyz` already solved the probe-coupling problem
where they exist; this closes the same gap for anything that scrapes
`/health` directly (dashboards, curl, manual checks) and, for the 7 repos
that never got the ADR-0013 treatment, adds `/livez`/`/readyz` at the same
time.

**Origin:** follow-up to ADR-0013 (obs_exporter, 2026-07-31). Argument
restated: an exporter is a probe. "Target unreachable" is data it reports,
not a failure of the exporter process. Encoding that as a non-200 HTTP
status on *any* endpoint — probe or informational — invites the same
mistake ADR-0013 fixed for `/livez`/`/readyz`: something downstream (a
probe, a dashboard health check, a script) treats the exporter itself as
down at exactly the moment its data is most needed.

## Scope — 8 repos, 3 buckets (verified against each repo, not assumed)

Corrected 2026-08-01 after per-repo verification: ppdd_exporter and
ppdm_exporter already have JSON bodies (bucket B), not plain text. Bucket C
is 4 repos, not 6.

| Bucket | Repos | Current state |
|---|---|---|
| A — probes done, /health still 503s | obs_exporter | `/livez` `/readyz` → `staticOKHandler` (`main.go:149-150`, ADR-0013). `/health` (`main.go:292-316`) still writes `http.StatusServiceUnavailable` when `len(snap.Clusters)==0` or any cluster's `OK` is false. |
| B — JSON body already, no probes | pmax_exporter, ppdd_exporter, ppdm_exporter | pmax_exporter: `/health` (`main.go:86`, handler `main.go:204-227`) — `built_at`, `servers: [{server, ok, last_scrape, err}]`, 503 via `main.go:225`. ppdd_exporter: `/health` (`main.go:121-123` wrapper → `healthHandler`, `main.go:234-256`) — `built_at`, `systems: [{system, ok, last_scrape, err}]`, 503 via `main.go:254`. ppdm_exporter: `/health` (`main.go:86` wrapper → `healthHandler`, `main.go:197-219`) — `built_at`, `servers: [{server, ok, last_scrape, err}]`, 503 via `main.go:218`. None of the three has `/livez`/`/readyz`. |
| C — text-only, no probes | nbu_exporter, pflex_exporter, pscale_exporter, pstore_exporter | `/health` returns plain text (`"OK"`/`"OK (starting)"`/`"UNHEALTHY: ..."`), no `/livez`/`/readyz`. nbu_exporter's handler (`main.go:459-483`) additionally makes a **live** `TestConnectivity` network call inline (`main.go:469`) instead of reading cached state — the same anti-pattern ADR-0013 ruled out for probes, present here on the informational endpoint too. |

## Architecture

Same shape as ADR-0013, applied per-repo:

```go
func staticOKHandler(w http.ResponseWriter, _ *http.Request) {
    w.WriteHeader(http.StatusOK)
}
```

Registered wherever `/health` is registered today:

```go
mux.HandleFunc("/livez", staticOKHandler)
mux.HandleFunc("/readyz", staticOKHandler)
```

`/health` handler change, all 8 repos: remove the branch that writes
`http.StatusServiceUnavailable` (or any non-200). Always `w.WriteHeader(http.StatusOK)`
(or let the default 200 stand if nothing else sets a header first). The
per-target `ok`/`err` fields already carry the informational signal — the
HTTP status code stops being an overloaded second channel for the same
fact.

### Bucket A (obs_exporter)

Smallest diff. Delete the `if !healthy { w.WriteHeader(...) }` block in
`healthHandler` (`main.go:312-314`). JSON shape untouched. `/livez`/`/readyz`
untouched (already correct).

### Bucket B (pmax_exporter, ppdd_exporter, ppdm_exporter)

Two changes per repo: add `staticOKHandler` + register `/livez`/`/readyz`
(net-new, copy obs_exporter's verbatim), and drop the 503 branch in the
existing `healthHandler` the same way as bucket A. JSON shape (`servers`
noun for pmax/ppdm, `systems` for ppdd) untouched.

### Bucket C (nbu, pflex, pscale, pstore)

Three changes per repo:

1. Add `staticOKHandler`, register `/livez`/`/readyz` — net-new, same as bucket B.
2. Convert `/health` from plain text to the same JSON shape as bucket A/B,
   built from each repo's existing snapshot store — every repo already
   tracks `Up`/`ScrapeError`/`LastScrape` (or equivalent) per target, so no
   new state is introduced. Field noun matches the repo's own vocabulary
   (e.g. `sites` for nbu_exporter, `servers`/`arrays`/whatever the snapshot
   struct already calls its target list) — **not** forced to "clusters".
   Shape:
   ```json
   {"built_at": "...", "<noun>": [{"<singular>": "...", "ok": true, "last_scrape": "...", "err": ""}]}
   ```
3. Always 200 — drop the existing 503 branch (verified present in all four:
   `nbu_exporter/main.go:475`, `pflex_exporter/main.go:395`,
   `pscale_exporter/main.go:350`, `pstore_exporter/main.go:401`).

**nbu_exporter extra fix:** the current handler calls `TestConnectivity`
live against the target on every `/health` hit (`main.go:469`). Replace with
a read from the cached snapshot store (`s.store.Load().Sites`), matching
every other repo's pattern. A status endpoint must be O(1) and side-effect-free; a live
network call on every hit is the ADR-0013 mistake recurring on `/health`
itself.

## Docs / ADR impact (per repo)

Each of the 7 non-obs_exporter repos gets its own ADR recording this
decision (obs_exporter's ADR-0013 already exists but only covered
`/livez`/`/readyz` — add a follow-up ADR there too, since `/health`'s
behavior is changing independently of that one). Chart `livenessProbe`/
`readinessProbe` defaults get repointed to `/livez`/`/readyz` wherever a
chart exists and isn't already pointed there (obs_exporter already done).
Deployment/troubleshooting docs updated per repo following obs_exporter's
ADR-0013 precedent (kubernetes.md probes section, troubleshooting.md
health-check section).

`exporter-standards` skill (`~/.claude/skills/exporter-standards/references/architecture.md`):
add the canonical rule — `/livez`/`/readyz` are always trivial 200 with no
state read, `/health` is always 200 and purely informational (JSON body
carries per-target status), never probe `/metrics` for liveness/readiness.
This makes the rule discoverable for `ppdm_exporter`'s eventual scaffold
and any future exporter, not just a fix applied once and forgotten.

## Testing

- Per repo: any existing test asserting 503-on-unhealthy (main_test.go or
  equivalent) updated to assert 200 always, with assertions moved to body
  content (`ok: false`, `err: "..."` on the affected target) instead of
  status code.
- New `TestLivezReturnsOK` / `TestReadyzReturnsOK` for the 7 repos that
  don't have them yet (obs_exporter already has these), same pattern as
  ADR-0013: call the handler directly via `httptest.NewRecorder`, no
  snapshot store constructed, proving the handler can't depend on
  collection state by construction.
- Bucket C: new test asserting `/health`'s `Content-Type` is
  `application/json` and the body parses, since this is new behavior for
  those repos (previously plain text).
- Existing chart lint/template CI (where present) picks up the probe path
  change automatically, same as obs_exporter's `helm-charts.yml`.

## Compatibility

- Buckets A and B: not a breaking change. `/health`'s path, JSON shape, and
  per-target fields are unchanged — only the HTTP status code on the
  unhealthy case changes (503 → 200). Anything parsing the JSON body is
  unaffected; anything branching on HTTP status code needs to switch to
  reading the body's `ok`/`err` fields instead. Called out in each repo's
  CHANGELOG.
- Bucket C: **breaking change**, called out explicitly per repo's
  CHANGELOG `### Changed` — `/health`'s body format changes from plain text
  to JSON, and the status code is always 200 regardless of target health.
  Anyone scripting against the old text format or the 503 status needs to
  update. `/livez`/`/readyz` are net-new additions (`### Added`), not
  breaking.
- Chart default probe wiring: same non-breaking-by-default logic as
  ADR-0013 — a fresh `helm install` or an upgrade without pinned probe
  overrides gets the corrected defaults on next chart bump; anyone who
  already overrode probes by hand is unaffected.

## Rollout order

1. obs_exporter (bucket A) — smallest diff, proves the `/health`-only change
   once more since the repo's pattern is already trusted.
2. Buckets B and C fan out in parallel afterward — independent repos, no
   shared state, each gets its own plan/PR.
3. `exporter-standards` skill update lands alongside or immediately after,
   so it reflects reality rather than lagging it.
