# Documentation Restructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure the mkdocs documentation set around the order a storage sysadmin works, so every page has one correct home and no fact lives in two places.

**Architecture:** Eight tasks, each moving or writing one group of pages and updating `mkdocs.yml` for its own additions, so every commit leaves the site buildable. No code changes anywhere — `internal/`, `cmd/`, `charts/` and `grafana/` are untouched. The gate for every task is `mkdocs build --strict`, which fails on broken internal links and is what makes a one-pass restructure safe.

**Tech Stack:** mkdocs-material, `pymdownx.superfences` with a mermaid custom fence, published to GitHub Pages by `.github/workflows/docs.yml`.

**Spec:** `docs/superpowers/specs/2026-07-30-documentation-restructure-design.md`

## Global Constraints

- **The reader is a storage sysadmin.** Assume Linux, systemd, networking, TLS, package management, YAML, reading logs, and containers as a concept. Do **not** assume Kubernetes objects and idioms, Helm, PromQL, or OTLP.
- **Define cloud-native vocabulary in a clause at first use** — *OCI artifact*, *ClusterIP*, *operator*, *CRD*, *sidecar*. No glossary page.
- **Explain, don't just state.** Why before how for anything non-obvious; state the consequence of getting it wrong; prefer a worked, runnable example to an abstract statement; one idea per paragraph in full sentences, not bullet fragments.
- **Do not explain fundamentals.** Nobody needs to be told what a container or a YAML file is.
- **Nothing is deleted.** Every page that exists today survives, either in place or at an explicit destination named in this plan. Content is moved, not summarised away.
- **`mkdocs build --strict` must pass before every commit.** It is the only gate; there is no docs linter and none is being added.
- **Diagrams are mermaid**, in ```mermaid fences. `pymdownx.superfences` is already configured for it, and GitHub renders the same fence natively.
- **Work directly on `main`** and push after each task — the maintainer instructed this explicitly for documentation. No branch, no PR.
- **No code changes.** If a task appears to need one, stop and report.

## File Structure

| File | Responsibility | Task |
| --- | --- | --- |
| `docs/metrics/index.md` | The metric catalog. Moved from `docs/metrics.md`; keeps the `/metrics/` URL. | 1 |
| `docs/metrics/flux.md` | Operator guide for the opt-in Flux collector, split out of the catalog. | 1 |
| `docs/metrics/reading.md` | The Prometheus concepts needed to not misread this exporter. New. | 2 |
| `docs/getting-started/first-run.md` | Running it against a real cluster the first time. From Quick start. | 3 |
| `docs/demo.md` | The no-hardware Compose stack. From Quick start. | 3 |
| `docs/operate/troubleshooting.md` | The diagnostic chapter, built around `--once --debug --trace`. | 4 |
| `docs/operate/upgrading.md` | Which migration guide applies, and what upgrading involves. | 5 |
| `docs/adr/index.md` | Explains what ADRs are and which ones answer operator questions. Rewritten. | 6 |
| `README.md` | Decision support only: what it is, does it fit, where to learn more. | 7 |
| `docs/deployment/docker.md`, `docs/deployment/kubernetes.md` | Voice pass for the sysadmin audience. | 8 |
| `mkdocs.yml` | Nav, updated by each task for its own pages. | 1–8 |

Deleted at the end of Task 3: `docs/getting-started/quickstart.md`, once all three of its jobs have homes.

---

### Task 1: Split the metrics reference

**Files:**
- Move: `docs/metrics.md` → `docs/metrics/index.md`
- Create: `docs/metrics/flux.md`
- Modify: `mkdocs.yml`, `README.md:74`, `docs/index.md:67`, `docs/migration-v3.md:175`, `docs/getting-started/configuration.md:107-108`, `docs/getting-started/installation.md:49`

**Interfaces:**
- Produces: the URL `/metrics/` (unchanged), plus `/metrics/flux/`. Later tasks link to `../metrics/index.md` and `../metrics/flux.md`.

- [ ] **Step 1: Move the file with git so history follows it**

```bash
mkdir -p docs/metrics
git mv docs/metrics.md docs/metrics/index.md
```

`docs/metrics.md` and `docs/metrics/index.md` both publish as `/metrics/`, so no
bookmark or GitHub-issue link breaks. Only in-repo relative links change.

- [ ] **Step 2: Fix the links that now point one level too high**

`docs/metrics/index.md` sits one directory deeper, so its own relative links
must gain a `../`. Two lines reference ADR-0011:

```bash
sed -i '' 's|](adr/|](../adr/|g' docs/metrics/index.md
grep -n '](\.\./adr/' docs/metrics/index.md   # expect 2 hits
```

- [ ] **Step 3: Cut the Flux section into its own page**

`docs/metrics/index.md` currently ends with `## Flux collector (opt-in, collectFlux: true)` at line 204. Move that section — heading and everything below it — into a new `docs/metrics/flux.md`, promoting the heading to `# The Flux collector`.

