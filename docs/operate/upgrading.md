# Upgrading

## Which guide applies to you

Start here rather than in a rename table. The project follows semantic
versioning, and it means what it says: **a major version bump is the only kind
that renames or removes a metric.** Everything else is additive. So the only
thing that decides how much work an upgrade is, is which major version you are
coming from.

| You are running | Upgrading to | What you need |
| --- | --- | --- |
| v1 (`prometheus-emcecs-exporter`) | 3.x | [Migrating from v1](../migration-v2.md) — new binary, new config format, new scrape configuration, all new metric names |
| v2.x | 3.x | [Migrating to v3](../migration-v3.md) — same config and endpoints; around thirty metric names consolidated into one name plus a label |
| 3.x | a later 3.x | Nothing. Replace the binary. |

The v1 guide's rename table gives the **current** names in its right-hand
column, not the v2 names it originally targeted, so a v1 user upgrading today
makes one hop rather than two. Do not read both guides in sequence.

Within 3.x there is no migration to do. 3.2.0 — the only release since 3.0.0 —
added metrics and fixed bugs without removing or renaming anything, and no
existing series changed type. (A 3.1.0 appears in some notes: it was drafted
while the work was split in two, both halves shipped together, and it was never
released. If you see it referenced, it means 3.2.0.)

For what changed in one specific release, read the
[CHANGELOG](https://github.com/fjacquet/obs_exporter/blob/main/CHANGELOG.md). It
records added, changed, fixed and removed metrics per version, and it marks
breaking changes **in the section headings** — `Removed — BREAKING`,
`Changed — BREAKING`. Read the headings rather than the opening line: some
entries begin with a sentence saying whether the release is breaking and some go
straight into their sections, so an entry with nothing at the top is not an entry
with nothing to worry about.

## The order to do a major upgrade in

The migration guides tell you which names changed. They do not tell you when to
change your queries relative to when you change the binary, and that ordering is
where a major upgrade actually goes wrong.

The constraint is simple and there is no way around it: **a scrape only ever
contains the names produced by the version that is running.** Old and new names
are never visible at the same time, because that would require running both
versions at once and you are not going to do that. There is no deprecation
window where both work, and no flag that emits the old names alongside the new.
The moment you restart the exporter on a new major, every dashboard panel and
every alert rule still asking for an old name goes to no data — silently, in the
case of alerts, which stop firing rather than start complaining.

So split the work either side of the restart:

1. **Before.** Read the rename table for your hop and find every query that
   references a name in the left-hand column — dashboard panels, alert rules,
   recording rules (the precomputed queries Prometheus stores back as new
   series), and anything else querying this exporter. Write the
   replacements, but do not apply them yet: they would query names that do not
   exist until the new binary is running. If you run the Grafana dashboards from
   this repository, take the updated ones from the same release as the binary,
   because they are updated together.
2. **Upgrade.** Replace the binary or image and restart. Confirm the new names
   are actually being produced before touching anything else — one collection
   cycle is enough:

   ```bash
   obs_exporter --config /etc/obs_exporter/config.yaml --once --debug | sort
   ```

   This runs a single cycle and prints every collected sample without starting
   the HTTP server, so it is safe to run alongside the service. See
   [Verify and troubleshoot](troubleshooting.md) for what the output means and
   what to do when a name you expected is not in it.
3. **After.** Apply the query changes you prepared. Work through alerts before
   dashboards: a broken panel is visible to whoever opens it, whereas a broken
   alert is invisible until the thing it was watching happens.

**Keep the previous binary on the host until the dashboards are confirmed.** A
rollback is the fastest way out of an upgrade that surprises you, and it stops
being available the moment a package manager or an image pull removes the old
version. Keep the old container image tag, or a copy of the old binary
alongside the new one, for as long as it takes you to check the panels.

## What an upgrade does not touch

Two things people expect to have to redo and do not, on any hop from v2 onward:
the config file format and the HTTP endpoints. `config.yaml`, the `/metrics`
path and the `cluster` label are the same in v2 and v3, so the Prometheus scrape
configuration does not change and neither do your secrets. Coming from v1 both
of those do change, and the [v1 guide](../migration-v2.md) covers them.

Historical data is not rewritten either. Series recorded under an old name stay
in Prometheus under that name, queryable for as long as your retention keeps
them; they simply stop gaining new samples. A dashboard switched to the new
names starts its history at the upgrade. If you need one important panel to draw
a continuous line across the cutover, ask for both names in one query. `or`
returns everything on its left, plus anything on its right that does not already
appear on the left — which is what fills the gap, since only one of the two names
has data at any given moment.

The catch is that "already appears on the left" is decided by comparing the whole
label set, metric name included. The old and new names differ in both — `state`
exists only on the new one — so writing them side by side gives you two
differently-labelled series and two broken half-lines, not one continuous one.
Reduce both sides to the same shape first:

```promql
sum by (cluster) (ecs_cluster_disks{state="good"}) or sum by (cluster) (ecs_cluster_good_disks)
```

Now both halves are labelled `{cluster="…"}` and nothing else, so Grafana joins
them into a single line. That is worth doing for a long-run capacity trend and
rarely worth it for anything else; most panels are fine starting fresh.
