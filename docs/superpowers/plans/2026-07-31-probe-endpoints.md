# Static Liveness/Readiness Probes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `/livez` and `/readyz` — two probe endpoints that always answer
200, with no dependency on cluster state or collection cycles — and repoint
the chart's default probes at them, closing the trap where a fresh
`helm install` wires both liveness and readiness to `/health`.

**Architecture:** Two trivial handlers in `main.go`, registered next to the
existing `/health` handler before the first collection cycle runs, reading no
process state. `/health` itself is untouched. The chart's default probe paths
change; docs that told operators to override the chart by hand are corrected;
a new ADR records the decision.

**Tech Stack:** Go 1.26.4, `net/http`, Helm chart YAML, MkDocs.

## Global Constraints

- `/health`'s path, status codes (200 / 503), and JSON body must not change —
  this is an additive change, not a breaking one to the exporter itself
  (spec's Compatibility section).
- `/livez` and `/readyz` must not read `SnapshotStore` or any other collection
  state — proven by tests that construct no store at all, not just asserted.
- Both new paths are fixed, matching the existing `/health` precedent
  (`server.uri` only moves `/metrics`).
- No `startupProbe` — explicitly out of scope (spec's Chart section).
- Full gate before any task is done: `make ci` for Go changes; `helm lint
  charts/obs-exporter` + `helm template release-test charts/obs-exporter` for
  chart changes; `uvx --with mkdocs-material --with pymdown-extensions mkdocs
  build --strict` for doc changes.

---

### Task 1: `/livez` and `/readyz` handlers

**Files:**
- Modify: `main.go:146-148` (handler registration, right after `/health`)
- Create: `main_test.go`

**Interfaces:**
- Produces: `staticOKHandler(w http.ResponseWriter, _ *http.Request)` — a
  package-level function in `main.go`, used directly as the `http.HandlerFunc`
  for both `/livez` and `/readyz`. No other task depends on this function's
  name, but keep it exported-lowercase (package-private) exactly as written
  here — the test file references it by this name.

- [ ] **Step 1: Write the failing tests**

Create `main_test.go`:

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLivezReturnsOK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	rec := httptest.NewRecorder()

	staticOKHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Fatalf("body = %q, want %q", got, "ok")
	}
}

func TestReadyzReturnsOK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	staticOKHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Fatalf("body = %q, want %q", got, "ok")
	}
}
```

Both tests call the same handler function under both endpoints' names —
`/livez` and `/readyz` collapse to one check for this exporter (no
distinction between "process alive" and "process ready" makes sense here,
since neither depends on cluster state). This is deliberate, not
duplication: it proves both routes resolve to the same state-free behavior
by construction.

No `ecs.SnapshotStore` is constructed anywhere in this file — that absence is
itself the assertion that these handlers cannot depend on collection state.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./... -run 'TestLivezReturnsOK|TestReadyzReturnsOK' -v`
Expected: FAIL — `undefined: staticOKHandler`

- [ ] **Step 3: Add the handler and register both routes**

In `main.go`, add this function near `healthHandler` (after its closing
brace, so both probe-related handlers stay together):

```go
// staticOKHandler always answers 200 — no cluster state, no collection
// state, nothing that can make it fail. /livez and /readyz both use it: a
// probe wired here can never be the reason a healthy process gets restarted
// or pulled from rotation. /health remains the endpoint for anything that
// wants to know whether a cluster is actually reachable.
func staticOKHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
```

Then in `main.go`, immediately after the existing `/health` registration
(currently lines 146-148):

```go
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		healthHandler(w, store)
	})
```

add:

```go
	mux.HandleFunc("/livez", staticOKHandler)
	mux.HandleFunc("/readyz", staticOKHandler)
```

so the block reads:

```go
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		healthHandler(w, store)
	})
	mux.HandleFunc("/livez", staticOKHandler)
	mux.HandleFunc("/readyz", staticOKHandler)
```

This sits before `runner.apply(ctx, cfg)` (the initial collection cycle),
same as `/health` — see the comment already above this block at
`main.go:162-165` explaining why the server starts serving before the first
cycle. No change needed to that comment; it already describes exactly why
these three routes are registered here rather than after collection starts.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./... -run 'TestLivezReturnsOK|TestReadyzReturnsOK' -v`
Expected: PASS

- [ ] **Step 5: Run the full gate**

Run: `make ci`
Expected: PASS — fmt via lint, `go test -race`, build, govulncheck.

- [ ] **Step 6: Commit**

```bash
git add main.go main_test.go
git commit -m "feat: add /livez and /readyz, probe endpoints independent of cluster state

Both always answer 200 with no dependency on SnapshotStore or the
collection cycle -- a probe wired here can never restart or de-pool a
healthy process over an unreachable cluster. /health is unchanged and
remains the endpoint for that signal.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Chart default probe paths