The new page keeps every fact from that section: the `collectFlux` flag, the `SYSTEM_MONITOR`/`SYSTEM_ADMIN` requirement, that it shares the management port and session so it needs no extra network access, all four bucket mapping tables, the rate-versus-counter guidance, the sole-source arbitration note over the three per-node names, and the "Two names, one measurement: cluster throughput" admonition.

Add an opening paragraph the catalog never had, stating in plain terms what this collector is for: ObjectScale 4.3 dashboard payloads omit per-node performance fields the API reference documents, and the directory-table stats live on a network a segmented cluster does not route. This collector reads both from the cluster's own monitoring store instead.

In `docs/metrics/index.md`, replace the removed section with a short pointer to the new page.

- [ ] **Step 4: Update every inbound link**

| File:line | Change |
| --- | --- |
| `mkdocs.yml:30` | nav entry becomes the Metrics group — see Step 5 |
| `README.md:74` | `[docs/metrics.md](docs/metrics.md)` → `[docs/metrics/](docs/metrics/index.md)` |
| `docs/index.md:67` | `[Metrics reference](metrics.md)` → `(metrics/index.md)` |
| `docs/migration-v3.md:175` | `[Metrics](metrics.md)` → `[Metrics](metrics/index.md)` |
| `docs/getting-started/configuration.md:107` | `../metrics.md#node-dt-opt-in-collectdt-true` → `../metrics/index.md#node-dt-opt-in-collectdt-true` |
| `docs/getting-started/configuration.md:108` | `../metrics.md#flux-collector-opt-in-collectflux-true` → `../metrics/flux.md` |
| `docs/getting-started/installation.md:49` | `../metrics.md#node-dt-opt-in-collectdt-true` → `../metrics/index.md#node-dt-opt-in-collectdt-true` |

Use `.md` paths rather than URL paths: mkdocs rewrites them, and they also work when the file is read on GitHub.

- [ ] **Step 5: Update the nav**

In `mkdocs.yml`, replace `- Metrics reference: metrics.md` with:

```yaml
  - Metrics:
      - Reference: metrics/index.md
      - The Flux collector: metrics/flux.md
```

(`Reading the metrics` joins this group in Task 2.)

- [ ] **Step 6: Verify**

```bash
uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict
grep -rn "metrics\.md" --include="*.md" --include="*.yml" . | grep -v superpowers | grep -v CHANGELOG | grep -v '^./site'
```

Expected: build succeeds; the grep returns only `CLAUDE.md` (Task 7 handles it) and prose mentions inside `docs/adr/`, which Task 6 handles. Any *link* still pointing at `metrics.md` is a miss.

- [ ] **Step 7: Commit and push**

