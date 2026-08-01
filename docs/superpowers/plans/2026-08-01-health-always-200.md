# /health always-200 (obs_exporter) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `/health` in obs_exporter always answers `200 OK`. The JSON body's
per-cluster `ok`/`err` fields remain the informational signal; the HTTP
status code stops duplicating that signal.

**Architecture:** Delete the `if !healthy { w.WriteHeader(http.StatusServiceUnavailable) }`
branch in `healthHandler` (`main.go:292-316`). No new types, no new state.
`/livez`/`/readyz` are untouched — they already always answer 200 (ADR-0013).

**Tech Stack:** Go, `net/http`, `net/http/httptest` for tests.

## Global Constraints

- Repo: `/Users/fjacquet/Projects/obs_exporter`.
- Spec: `docs/superpowers/specs/2026-08-01-family-health-endpoint-design.md`.
- `/health`'s path and JSON body shape (`built_at`, `clusters: [{cluster, ok, last_scrape, err}]`) do not change — only the status code.
- Not a breaking change (JSON shape unchanged) — call this out explicitly in CHANGELOG, don't imply it's breaking.
- Next ADR number is 0014.

---

### Task 1: Drop the 503 branch in `healthHandler`

**Files:**
- Modify: `main.go:292-316` (function `healthHandler`)
- Test: `main_test.go` (new tests, file already exists with `TestLivezReturnsOK`/`TestReadyzReturnsOK` at lines 9 and 23)

**Interfaces:**
- Consumes: `ecs.SnapshotStore` (`internal/ecs/snapshot.go:69-92`) — `Load() *Snapshot`. `ecs.Snapshot` (`internal/ecs/snapshot.go:19-22`): `BuiltAt time.Time`, `Clusters []*ClusterSnapshot`. `ecs.ClusterSnapshot` (`internal/ecs/snapshot.go:10-16`): `Cluster string`, `LastScrape time.Time`, `OK bool`, `Err string`, `Samples []Sample`.
- Produces: `healthHandler(w http.ResponseWriter, store *ecs.SnapshotStore)` — signature unchanged, only body behavior changes.

- [ ] **Step 1: Write the failing test — unhealthy cluster still returns 200**

Add to `main_test.go`:

```go
func TestHealthReturns200WhenClusterUnhealthy(t *testing.T) {
	store := ecs.NewSnapshotStore()
	store.Store(&ecs.Snapshot{
		BuiltAt: time.Now(),
		Clusters: []*ecs.ClusterSnapshot{
			{Cluster: "ecs-dr-02", OK: false, Err: "all 6 collectors failed: login GET: status 401"},
		},
	})

	rec := httptest.NewRecorder()
	healthHandler(rec, store)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body struct {
		Clusters []struct {
			Cluster string `json:"cluster"`
			OK      bool   `json:"ok"`
			Err     string `json:"err"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Clusters) != 1 || body.Clusters[0].OK {
		t.Fatalf("clusters = %+v, want one cluster with ok=false", body.Clusters)
	}
	if body.Clusters[0].Err == "" {
		t.Fatalf("err field empty, want the collector failure message")
	}
}