**Files:**
- Modify: `charts/obs-exporter/values.yaml:50-57`

**Interfaces:**
- Consumes: the `/livez` and `/readyz` paths Task 1 registered. No Go
  interface involved.

- [ ] **Step 1: Change the default probe paths**

In `charts/obs-exporter/values.yaml`, replace:

```yaml
livenessProbe:
  httpGet:
    path: /health
    port: http
readinessProbe:
  httpGet:
    path: /health
    port: http
```

with:

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

- [ ] **Step 2: Lint and render the chart**

Run:
```bash
helm lint charts/obs-exporter
helm template release-test charts/obs-exporter | grep -A2 'livenessProbe:\|readinessProbe:'
```
Expected: lint passes with no errors; the rendered template shows
`path: /livez` under `livenessProbe` and `path: /readyz` under
`readinessProbe`.

- [ ] **Step 3: Commit**

```bash
git add charts/obs-exporter/values.yaml
git commit -m "feat(chart): default probes to /livez and /readyz, not /health

A fresh install with no probe overrides no longer wires liveness and
readiness to an endpoint that answers 503 on any single cluster going
unreachable -- the exact trap docs/deployment/kubernetes.md has told
operators to work around by hand.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: ADR-0013 and the ADR index

**Files:**
- Create: `docs/adr/0013-static-liveness-readiness-probes.md`
- Modify: `docs/adr/index.md`

**Interfaces:**
- Consumes: the behavior Tasks 1-2 shipped (`/livez`/`/readyz` always 200,
  chart defaults repointed) — describe it as already-shipped fact, this task
  does not re-derive it.

- [ ] **Step 1: Write the ADR**

Create `docs/adr/0013-static-liveness-readiness-probes.md`:

```markdown
# Static `/livez` and `/readyz`, decoupled from cluster state

## Status

Accepted (2026-07-31). Additive; ships alongside the Flux live-validation
work. Does not supersede a prior ADR — the chart's previous `/health`-for-both
probe wiring was never itself a recorded decision, just an unexamined
default.

## Context

`/health` (unchanged by this ADR) answers 503 while any configured cluster is
unreachable, and 200 otherwise, with a JSON body describing every cluster's
status. That is the right signal for a human, a monitoring probe reading the
body, or an alerting rule. It is the wrong signal for a Kubernetes liveness or
readiness probe, and the chart shipped with both wired to it anyway
(`charts/obs-exporter/values.yaml`, prior to this change).

As a *readiness* check — the sort that decides whether traffic reaches this
pod — `/health` is defensible on one cluster and on several: pulling a
degraded instance out of rotation is reasonable. As a *liveness* check — the
sort that restarts the process — it is wrong on one cluster and worse on
several: no restart makes an unreachable cluster reachable, and with several
clusters a restart additionally drops every metric from every cluster that
was collecting fine. `docs/deployment/kubernetes.md` and
`docs/operate/troubleshooting.md` already carried this argument and told
operators to override the chart's `livenessProbe` to `/metrics` by hand — the
fix here is to stop needing the override.

Proposed by Benjamin (see ADR-0011 for context on this contact) alongside the
2026-07-31 Flux capture: probes must not depend on cluster state at all,
collapsing liveness and readiness into one check for this exporter, since
neither should ever fail for a reason a restart or a pool removal can fix.

## Decision

Two new endpoints, `/livez` and `/readyz`, each answering `200 OK` with a
fixed `ok` body — no cluster state, no `SnapshotStore` read, nothing that can
make either fail once the process is running. Both are registered before the
first collection cycle starts, alongside `/health` (ADR-0002: the HTTP server
starts serving before the first cycle so a slow login or poll doesn't look
like a dead exporter) — so unlike `/health`, they have no startup window to
wait out either.

The chart's `livenessProbe` and `readinessProbe` defaults now point at
`/livez` and `/readyz` respectively. `/health` is unchanged: same path, same
200/503 behavior, same JSON body, still the right endpoint for a human or an
alerting rule that wants to know which cluster is degraded.

No `startupProbe` is added. The chart didn't define one before this change,
and `/livez`/`/readyz` have no startup window to cover, unlike the `/health`
based probes they replace.

## Consequences

- A fresh `helm install` with no probe overrides now gets correct behavior by
  default. Anyone who already overrode `livenessProbe` to `/metrics` per the
  prior docs advice is unaffected — their override still works, it's just no
  longer necessary.
- `/health`'s consumers — anything scripting against its status code or body
  today — see no change.
- Alerting on cluster reachability still means `ecs_up` and
  `ecs_collector_up`, or reading `/health`'s JSON body directly. `/livez` and
  `/readyz` will never reveal a degraded cluster; that was never their job.

## Related

- [ADR-0002](0002-prometheus-snapshot-model.md) — the snapshot model this
  reuses: server up before the first cycle.