```bash
git add -A docs mkdocs.yml README.md
git commit -m "docs(metrics): split the reference and the Flux operator guide

The catalog and the Flux collector guide are different documents doing
different jobs; at 323 lines they were one file. The move to metrics/index.md
keeps the published /metrics/ URL identical, so nothing external breaks."
git push origin main
```

---

### Task 2: Write "Reading the metrics"

**Files:**
- Create: `docs/metrics/reading.md`
- Modify: `mkdocs.yml`, `docs/metrics/index.md` (link to it from the top)

**Interfaces:**
- Consumes: the Metrics nav group from Task 1.
- Produces: `/metrics/reading/`, linked from Task 4's troubleshooting page.

- [ ] **Step 1: Write the page**

This is new content, not moved content, and it is load-bearing: 3.2.0 shipped the project's first counters, and the catalog tells the reader one metric "must be `rate()`d" and another "must never be" without explaining either.

Cover exactly these four things, in this order, and nothing else:

**1. Gauges and counters.** Most metrics here are gauges — a reading at a moment, which can go up or down, like used bytes or a node's CPU percentage. A few are counters: they only ever increase, and they reset to zero when the process producing them restarts. Names ending `_total` are counters. A counter's raw value is close to meaningless on its own; what you want is how fast it is climbing, which is what `rate()` gives you. Show the contrast with real metrics from this exporter:

```promql
rate(ecs_node_requests_total[5m])     # requests per second, from a counter
ecs_cluster_requests_per_second       # already per second — do NOT wrap in rate()
```

State the consequence plainly: wrapping an already-averaged rate in `rate()` produces a number that is not wrong so much as meaningless, and graphing a counter raw produces a line that climbs forever and tells you nothing.

**2. Why the reset matters.** ObjectScale's monitoring store restarts its counters when the datahead service restarts. `rate()` understands counter resets and handles them; a gauge-style graph of the same data shows a cliff that looks like an outage and is not one.

**3. Absent, never zero.** This exporter never invents a value. If a cluster does not report a field, or reports something unparseable, the metric is *absent* from `/metrics` rather than present as `0`. That is deliberate — a zero is a claim, and "the disk is 0% full" and "we could not read the disk" are very different statements. Point out the practical consequence: a missing metric usually means the cluster did not report it, and the way to find out is the trace workflow. Link to `../operate/troubleshooting.md`. Reference ADR-0007 at `../adr/0007-obs-4-1-api-alignment.md`.

**4. Scrape interval versus collection interval.** The exporter polls clusters on its own schedule (`collection.interval`, default 5 minutes) and serves whatever it last collected. Prometheus scraping more often than that gets the same numbers repeatedly — it costs the ObjectScale cluster nothing, which is the point of the design, but it does not make the data fresher. Set the scrape interval to match, and if you want fresher data, lower `collection.interval`.

Do **not** cover: PromQL as a language, recording rules, alerting syntax, or Grafana panel construction. Those duplicate upstream documentation and drift with Prometheus rather than with this exporter.

- [ ] **Step 2: Link it from the catalog**

Add a line near the top of `docs/metrics/index.md`, before the first table:

```markdown
New to Prometheus metrics, or unsure why one of these is missing?
[Reading the metrics](reading.md) covers gauges versus counters, why a metric
can be absent rather than zero, and how the collection interval relates to your
scrape interval.
```

- [ ] **Step 3: Add it to the nav**

```yaml
  - Metrics:
      - Reading the metrics: metrics/reading.md
      - Reference: metrics/index.md
      - The Flux collector: metrics/flux.md
```

Concepts first: someone who needs it needs it before the catalog, not after.

- [ ] **Step 4: Verify and commit**

```bash
uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict
git add docs/metrics/reading.md docs/metrics/index.md mkdocs.yml
git commit -m "docs(metrics): explain gauges, counters and absent samples

3.2.0 shipped the first counters in this exporter and the reference told the
reader to rate() one metric and never rate() another without explaining what
either means. Covers only what affects reading this exporter's output."
git push origin main
```

