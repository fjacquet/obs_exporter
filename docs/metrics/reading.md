# Reading the metrics

This page covers the four things about this exporter's output that catch people
out: which metrics you may read at face value and which need `rate()`, what
happens to a graph when a counter restarts, why a metric you expect can be
missing rather than zero, and why scraping more often does not get you fresher
numbers. It assumes no prior Prometheus experience.

It is deliberately not a PromQL tutorial. The query language, recording rules
and alerting syntax belong to Prometheus and change with Prometheus; the
upstream documentation will always be better than anything kept here. What
follows is only the part that is specific to reading *this* exporter.

## Gauges and counters

Almost every metric this exporter publishes is a **gauge**: a reading taken at a
moment in time, which can go up or down between one reading and the next.
`ecs_cluster_disk_space_bytes` is a gauge — the cluster has that many bytes
allocated right now, and next cycle it may have fewer. So is
`ecs_node_cpu_utilization_percent`, and so is `ecs_namespace_objects`. A gauge
means what it says. Graph it directly and you get the story.

A few metrics are **counters**. A counter only ever goes up. It is not a reading
of anything you could point at on the cluster — it is a running tally of events
since whatever produced it started counting, so its raw value is close to
meaningless on its own. Knowing that a node has served 4,182,116,733 requests
since some unstated moment tells you nothing you can act on. What you want is
how fast that number is climbing, and that is what Prometheus's `rate()`
function is for: it looks at a counter's values across a window of time and
reports the average increase per second over that window.

These two are the pair that trips people up, because they describe nearly the
same thing and are read in opposite directions:

```promql
rate(ecs_node_requests_total[5m])     # requests per second, from a counter
ecs_cluster_requests_per_second       # already per second — do NOT wrap in rate()
```

The first is a counter: the tally of requests one node has served. `rate()`
turns it into requests per second, averaged over the last five minutes. The
second is a gauge that the cluster averaged into a per-second figure before the
exporter ever saw it, so it is already the answer — there is nothing left to
compute. (Both come from the opt-in [Flux collector](flux.md), so they appear
only when `collectFlux` is on.)

Getting this backwards fails quietly, which is why it is worth knowing. Graphing
a counter raw draws a line that climbs forever and never dips, whatever the
cluster is doing; it looks like a graph and carries no information. Wrapping an
already-averaged rate in `rate()` is worse, because the result is a small,
plausible-looking number: you get the rate of change *of a rate*, in units
nobody wants, and it is not so much wrong as meaningless. Neither mistake
produces an error. Both produce a panel that quietly says nothing.

A `_total` suffix is a strong hint that a metric is a counter, but in this
exporter it is not a promise — most of the names carrying it are served as
gauges. `ecs_node_dt_total` is one: there `_total` means "all of the directory
tables", a count you can take right now and one that falls as well as rises, not
a tally that accumulates. Where the distinction matters it is stated explicitly
— the [reference](index.md) names which `_total` metrics are counters and flags
the values that are already rates, and the [Flux collector](flux.md) mapping
tables give a type for every metric they publish. The exporter's own output is the final authority, since a
Prometheus exporter declares each metric's type in the text it serves:

```bash
curl -s http://localhost:9438/metrics | grep -E '^# TYPE .* counter'
```

That lists the metrics the exporter declares as counters, and those are the ones
`rate()` is for. Everything else is served as a gauge — and in particular any
name ending `_per_second` or `_rate` is a figure the cluster averaged for you
before the exporter saw it, so leave those alone.

## Why a counter reset matters

A counter counts from the moment the process producing it started, and that
process can restart. These counters come from ObjectScale's own monitoring
store, and ObjectScale restarts them from zero when the datahead service on a
node restarts — a service restart, a node reboot, a rolling upgrade. The tally
does not carry over; it begins again at zero.

That is not a problem for `rate()`. Prometheus knows counters reset, so when it
sees the value drop it treats the drop as a restart rather than as an enormous
negative rate, and the per-second line stays continuous across the event.

It is very much a problem for the same data plotted raw. A counter graphed as if
it were a gauge climbs steadily for days and then falls off a cliff to zero in a
single step. On a dashboard that reads as a total outage, and sooner or later
somebody pages on it at three in the morning to find that a service restarted
normally and nothing was ever lost. This is the second reason to `rate()` a
counter rather than graph it: `rate()` is the thing that knows the difference
between "traffic stopped" and "the counter restarted".

## Absent, never zero

The exporter never invents a value. Every number on `/metrics` was read out of a
payload the cluster actually returned and parsed cleanly as a number. When a
cluster does not report a field, or reports it in a shape the collector cannot
read, the metric is simply **absent** from `/metrics` for that cycle. It is not
published as `0`, and the previous cycle's value is not carried forward.

That is deliberate, and recorded as
[ADR-0007](../adr/0007-obs-4-1-api-alignment.md). A zero is a claim. "The pool
is 0% full" and "we could not read how full the pool is" are very different
statements, and once a fabricated zero is stored in Prometheus nothing
downstream can tell them apart — it will eventually fire an alert, or clear one
that should have fired.

The practical consequence is that a panel which is *empty* rather than flat at
zero usually means the cluster did not report that field, not that the exporter
is broken. `ecs_up` and every `ecs_collector_up` can be 1 while a metric you
expect is missing, and the log may say nothing at all, because from the
collector's point of view nothing went wrong. Telling "your cluster does not
publish this" apart from "the exporter could not read what your cluster sent"
means looking at the payload itself, which is what the exporter's trace mode is
for. [Verify and troubleshoot](../operate/troubleshooting.md) walks through that
workflow.

## Scrape interval and collection interval

Two separate clocks are involved here, and confusing them wastes effort.

The exporter polls each configured cluster on its own schedule —
`collection.interval`, five minutes by default — and keeps the result. A scrape
of `/metrics` never touches ObjectScale; it hands back whatever the last poll
produced. That is the point of the design
([ADR-0002](../adr/0002-prometheus-snapshot-model.md)): a slow or unreachable
cluster can never slow down or fail a scrape, and adding another Prometheus
server that scrapes the exporter adds no load at all to the cluster.

The consequence is that scraping faster than the exporter collects returns the
same numbers over and over. It costs ObjectScale nothing, so it will not hurt
the cluster — but it does not make the data any fresher either. It only stores
more copies of a reading that may already be five minutes old. So set the two
to match:

```yaml
# obs_exporter config.yaml
collection:
  interval: "1m"
```

```yaml
# prometheus.yml
scrape_configs:
  - job_name: obs_exporter
    scrape_interval: 1m
    static_configs:
      - targets: ["obs-exporter.example.net:9438"]
```

If you want fresher data, `collection.interval` is the knob — it is what decides
how old a number can be. Lowering only the scrape interval changes nothing
except how much Prometheus stores.

!!! note "Match them low, not high"
    Prometheus stops treating a sample as current five minutes after it was
    scraped (its *staleness* window), so a five-minute scrape interval sits
    exactly on that boundary and a single missed scrape leaves a visible gap in
    every graph. Bring `collection.interval` down to the scrape interval you
    actually want rather than stretching the scrape interval up to the
    five-minute default.

Lowering `collection.interval` does have a price at the cluster end: every cycle
re-polls every configured cluster. Most collectors cost one request per cluster
per cycle, and namespace usage is a single bulk request whatever the namespace
count — but `collectQuotas` has no bulk endpoint and issues one request per
namespace per cycle. On a cluster with hundreds of namespaces, that is the
number that grows when you halve the interval.
