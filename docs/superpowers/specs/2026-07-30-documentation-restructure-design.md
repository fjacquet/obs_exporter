# Documentation restructure

Date: 2026-07-30
Status: design approved
Scope: `docs/`, `mkdocs.yml`, `README.md`

## Context

The documentation is published to GitHub Pages with mkdocs-material. It grew a
page at a time, and an audit after the 3.2.0 release found the structure had
stopped serving its readers:

- `README.md` and `docs/index.md` are two front doors with overlapping content
  and no stated relationship. They have drifted: the README gained a Flux
  section in the 3.2.0 work, `index.md` did not, and `index.md`'s architecture
  diagram still listed five collectors after two more had shipped.
- Twelve of twenty-two navigation entries are architecture decision records.
  A reader arriving to answer "how do I run this?" scans a sidebar that is more
  than half maintainer material.
- Both migration guides sit above Deployment, prominent for readers who are by
  definition not new.
- `metrics.md` runs to 323 lines doing two unrelated jobs: a metric catalog and
  an operator guide for the Flux collector.
- There is no troubleshooting page, although the `--once --debug --trace`
  workflow is the most valuable operational content in the docs. It sits at the
  bottom of Quick start, which is not where anyone looks mid-incident.
- The Docker and Kubernetes pages written during the 3.2.0 documentation pass
  are voiced for a devops audience the project does not have.

The structural problem underneath all of these: several pages have no obviously
correct home, so new content lands wherever there is room. Troubleshooting was
never written partly because no section owned it.

## The reader

Stated by the maintainer, and the constraint everything else follows from.

**A storage sysadmin.** Comfortable with Linux and systemd, networking, TLS,
package management, YAML, reading logs, and containers as a concept. Not
necessarily fluent in Kubernetes, Helm, Prometheus/PromQL, or OTLP.

**Not a devops expert, and not a novice.** The docs explain rather than state,
but they do not teach fundamentals. Nobody needs to be told what a container is.

**Prometheus knowledge is not assumed**, and this matters more than it sounds:
3.2.0 introduced the project's first counters, and `metrics.md` currently tells
the reader that one metric "must be `rate()`d" and another "must never be"
without explaining what that means or what getting it backwards produces.

## Decisions

### 1. Task-ordered site map

Sections follow the order a sysadmin works: understand, install, configure,
deploy, operate, look things up.

```
Home                        index.md
Getting started
  Install                   getting-started/installation.md
  Configure                 getting-started/configuration.md
  First run                 getting-started/first-run.md        NEW
Deploy
  systemd                   deployment/systemd.md
  Docker                    deployment/docker.md
  Kubernetes                deployment/kubernetes.md
Operate
  Verify & troubleshoot     operate/troubleshooting.md          NEW
  Upgrading                 operate/upgrading.md                NEW
Metrics
  Reading the metrics       metrics/reading.md                  NEW
  Reference                 metrics/index.md                    was metrics.md
  The Flux collector        metrics/flux.md                     split out
Try it without hardware     demo.md                             NEW
Design decisions            adr/index.md                        one entry, was twelve
```

Two page groups are published but deliberately absent from the nav: the twelve
ADRs, reached through `adr/index.md`, and the two migration guides, reached
through *Upgrading*. Both are material a reader wants exactly once, on purpose,
rather than while scanning for how to deploy.

Rejected alternatives: a minimal repair that collapsed the ADRs and added the
missing pages without reorganising — cheaper, but it leaves *Getting started*
and *Deploy* competing for "how do I run this in Docker"; and a two-track split
between guides and reference, which adds a navigation layer that mostly costs a
click at fifteen pages.

### 2. Content migration

| Today | Goes to |
| --- | --- |
| Quick start — real-cluster walkthrough | *First run* |
| Quick start — `--once` / `--debug` / `--trace`, `/health` | *Verify & troubleshoot* |
| Quick start — Compose demo stack | *Try it without hardware* |
| `metrics.md` — catalog | *Metrics → Reference* (`metrics/index.md`) |
| `metrics.md` — Flux collector section | *Metrics → The Flux collector* |
| `metrics.md` — counter/gauge and absent-not-zero rules | expanded into *Reading the metrics* |
| `migration-v2.md`, `migration-v3.md` | unchanged on disk and still published; leave the nav, reached through *Upgrading* |
| README — Quick start, Configuration, What it exports, Node Exporter Full | the docs site; README keeps a sentence and a link |

Nothing is deleted. Every page that exists today survives, either in place or
as an explicit destination above.

### 3. The trace workflow becomes a chapter, not a footnote

`operate/troubleshooting.md` is built around the diagnostic workflow rather than
mentioning it. It carries over every flag and example from Quick start intact,
and adds what a first-time user needs:

- What each flag does, including that `--trace` logs every management API
  response body and that the auth token is never among them. A sysadmin should
  know that before running it against production.
- **Why the trace is the right tool for this exporter specifically.** It never
  guesses a value, so a payload the cluster shapes unexpectedly produces a
  *missing* metric, not a wrong one. That is counter-intuitive coming from
  exporters that emit zeros, and it is the most likely reason someone reaches
  this page. The trace distinguishes "the cluster does not report this" from
  "the exporter cannot read it".