---

### Task 3: Split Quick start into First run and the demo

**Files:**
- Create: `docs/getting-started/first-run.md`, `docs/demo.md`
- Delete: `docs/getting-started/quickstart.md`
- Modify: `mkdocs.yml`, `docs/index.md:61`, `docs/deployment/docker.md:41`

**Interfaces:**
- Produces: `/getting-started/first-run/` and `/demo/`. Task 4 takes the diagnostic flags from the same source page.

- [ ] **Step 1: Create `docs/getting-started/first-run.md` from the real-cluster half**

Take `docs/getting-started/quickstart.md` lines 3–16 — the minimal `config.yaml` and the `obs_exporter --config` invocation — and build a page around them that reads as a first run rather than a snippet. It should walk through: what the smallest working config contains and why each field is there, starting the exporter in the foreground so you can watch it, what a healthy first cycle looks like in the log, and confirming with `curl -s localhost:9438/metrics | grep '^ecs_up'` that the cluster answered.

Mention `--once` here only as "there is a way to do a single cycle and exit, useful for checking connectivity — see Verify & troubleshoot", and link `../operate/troubleshooting.md`. The flags belong to that page; this page is about a successful first run.

End by pointing at the Deploy section, since the natural next question after a successful foreground run is how to run it as a service.

- [ ] **Step 2: Create `docs/demo.md` from the demo half**

Take `quickstart.md` lines 40–55 and expand. This page is the strongest thing the project offers a sysadmin evaluating it, and it currently sits at the bottom of a page they may never scroll to.

It must state: that `make demo` needs no ObjectScale hardware at all, what the stack contains (`mockecs` serving fixture payloads over self-signed TLS, the exporter, Prometheus, Grafana with dashboards already provisioned), that `make demo-ghcr` uses the published image instead of building from source, that Grafana is on <http://localhost:3000> with admin/admin, which dashboard to open first, and `make demo-down` to stop it.

Add what the old text omitted: the numbers in the demo come from fixtures, so they do not move the way a real cluster's would, and the point is to see the metric surface and the dashboards rather than to evaluate performance.

- [ ] **Step 3: Delete the old page and fix its inbound links**

```bash
git rm docs/getting-started/quickstart.md
```

- `docs/index.md:61` — the "New here?" paragraph points at Quick start for the demo. Point it at `demo.md`.
- `docs/deployment/docker.md:41` — links Quick start for the `--once --debug --trace` workflow. Point it at `../operate/troubleshooting.md`.

- [ ] **Step 4: Update the nav**

Replace the Quick start entry, and add the demo as a top-level entry:

```yaml
  - Getting started:
      - Install: getting-started/installation.md
      - Configure: getting-started/configuration.md
      - First run: getting-started/first-run.md
```

Add after the Deploy group (Task 4 inserts Operate between them):

```yaml
  - Try it without hardware: demo.md
```

- [ ] **Step 5: Verify and commit**

```bash
uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict
grep -rn "quickstart" --include="*.md" --include="*.yml" . | grep -v superpowers | grep -v '^./site'
```

Expected: build succeeds, grep returns nothing.

```bash
git add -A docs mkdocs.yml
git commit -m "docs: split Quick start into a first run and the demo stack

Quick start was doing three unrelated jobs. The no-hardware demo is the best
thing this project offers someone evaluating it and was buried at the bottom of
a page they may never scroll; it gets its own entry. The diagnostic flags move
to Verify & troubleshoot in the next commit."
git push origin main
```

---

### Task 4: The troubleshooting chapter

**Files:**
- Create: `docs/operate/troubleshooting.md`
- Modify: `mkdocs.yml`

**Interfaces:**
- Consumes: the diagnostic flags from the now-deleted `quickstart.md` — recover them from `git show HEAD~1:docs/getting-started/quickstart.md` if needed.
- Produces: `/operate/troubleshooting/`, linked from Tasks 2, 3 and 8.