- [ADR-0011](0011-flux-collector-for-unreachable-metrics.md) — prior context
  on Benjamin as the live-cluster channel.
- `docs/deployment/kubernetes.md` §Probes and
  `docs/operate/troubleshooting.md` §Checking health without scraping —
  operator-facing guidance, updated in the same change.
```

- [ ] **Step 2: Add the index row**

In `docs/adr/index.md`, after the `[0012]` row, add:

```markdown
| [0013](0013-static-liveness-readiness-probes.md) | `/livez` and `/readyz`, always-200 probe endpoints decoupled from cluster state; chart defaults repointed away from `/health` |
```

- [ ] **Step 3: Verify the docs build**

Run: `uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict`
Expected: exit 0, no broken nav or link warnings for the new page.

- [ ] **Step 4: Commit**

```bash
git add docs/adr/0013-static-liveness-readiness-probes.md docs/adr/index.md
git commit -m "docs(adr): record the /livez /readyz decision as ADR-0013

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Operator docs and CHANGELOG

**Files:**
- Modify: `docs/deployment/kubernetes.md` (the `## Probes` section)
- Modify: `docs/operate/troubleshooting.md` (the "Checking health without
  scraping" section and the "`/metrics` returns 503" symptom entry)
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: the shipped behavior from Tasks 1-3. Purely descriptive changes,
  no code.

- [ ] **Step 1: Rewrite `docs/deployment/kubernetes.md`'s Probes section**

Replace this exact block (currently the section starting at "## Probes" and
running through the `ecs_up`/`ecs_collector_up` paragraph):

```markdown
## Probes

The image is distroless — it contains the exporter binary and nothing else, no
shell and no `curl` — so a probe that runs a command inside the container has
nothing to run. Every probe has to be an HTTP request, which the kubelet — the
Kubernetes agent running on each node — makes from outside the container.

Which endpoint each probe uses is a real decision, not a formality: use
`/metrics` for liveness and `/health` for readiness. `/health` answers 503 while
**any** configured cluster is failing, and a restart cannot make an unreachable
cluster reachable — which holds with a single cluster just as much as with
several, where the restart additionally drops the metrics for every cluster that
was collecting perfectly well. `/metrics` answers 200 as long as the process is
up and serving, which is what liveness is supposed to mean. [Verify and
troubleshoot](../operate/troubleshooting.md#checking-health-without-scraping)
sets out the full argument and what the `/health` JSON body contains.

The chart ships with both probes pointing at `/health`, so override the liveness
one:

```yaml
livenessProbe:
  httpGet:
    path: /metrics
    port: http
```

Cover the startup window as well, with `initialDelaySeconds` or a
`startupProbe`. The exporter's HTTP server deliberately comes up before the
first collection cycle finishes, so there is a real window in which `/health`
answers 503 and `/metrics` carries only `obs_exporter_build_info` — long enough
to fail a readiness probe and, without a delay, to restart a pod that is
starting normally. Its length is bounded by `collection.timeout`, 60 seconds by
default.

Then alert on `ecs_up` and `ecs_collector_up` rather than on either probe. The
exporter is built to degrade per cluster and per collector instead of going
dark, and those two metrics are the ones that say which part is degraded.
```

with:

```markdown
## Probes

The image is distroless — it contains the exporter binary and nothing else, no
shell and no `curl` — so a probe that runs a command inside the container has
nothing to run. Every probe has to be an HTTP request, which the kubelet — the
Kubernetes agent running on each node — makes from outside the container.

The chart's default `livenessProbe` and `readinessProbe` point at `/livez` and
`/readyz`. Both always answer 200 — neither depends on cluster state or on the
collection cycle having run at all, so neither can restart or de-pool a pod
over a cluster that happens to be unreachable, which no restart could fix
anyway. [ADR-0013](../adr/0013-static-liveness-readiness-probes.md) has the
full argument. No override is needed for a standard deployment.

`/health` still exists, unchanged: it answers 503 while any configured cluster
is failing, and 200 otherwise, with a JSON body naming every cluster's status.
It is not what the chart's probes use, but it is still the right endpoint for
a human checking in, or for a monitoring system that wants to know *which*
cluster is degraded rather than just that the pod should stay in rotation.
[Verify and troubleshoot](../operate/troubleshooting.md#checking-health-without-scraping)
covers it in full.

Because `/livez` and `/readyz` don't wait on the first collection cycle, there
is no startup window to cover with `initialDelaySeconds` or a `startupProbe` —
unlike `/health`, which answers 503 until that first cycle finishes (bounded
by `collection.timeout`, 60 seconds by default).

Alert on `ecs_up` and `ecs_collector_up` rather than on any probe. The
exporter is built to degrade per cluster and per collector instead of going
dark, and those two metrics are the ones that say which part is degraded.
```

- [ ] **Step 2: Update `docs/operate/troubleshooting.md`'s health section**

Find this paragraph (in the "Checking health without scraping" section, after
the "As a *readiness* check..." paragraph and its `curl`/JSON example):

```markdown
The path `/health` is fixed. Only the metrics path is configurable, through
`server.uri` in the config file, so on a deployment that has moved `/metrics`
elsewhere `/health` has not moved with it.
```

Replace it with:

```markdown
The path `/health` is fixed. Only the metrics path is configurable, through
`server.uri` in the config file, so on a deployment that has moved `/metrics`
elsewhere `/health` has not moved with it.

Two more fixed paths, `/livez` and `/readyz`, always answer 200 — no cluster
state, no dependency on the collection cycle. The chart's default
`livenessProbe` and `readinessProbe` use these, not `/health`
([ADR-0013](../adr/0013-static-liveness-readiness-probes.md)). Probing either
one to check on a cluster will never show a problem; that was never their
job, and asking them the question `/health` or `ecs_up` answers is the
mistake to look for if a probe stays green through an outage a dashboard is
showing.
```

- [ ] **Step 3: Update the `/metrics` returns 503 symptom entry**

Find this line in the "`/metrics` returns 503" section:

```markdown
```bash
curl -s -o /dev/null -w '%{http_code} %{url_effective}\n' localhost:9438/metrics
curl -s localhost:9438/health | jq '.clusters[] | select(.ok == false)'
```

If the first prints 200 and the second prints a cluster, your probe is hitting
`/health`. Fix the probe, or fix the cluster it is complaining about. Remember
that `/metrics` may have been moved by `server.uri` while `/health` has not.
```

Replace the last paragraph with:

```markdown
If the first prints 200 and the second prints a cluster, your probe is hitting
`/health`. Fix the probe, or fix the cluster it is complaining about — the
chart's own probes no longer hit `/health` by default
([ADR-0013](../adr/0013-static-liveness-readiness-probes.md)), so seeing this
usually means a manual override predating that change. Remember that
`/metrics` may have been moved by `server.uri` while `/health`, `/livez` and
`/readyz` have not.
```

- [ ] **Step 4: Add the CHANGELOG entry**

Under `## [Unreleased]` in `CHANGELOG.md` (create the section if the file
currently has none — check the top of the file first), add:

```markdown
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
```

If `## [Unreleased]` already has `### Added` and/or `### Changed`
subsections from other work, append these bullets under the existing
subsections rather than creating duplicates.

- [ ] **Step 5: Verify the docs build**

Run: `uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict`
Expected: exit 0.

- [ ] **Step 6: Run the full gate**

Run: `make ci`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add docs/deployment/kubernetes.md docs/operate/troubleshooting.md CHANGELOG.md
git commit -m "docs: point probe guidance at /livez /readyz, retire the manual override

kubernetes.md's Probes section and troubleshooting.md's health-check
docs told operators to override the chart's liveness probe by hand.
That advice is now obsolete -- the chart does it correctly by default.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage.** Architecture (handlers + registration point) → Task 1.
Chart (default probe paths, no startupProbe) → Task 2. Docs impact's ADR
bullet → Task 3. Docs impact's kubernetes.md/troubleshooting.md/CHANGELOG
bullets → Task 4. Testing section's `main_test.go` requirement → Task 1's
Step 1. Testing section's `helm lint`/`helm template` requirement → Task 2's
Step 2. Testing section's `mkdocs build --strict` requirement → Task 3's Step
3 and Task 4's Step 5. Compatibility section (no `/health` change, chart
version auto-tracks the app tag) → asserted as a Global Constraint and
reflected in Task 4's CHANGELOG wording. README.md is explicitly out of
scope per the spec — no task touches it.

**Type consistency.** `staticOKHandler(w http.ResponseWriter, _ *http.Request)`
is defined once in Task 1 and referenced by name only in Task 1's own test —
no later task calls it directly, so there's no signature drift to check
across tasks.

**Placeholder scan.** No TBD/TODO. Every doc-editing step quotes the exact
current text (verified against the live files during design) and the exact
replacement text — no "update similarly" steps. Task 4's CHANGELOG step
includes a conditional (append vs. create subsections) because the file's
exact `## [Unreleased]` state at execution time is not something this plan
can freeze in advance; the instruction is concrete either way, not vague.

**Task ordering.** Task 2 (chart) doesn't depend on Task 1's Go code existing
to lint or render correctly — chart YAML is independent of `main.go` — but
running them in order keeps the story straight for reviewers and matches the
spec's own section order. Task 3 (ADR) references "already shipped" behavior
from Tasks 1-2 in its prose, so it must follow them. Task 4 links to the ADR
Task 3 creates, so it must follow Task 3.
