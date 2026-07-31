# Static liveness/readiness probes — design

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this design's plan task-by-task.

**Goal:** Add `/livez` and `/readyz`, two probe endpoints that never depend on
cluster state, and repoint the chart's default liveness/readiness probes at
them — closing the trap the chart currently ships with (both probes wired to
`/health`, which the exporter's own docs already tell operators to override by
hand).

**Origin:** proposed by Benjamin (the project's only live-cluster contact,
see `docs/adr/0011` for prior context on that channel) alongside the Flux
capture delivered 2026-07-31. Argument: `/health` answering 503 while any
cluster is unreachable is the right signal for a human or an alerting rule,
and the wrong signal for a readiness probe — pulling the exporter out of the
scrape pool exactly when `ecs_up=0` is the fact worth scraping — and the wrong
signal for a liveness probe twice over, since no restart makes an
unreachable cluster reachable.

## Current state (verified against the repo, not assumed)

- `main.go:146-148` registers `/health` only, calling `healthHandler(w, store)`.
- `healthHandler` (`main.go:290-314`) answers 503 when `len(snap.Clusters) == 0`
  or any cluster's `OK` is false; 200 otherwise. JSON body unchanged by this
  design.
- `charts/obs-exporter/values.yaml:50-57` wires **both** `livenessProbe` and
  `readinessProbe` to `/health`. This is the trap: a fresh `helm install` with
  no overrides gets it wrong on both counts.
- `docs/deployment/kubernetes.md:135-158` already tells operators to override
  `livenessProbe` to `/metrics` by hand, and covers the startup window
  (`/health` answers 503 until the first collection cycle finishes, bounded by
  `collection.timeout`).
- `docs/operate/troubleshooting.md:256-303,499-523` documents the same
  liveness/readiness argument in more depth, plus the `/health` JSON shape and
  a symptom entry for probing `/health` by mistake.
- No ADR currently owns this decision — the chart's `/health`-for-both wiring
  was never one, just an unexamined default.
- `main_test.go` does not exist; `healthHandler` has no unit test today.

## Architecture

Two new handlers in `main.go`, registered next to `/health` — before
`runner.apply`'s initial collection cycle, same as `/health` is today (ADR-0002:
the HTTP server starts serving before the first cycle so a slow login/poll
doesn't look like a dead exporter). Neither reads `SnapshotStore` or any other
process state:

```go
mux.HandleFunc("/livez", staticOKHandler)
mux.HandleFunc("/readyz", staticOKHandler)
```

`staticOKHandler` writes `200 OK` with a minimal body (`ok`) and nothing else
— no JSON, no headers beyond what `net/http` sets by default. Both paths are
fixed, matching the existing `/health` precedent (`troubleshooting.md:305-307`:
"The path `/health` is fixed. Only the metrics path is configurable").

`/health` is untouched: same handler, same 503-on-any-cluster-down behavior,
same JSON body. Nothing that depends on `/health`'s status code today breaks.

## Chart

`charts/obs-exporter/values.yaml:50-57`:

```yaml
livenessProbe:
  httpGet:
    path: /livez
    port: http
readinessProbe:
  httpGet:
    path: /readyz
    port: http
```

No `startupProbe` is added. The chart doesn't define one today, Benjamin's
proposal doesn't ask for one, and `/livez`/`/readyz` have no startup window to
cover in the first place — unlike `/health`, they don't wait on the first
collection cycle. Out of scope; noted so a later reviewer doesn't wonder why
it's missing.

## Docs impact

Grep-verified list of every file that documents `/health` as the probe target
— the same discipline CLAUDE.md's `collectDT`/`collectFlux` note asks for,
applied to an endpoint instead of a metric flag:

- **`docs/deployment/kubernetes.md`** §Probes (~lines 126-158): the manual
  override instructions are no longer needed — the chart is correct by
  default now. Keep the `ecs_up`/`ecs_collector_up` alerting advice; it's
  still the right guidance regardless of which endpoint gates the pod.
- **`docs/operate/troubleshooting.md`** §Checking health without scraping
  (~lines 256-303) and the `/metrics` returns 503 symptom entry
  (~lines 499-523): add `/livez`/`/readyz`, and say plainly that they always
  answer 200 — probing them to diagnose a cluster problem will never show
  one; `/health` or `ecs_up` is what answers that question.
- **New `docs/adr/0013-static-liveness-readiness-probes.md`**: records the
  decision — why cluster state and probe status must not be coupled, what
  `/health` remains for (informational, human/alerting consumption), and
  supersedes nothing (no prior ADR owned the chart's old wiring). Add a row to
  `docs/adr/index.md`'s table.
- **`CHANGELOG.md`**: `### Added` — `/livez` and `/readyz`. `### Changed` —
  the chart's default `livenessProbe`/`readinessProbe` now point at
  `/livez`/`/readyz` instead of `/health`; anyone who already overrode them by
  hand (per the kubernetes.md advice this design retires) is unaffected,
  anyone on chart defaults gets the fix.

`README.md`'s one-line mention of `/health` (line 14) is purely descriptive —
doesn't discuss probes — left alone.

## Testing

- New `main_test.go`: `TestLivezReturnsOK` and `TestReadyzReturnsOK`, each
  calling the handler directly via `httptest.NewRecorder`, asserting
  `http.StatusOK` and the body, with no `SnapshotStore` constructed at all —
  proving the handler cannot depend on collection state by construction, not
  just by inspection.
- `helm-charts.yml` already runs `helm lint` and `helm template` on every PR
  touching `charts/**` (`.github/workflows/helm-charts.yml:19-27`) — no new CI
  wiring needed; the rendered template will show the new probe paths.
- `mkdocs build --strict` (existing docs gate) catches any broken nav entry
  from the new ADR.

## Compatibility

Not a breaking change to the exporter itself: `/health`'s path, status codes,
and JSON body are all unchanged, so nothing that scrapes or scripts against it
today is affected. The chart's default probe wiring does change — anyone
doing a fresh `helm install` or upgrading without pinned probe overrides gets
the new, correct defaults on their next chart bump; the chart's `version` is
auto-set from the app's git tag on release (`Chart.yaml:5-9`), so this ships
in whatever version this work is tagged as. Called out explicitly in the
CHANGELOG rather than left for someone to discover via `git diff`.