This is the most important page in the restructure. The maintainer asked specifically that this material not be thinned out in the move. It grows.

- [ ] **Step 1: Carry over every flag, intact**

From the old Quick start: `--once`, `--debug`, `--trace`, the combined
`--once --debug --trace` invocation producing `samples.txt` and `trace.log`, and
`/health` returning per-cluster JSON with HTTP 503 when any cluster is failing.
Nothing here is dropped or shortened.

Expand each for a reader who has not used them:

- `--once` runs a single collection cycle, logs the result and exits. It is the fastest way to check that credentials and network reach a new cluster, and it does not start the HTTP server.
- `--debug` turns on verbose logging including per-collector failures. Combined with `--once` it also prints every collected sample, sorted, in exposition format — which you can diff against the reference.
- `--trace` logs every management API response body: method, path, status, payload. **The auth token is never logged**; the login response is skipped deliberately. Say this explicitly — a sysadmin should know before running it against production.

- [ ] **Step 2: Explain why the trace is the right tool for this exporter**

This is the part that makes the page worth reading, and it must not be compressed into a bullet.

This exporter never guesses a value. If ObjectScale returns a payload shaped differently from what a collector expects, the result is a *missing* metric, not a wrong one. That is the opposite of what someone expects coming from exporters that emit zeros on failure, and it is the most likely reason a reader arrives here: a panel is empty and nothing in the logs says why.

The trace is what distinguishes the two cases that look identical from outside — the cluster genuinely does not report that field, versus the exporter could not read what it sent. Note that this is not hypothetical: it is how the live-cluster evidence behind ADR-0008 was produced. Link it as `../adr/0008-swagger-4.2-validation-findings.md`.

- [ ] **Step 3: Write the symptom-first section**

Someone reaching this page mid-incident needs to find their symptom, not read a tour. Give each of these its own subheading, what it means, and what to run:

| Symptom | What it means |
| --- | --- |
| A metric is missing entirely | The cluster did not report the field, or reported it unparseably. Run `--once --debug` to see everything that *was* collected, then `--trace` to see what the cluster actually returned. |
| `ecs_up{cluster="..."} == 0` | No collector returned domain samples for that cluster this cycle — usually credentials, network, or TLS. `--once --debug` names the failing endpoint. |
| `ecs_collector_up{collector="..."} == 0` while others are 1 | One domain failed and the rest are fine; this is the exporter degrading per collector by design. Restarting will not help if the cause is a permission or a missing endpoint. |
| `collectFlux` on but no per-node Flux metrics | Check `ecs_collector_unmapped_nodes{collector="flux"}`. Above zero means Flux reported hosts that matched no node in the inventory, so those rows were dropped rather than attached to the wrong node. |
| `/metrics` returns 503 | You are probing `/health`, not `/metrics` — `/health` answers 503 while any single cluster is failing. |
| `/metrics` returns 500 | A duplicate series reached the registry. Report it: this is guarded against since 3.2.0 and should not happen. |

- [ ] **Step 4: Write the "sharing a trace" section**

New material, and it matters. A trace contains real payloads: namespace names, node names, IP addresses, cluster topology. Anyone attaching one to a GitHub issue should know that before they do.

Say what to strip, that identifier *consistency* must survive the sanitising or the trace stops being diagnosable (the same node must remain the same string throughout), and what a maintainer actually needs: the exporter version from `obs_exporter_build_info`, the ObjectScale version, the failing endpoint, and the payload the collector received.

- [ ] **Step 5: Add the Operate group to the nav**

Between Deploy and `Try it without hardware`:

```yaml
  - Operate:
      - Verify & troubleshoot: operate/troubleshooting.md
```

(`Upgrading` joins this group in Task 5.)

- [ ] **Step 6: Verify and commit**