- **Symptom-first entries**, so the page is usable mid-incident: metric absent,
  `ecs_up=0`, one collector down while others work, no per-node Flux data,
  `/metrics` returning 503 or 500.
- **Sanitising a trace before sharing.** Trace output carries namespace names,
  node names and IP addresses. Anyone attaching one to a GitHub issue needs to
  know that, and what a maintainer actually needs to see. This is how the
  live-cluster evidence behind ADR-0008 was produced.

### 4. `Reading the metrics` teaches only what affects reading this exporter

Counter versus gauge and why `rate()` applies to one and not the other; why a
metric can be absent rather than zero and that this is deliberate (ADR-0007);
why scraping faster than `collection.interval` returns the same values. About a
page, linked from the reference.

Explicitly not taught: PromQL as a language, recording rules, alerting syntax,
Grafana panel construction. Those duplicate upstream documentation and drift
with Prometheus rather than with this exporter.

### 5. ADRs stay published, stop dominating the sidebar

`adr/index.md` becomes a real page explaining what these records are and
pointing a sysadmin at the ones that answer operational questions — why a metric
is absent rather than zero, why the exporter logs out of clusters on shutdown,
why the Flux collector is opt-in. The individual ADR pages remain published and
linked from there, but leave the nav.

This works because mkdocs does not require every page in the nav: the
`superpowers/` specs already sit outside it and `--strict` passes.

### 6. README sells, the docs site explains

The README answers the question someone has on GitHub before committing to
anything: what is this, does it fit my cluster, is it maintained, where do I
learn more. It keeps badges, one paragraph of description, a plain statement of
supported ObjectScale and ECS versions, a short capability summary, the install
one-liner, the development section, and lineage/license.

It loses everything operational — Quick start, Configuration, the full metric
list, the Node Exporter Full section — each reduced to a sentence and a link.

**The rule that prevents recurrence: every fact has exactly one home.** The
drift that started this was not carelessness; it was that two files both
plausibly owned "what this exports", so updating one felt complete. Under this
split the README has no metric list to forget.

### 7. Voice

**Assume:** Linux, systemd, networking, TLS, package management, YAML, logs,
containers as a concept.

**Do not assume:** Kubernetes objects and idioms, Helm, PromQL, OTLP, or
cloud-native vocabulary — *OCI artifact*, *ClusterIP*, *operator*, *CRD*,
*sidecar*. Define these in a clause at first use, not in a glossary.

Four rules:

1. **Why before how** for anything non-obvious.
2. **State the consequence of getting it wrong.** "Pin the version tag" is an
   instruction; "`latest` moves on every release, so a metric rename between
   majors arrives unannounced on the next pull" is a reason to comply.
3. **Prefer a worked, runnable example to an abstract statement.**
4. **One idea per paragraph, full sentences** — not bullet fragments.

Worked calibration, from the Kubernetes page as first drafted:

> **Before:** A chart is published to the GitHub Container Registry as an OCI
> artifact on every release. Chart version and `appVersion` are both set from
> the git tag.

Four unexplained terms, no reason to care.

> **After:** Every release publishes a Helm chart to GitHub's container
> registry, alongside the container image. Helm installs from it directly —
> there is no chart repository to add first, which is the older workflow you may
> have seen in other projects. The chart version always matches the exporter
> version it deploys, so `--version 3.2.0` gets you the chart for exporter
> 3.2.0 and you never have to look up which pairs with which.

Same facts; the reader now knows what to type, why it differs from what they
have seen elsewhere, and what the version numbers mean.

This applies retroactively: `deployment/docker.md` and `deployment/kubernetes.md`
were drafted before the audience was stated and both need a pass. The Kubernetes
page also says "Prometheus Operator `ServiceMonitor` or `ScrapeConfig`" and
"external secrets operator" with neither explained.

## Mechanical work

- `docs/metrics.md` → `docs/metrics/index.md`. mkdocs serves both as `/metrics/`,
  so **the published URL does not change** and nothing bookmarked or linked from
  GitHub issues breaks.
- In-repo relative links to `docs/metrics.md` do break — they exist in the README
  and several ADRs. `configuration.md` also links the
  `#flux-collector-opt-in-collectflux-true` anchor, which moves to
  `metrics/flux.md`. All greppable.
- `mkdocs.yml` nav rewritten wholesale.
- `mkdocs build --strict` remains the gate. It fails on broken internal links,
  which is what makes a one-pass restructure safe.

## Out of scope

- No docs linter or link-checking CI beyond `--strict`. It already catches
  broken internal links, and enforcing prose style would cost more than it
  catches at this size.
- No redirect plugin. The one URL that would have moved does not.
- No changes to the ADR contents. They are a record; only their navigation
  changes.
- No new diagrams beyond converting existing ASCII ones to mermaid, already done
  for `index.md`.

## Related

- [Flux collector design](2026-07-30-flux-collector-design.md) — the release whose
  documentation pass exposed these gaps.
- ADR-0007 — absent, never zero; the behaviour *Reading the metrics* and
  *Verify & troubleshoot* both have to explain.