func TestHealthReturns200WhenNoClusters(t *testing.T) {
	store := ecs.NewSnapshotStore()

	rec := httptest.NewRecorder()
	healthHandler(rec, store)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
```

Add the needed imports to `main_test.go`'s existing import block (`encoding/json`, `time`, and the module's `ecs` package — check `main.go`'s own import path for `internal/ecs`, e.g. `"github.com/<org>/obs_exporter/internal/ecs"`, and use the same import string).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./... -run TestHealthReturns200 -v`
Expected: `TestHealthReturns200WhenClusterUnhealthy` FAILs with `status = 503, want 200`. `TestHealthReturns200WhenNoClusters` FAILs the same way.

- [ ] **Step 3: Remove the 503 branch**

In `main.go`, `healthHandler` currently ends:

```go
	w.Header().Set("Content-Type", "application/json")
	if !healthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(out)
```

Change to:

```go
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
```

The `healthy` local variable (`main.go:304`, `healthy := len(snap.Clusters) > 0`) and the loop that sets `healthy = false` on any unhealthy cluster (`main.go:307-309`) are now unused — delete them too, along with the now-dead `healthy` tracking, since nothing reads it anymore. The per-cluster `OK`/`Err` fields already carry the same information in the body.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -run TestHealthReturns200 -v`
Expected: both PASS.

- [ ] **Step 5: Run the full test suite**

Run: `go test ./...`
Expected: all PASS, including the existing `TestLivezReturnsOK`/`TestReadyzReturnsOK`.

- [ ] **Step 6: Commit**

```bash
git add main.go main_test.go
git commit -m "fix: /health always answers 200, never 503

Cluster-down is data the exporter reports, not a failure of the
exporter itself. The JSON body's per-cluster ok/err fields already
carry the signal; the status code stops duplicating it. Follow-up to
ADR-0013's /livez /readyz argument, applied to /health itself."
```

---

### Task 2: ADR-0015 + docs updates

**Files:**
- Create: `docs/adr/0015-health-always-200.md`
- Modify: `docs/adr/index.md:53` (append row after 0013)
- Modify: `docs/deployment/kubernetes.md:141-145`
- Modify: `docs/operate/troubleshooting.md:257-292`, `docs/operate/troubleshooting.md:294-303`, `docs/operate/troubleshooting.md:508-536`
- Modify: `CHANGELOG.md` (new entry above `## [3.4.0]`)

**Interfaces:**
- Consumes: nothing (docs-only task).
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Write ADR-0015**

Create `docs/adr/0015-health-always-200.md`:

```markdown
# `/health` always answers 200

## Status

Accepted (2026-08-01). Additive; follows ADR-0013. Does not supersede it —
ADR-0013 covered `/livez` and `/readyz`, this covers `/health` itself.

## Context

ADR-0013 established that an exporter is a probe: "cluster unreachable" is
data it reports, not a failure of the exporter process, and no HTTP endpoint
should encode that fact as a non-200 status where something downstream might
treat the exporter as down. It applied that argument to `/livez` and
`/readyz`. `/health` (`main.go`'s `healthHandler`) still answered 503 while
any configured cluster was unreachable — the same coupling, one level
removed, on the one endpoint documented for humans, dashboards, and manual
`curl` checks rather than for Kubernetes probes specifically.

Nothing in the chart wires `/health` to a probe anymore (ADR-0013 fixed
that), but the 503 remained a trap for anything else that treats a non-200
response as "exporter is down" rather than "exporter is telling you a
cluster is down" — a monitoring script's health check, a load balancer's
passive health check, a dashboard's uptime tile.

## Decision

`healthHandler` (`main.go`) always writes `200 OK`. The JSON body is
unchanged: `built_at`, and `clusters: [{cluster, ok, last_scrape, err}]` per
configured cluster. The per-cluster `ok`/`err` fields are now the only
channel for "which cluster is down and why" — the status code no longer
duplicates that signal, and nothing that reads the body loses information.

## Consequences

- Anything that gated on `/health`'s HTTP status code (rather than parsing
  the body) now sees 200 unconditionally and must read `ok`/`err` per
  cluster instead. Not a breaking change to the JSON shape — the fields
  were already there.
- `docs/deployment/kubernetes.md` and `docs/operate/troubleshooting.md`
  updated to stop describing `/health` as ever answering 503.
- Alerting guidance is unchanged: alert on `ecs_up`/`ecs_collector_up`, not
  on any HTTP status code, per ADR-0013 and the CLAUDE.md family standard.
```

- [ ] **Step 2: Add the ADR to the index**

In `docs/adr/index.md`, after line 53 (`| [0013] ... |`), add:

```markdown
| [0015](0015-health-always-200.md) | `/health` always answers 200; the JSON body's per-cluster `ok`/`err` fields are the only status channel |
```

- [ ] **Step 3: Update `docs/deployment/kubernetes.md`**

Replace lines 141-145:

```markdown
`/health` still exists, unchanged: it answers 503 while any configured cluster
is failing, and 200 otherwise, with a JSON body naming every cluster's status.
It is not what the chart's probes use, but it is still the right endpoint for
a human checking in, or for a monitoring system that wants to know *which*
cluster is degraded rather than just that the pod should stay in rotation.
```

with:

```markdown
`/health` still exists and always answers 200, with a JSON body naming every
cluster's status (`ok`/`err` per cluster). It is not what the chart's probes
use, but it is still the right endpoint for a human checking in, or for a
monitoring system that wants to know *which* cluster is degraded — read the
body, not the status code ([ADR-0015](../../adr/0015-health-always-200.md)).
```

Line 151 (`unlike /health, which answers 503 until that first cycle finishes`)
is now false — replace the sentence spanning lines 149-152:

```markdown
Because `/livez` and `/readyz` don't wait on the first collection cycle, there
is no startup window to cover with `initialDelaySeconds` or a `startupProbe` —
unlike `/health`, which answers 503 until that first cycle finishes (bounded
by `collection.timeout`, 60 seconds by default).
```

with:

```markdown
Because `/livez` and `/readyz` don't wait on the first collection cycle, there
is no startup window to cover with `initialDelaySeconds` or a `startupProbe`.
`/health`'s body reports an empty `clusters` array until that first cycle
finishes (bounded by `collection.timeout`, 60 seconds by default), but its
status code is 200 throughout.
```

- [ ] **Step 4: Update `docs/operate/troubleshooting.md` — main health section**

Replace lines 257-260:

```markdown
The exporter serves `/health` alongside `/metrics`. It answers JSON describing
every configured cluster, and it sets the HTTP status code so that something
which cannot read JSON — a container health check, a load balancer, a monitoring
probe — still gets a usable answer:
```

with:

```markdown
The exporter serves `/health` alongside `/metrics`. It answers JSON describing
every configured cluster and always answers `200 OK`
([ADR-0015](../../adr/0015-health-always-200.md)) — read the body's `ok`/`err`
fields per cluster, don't gate on the status code:
```

Replace lines 278-292 (the "status code is 200 only when..." paragraph) with:

```markdown
The status code is always **200**, whether every configured cluster is
healthy or not — that was the mistake ADR-0013 fixed for `/livez`/`/readyz`
and ADR-0015 fixed for `/health` itself: a cluster being unreachable is data
the exporter reports, not a failure of the exporter. Read the JSON body's
per-cluster `ok` and `err` fields to find out which cluster, if any, is
degraded and why. Alert on `ecs_up` per cluster (or on `ok`/`err` in this
body), not on `/health`'s HTTP status.
```

Replace lines 294-299 (the "`/health` also answers 503 before the first
collection cycle" sentence) with:

```markdown
`/health`'s body reports an empty `clusters` array before the first
collection cycle finishes, because at that point it knows about no clusters
at all — but its status code is 200 throughout, including during that
startup window. The HTTP server deliberately starts before the first cycle —
logging in to every cluster and polling it can take longer than a scrape
timeout, and a blocked `/metrics` looks like a dead process — so there is a
real window at startup where `/metrics` answers with only
`obs_exporter_build_info` and `/health`'s `clusters` array is empty. Its
length is bounded by `collection.timeout` (60 seconds by default), not by
`collection.interval`: clusters are polled in parallel, and the timeout is
the per-cluster budget within one cycle.
```

- [ ] **Step 5: Update `docs/operate/troubleshooting.md` — `/metrics` returns 503 symptom entry**

Replace lines 510-514:

```markdown
**What it means.** You are probing `/health`, not `/metrics`. It is an easy
mistake to make through a proxy or a health-check configuration that rewrites the
path, and the two endpoints disagree by design: `/health` answers 503 while any
single configured cluster is failing, whereas `/metrics` keeps answering 200 with
whatever it has, including `ecs_up 0` for the cluster that is down.
```

with:

```markdown
**What it means.** `/metrics` has no failure mode that produces a 503 — see
below. If something reports a 503 "from the exporter," it is either probing
the wrong path entirely, or hitting something in front of the exporter (a
proxy, an ingress) that is itself unreachable. `/health` cannot be the source
of a 503 either, as of [ADR-0015](../../adr/0015-health-always-200.md) — it
always answers 200.
```

Replace lines 526-536 (the `curl` block and the paragraph after it):

```bash
curl -s -o /dev/null -w '%{http_code} %{url_effective}\n' localhost:9438/metrics
curl -s localhost:9438/health | jq '.clusters[] | select(.ok == false)'
```

If the first does not print 200, the exporter itself is unreachable —
check the process, the port, and anything in front of it. The second is
diagnostic regardless of the first's result: it lists any cluster currently
reporting unhealthy, straight from `/health`'s body, which always answers
200 ([ADR-0015](../../adr/0015-health-always-200.md)) — the status code will
never point you at a degraded cluster, the body will.
```

(Keep this as one fenced bash block followed by the prose paragraph, matching the surrounding file's existing style — don't literally nest a bash fence inside the replacement text above; write the bash block and the paragraph as separate elements in the file.)

- [ ] **Step 6: CHANGELOG entry**

Insert above `## [3.4.0] - 2026-07-31` in `CHANGELOG.md` a new unreleased entry:

```markdown
## [Unreleased]

### Changed

- `/health` always answers 200, never 503. The JSON body's per-cluster
  `ok`/`err` fields are unchanged and remain the way to tell whether a
  cluster is degraded — read the body, not the status code. See ADR-0015.
  Not a breaking change: the path and JSON shape are unchanged.

```

- [ ] **Step 7: Build docs**

Run: `mkdocs build --strict` (from repo root, if `mkdocs.yml` present)
Expected: exits 0, no broken nav/link errors from the new ADR file.

- [ ] **Step 8: Commit**

```bash
git add docs/adr/0015-health-always-200.md docs/adr/index.md \
  docs/deployment/kubernetes.md docs/operate/troubleshooting.md CHANGELOG.md
git commit -m "docs: record ADR-0015, update probe/health docs for always-200 /health"
```