```bash
uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict
git add docs/operate/troubleshooting.md mkdocs.yml
git commit -m "docs(operate): make the trace workflow a chapter, not a footnote

The --once --debug --trace workflow was the most useful operational content in
the docs and lived at the bottom of Quick start, which is not where anyone
looks mid-incident. Carries over every flag intact and adds what was missing:
why a missing metric rather than a wrong one is this exporter's failure mode,
a symptom-first index, and how to sanitise a trace before attaching it."
git push origin main
```

---

### Task 5: Upgrading

**Files:**
- Create: `docs/operate/upgrading.md`
- Modify: `mkdocs.yml`

- [ ] **Step 1: Write the page**

Short, and its job is routing. Someone upgrading needs to know which guide applies before they read a table of eighty metric renames.

State: that the project uses semantic versioning, so a major bump is the only kind that renames or removes metrics; that `migration-v2.md` covers v1 → v2 and `migration-v3.md` covers v2 → v3, linking both; and that 3.1 and 3.2 added metrics without removing or renaming any, so upgrading within 3.x needs no migration.

Add the practical advice the migration guides do not give: upgrade the exporter before updating dashboards and alerts, because both old and new names are visible in a scrape only if you run both versions — and you do not. The safe order is read the rename table, update the queries, then upgrade, and keep the old binary available until the dashboards are confirmed.

Point at the CHANGELOG for what changed in a specific release.

- [ ] **Step 2: Move the migration guides out of the nav**

They stay on disk and stay published — only their nav entries go. Remove these two lines from `mkdocs.yml`:

```yaml
  - Migrating from v1: migration-v2.md
  - Migrating to v3: migration-v3.md
```

and extend the Operate group:

```yaml
  - Operate:
      - Verify & troubleshoot: operate/troubleshooting.md
      - Upgrading: operate/upgrading.md
```

mkdocs does not require every page in the nav — `docs/superpowers/` already sits outside it and `--strict` passes. The build will list them as not-in-nav at INFO level, which does not fail the build.

- [ ] **Step 3: Verify and commit**

```bash
uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict
git add docs/operate/upgrading.md mkdocs.yml
git commit -m "docs(operate): route upgrades before the rename tables

Both migration guides sat above Deployment in the nav, prominent for readers
who by definition are not new. They stay published, reached through a page that
first says which one applies and in what order to do the upgrade."
git push origin main
```

---

### Task 6: The ADR index

**Files:**
- Modify: `docs/adr/index.md`, `mkdocs.yml`

- [ ] **Step 1: Rewrite `docs/adr/index.md` as a real page**

It is currently a bare table of twelve rows. It becomes the single nav entry standing in for all of them, so it has to orient a reader who does not know what an ADR is — without lecturing.

Open by saying what these records are: decisions with lasting consequences, written down with the reasoning and the alternatives that were rejected, kept even when later superseded. They exist so that a decision can be revisited on its merits rather than rediscovered by accident.

Then the part that earns a sysadmin's attention — a short "start here if you are running this" list, because three of them explain behaviour that otherwise looks like a bug:

- **ADR-0007** (`0007-obs-4-1-api-alignment.md`) — why a metric is absent rather than zero when a cluster does not report it.
- **ADR-0004** (`0004-token-auth-retry-policy.md`) — why the exporter logs out of every cluster on shutdown. ObjectScale caps auth tokens per user, and leaking sessions eventually locks the account out.
- **ADR-0011** (`0011-flux-collector-for-unreachable-metrics.md`) — why the Flux collector exists and why it is opt-in.

Write these as real markdown links in `adr/index.md`; the filenames are siblings there, so a bare filename resolves.

Keep the full table below that, unchanged in content.

- [ ] **Step 2: Correct the stale ADR-0011 row**

`docs/adr/index.md:15` still ends "implementation deferred". The collector shipped in 3.2.0. Change that clause to state it shipped, opt-in, in 3.2.0.

- [ ] **Step 3: Fix the `docs/metrics.md` path references in ADR prose**

Four lines in `docs/adr/0011-flux-collector-for-unreachable-metrics.md` (12, 124, 129, 143) and two in `docs/adr/0008-swagger-4.2-validation-findings.md` (32, 109) name `docs/metrics.md` in prose. That path no longer exists. Update each to `docs/metrics/` — and where the reference is specifically about the Flux mapping tables (0011 lines 124 and 129), to `docs/metrics/flux.md`.

These are prose mentions rather than links, so `--strict` will not catch them. Verify by eye.

- [ ] **Step 4: Collapse the ADR nav to one entry**

Replace the whole `Architecture decisions` block — the group heading and all thirteen child entries — with:

```yaml
  - Design decisions: adr/index.md
```

- [ ] **Step 5: Verify and commit**

```bash
uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict
grep -rn "docs/metrics\.md" docs/adr/
```

Expected: build succeeds, grep returns nothing.

```bash
git add docs/adr mkdocs.yml
git commit -m "docs(adr): one nav entry, and an index that orients an operator

Twelve of twenty-two nav entries were architecture decision records, so a
reader looking for how to deploy scanned mostly maintainer material. They stay
published and reachable; the index now says what they are and points at the
three that explain behaviour an operator would otherwise file as a bug. Also
corrects the 0011 row, which still said implementation was deferred."
git push origin main
```

---

### Task 7: Reduce the README

**Files:**
- Modify: `README.md`, `CLAUDE.md:71`

- [ ] **Step 1: Cut the README to decision support**

The README answers the question someone has on GitHub before committing to anything: what is this, does it fit my cluster, is it maintained, where do I learn more.

Keep: the badges; one paragraph of description; a plain statement of which ObjectScale and ECS versions it works against; a short capability summary of roughly six bullets naming metric *domains* rather than metric names; the install one-liner; the Development section; Lineage & license.

Replace with a sentence and a link, each pointing at the docs site:

| Section today | Becomes |
| --- | --- |
| `## Quick start` (line 23) | one line pointing at Getting started, plus the demo one-liner pointing at `docs/demo.md` |
| `## What it exports` (line 57) | the six-bullet domain summary, ending "Full catalog: docs/metrics/" |
| `## Configuration` (line 76) | two sentences — YAML with env interpolation, multi-cluster, hot reload — and a link to `docs/getting-started/configuration.md` |
| `## Node Exporter Full (Grafana 1860)` (line 99) | delete from the README; if the content is not already in the demo or metrics pages, move it to `docs/demo.md` rather than dropping it |

**Every fact must have exactly one home after this.** The README must not carry a metric list, a config example, or a flag table. That duplication is the mechanism by which `docs/index.md` went stale while the README gained a Flux section — two files both plausibly owned "what this exports", so updating one felt complete.

- [ ] **Step 2: Update the project rule in `CLAUDE.md`**

Line 71 reads "Update `docs/metrics.md` AND the Grafana dashboard when adding/renaming metrics." The path changed, and this session showed the rule was too narrow — the Flux collector was documented in the metrics reference while the getting-started pages, the README and the chart values went stale for a full release.

Rewrite it to say: update `docs/metrics/`, the Grafana dashboards, **and every operator-facing page that mentions a comparable flag**. Name the check that catches it — for a new per-cluster flag, `grep -rl collectDT` shows every file that documents the closest existing equivalent, and the new flag belongs in all of them.

- [ ] **Step 3: Verify and commit**

```bash
uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict
```

Read the rendered README on GitHub after pushing and confirm no section duplicates the docs site.

```bash
git add README.md CLAUDE.md
git commit -m "docs: make the README decision support, not documentation

The README and docs/index.md were two front doors with overlapping content and
no stated relationship, which is how one gained a Flux section and the other
did not. The README now answers only what someone asks before adopting; every
operational fact lives on the docs site, once. Widens the CLAUDE.md rule that
should have caught the gap: a new flag belongs everywhere its closest
equivalent is documented, which grep -rl collectDT enumerates."
git push origin main
```

---

### Task 8: Voice pass on the deployment pages

**Files:**
- Modify: `docs/deployment/docker.md`, `docs/deployment/kubernetes.md`

Both were written before the audience was established and read as devops-expert material. The facts in them are correct and verified — do not change any fact. Change how they are said.

- [ ] **Step 1: Rewrite `docs/deployment/kubernetes.md`**

The worse of the two. Unexplained terms to fix, each defined in a clause at first use rather than removed:

- "published to the GitHub Container Registry as an OCI artifact" — say that Helm installs directly from the registry with no chart repository to add first, which is the older workflow the reader may have seen elsewhere.
- "ClusterIP" — say the service is reachable inside the cluster only, and what that means for scraping.
- "Prometheus Operator `ServiceMonitor` or `ScrapeConfig`" — say these are configuration objects the Prometheus Operator watches to discover scrape targets, and that they are only useful if that operator is installed; otherwise point Prometheus at the service directly.
- "external secrets operator" — either explain it in a clause or drop the mention; do not leave it bare.

The calibration to match, from the spec:

> Every release publishes a Helm chart to GitHub's container registry, alongside the container image. Helm installs from it directly — there is no chart repository to add first, which is the older workflow you may have seen in other projects. The chart version always matches the exporter version it deploys, so `--version 3.2.0` gets you the chart for exporter 3.2.0 and you never have to look up which pairs with which.

- [ ] **Step 2: Pass over `docs/deployment/docker.md`**

Closer to right already. Check that the distroless explanation says *why* it matters to the reader (no `docker exec` to debug, no in-container `HEALTHCHECK`) rather than only stating the base image, and that the liveness-versus-readiness reasoning is spelled out rather than asserted.

- [ ] **Step 3: Verify and commit**

```bash
uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict
git add docs/deployment
git commit -m "docs(deployment): rewrite for a sysadmin, not a devops expert

Both pages were drafted before the audience was settled and leaned on
unexplained cloud-native vocabulary — OCI artifact, ClusterIP, ServiceMonitor,
external secrets operator. Same facts, defined at first use, with the reason a
reader should care stated before the command they should run."
git push origin main
```

---

## Self-Review

**Spec coverage.** Every spec decision maps to a task: the task-ordered site map is built incrementally across Tasks 1–6 with each task owning its nav lines; content migration is Tasks 1 and 3; the trace chapter is Task 4; *Reading the metrics* is Task 2; the ADR collapse is Task 6; the README split is Task 7; the voice rules bind every task through Global Constraints and are applied retroactively in Task 8. The mechanical work — the `metrics.md` move, the inbound links, the anchor that moves to the Flux page, the nav rewrite — is distributed across Tasks 1, 3 and 6 with explicit file:line references.

**Deliberate omissions.** No docs linter and no link checker beyond `--strict`, per the spec's out-of-scope section. No redirect plugin, because the one URL that would have moved does not. ADR contents are untouched apart from the two stale path references and the one stale status clause.

**Ordering constraint worth naming.** Task 3 deletes `quickstart.md`, and Task 4 needs its content. Task 4's Interfaces block says to recover it with `git show HEAD~1:docs/getting-started/quickstart.md` if the working copy is gone. If the tasks run out of order, Task 4 must run before Task 3's deletion or recover from git.

**Consistency check.** Paths used in later tasks match those created earlier: `metrics/index.md`, `metrics/flux.md`, `metrics/reading.md`, `getting-started/first-run.md`, `demo.md`, `operate/troubleshooting.md`, `operate/upgrading.md`, `adr/index.md`. The nav accumulates rather than being rewritten wholesale, so no task leaves the site unbuildable.
