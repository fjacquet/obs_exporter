# Kubernetes (Helm)

Every release publishes a Helm chart to GitHub's container registry, alongside
the container image. Helm installs from it directly — there is no chart
repository to add first, which is the older workflow you may have seen in other
projects. The chart version always matches the exporter version it deploys, so
`--version 3.2.0` gets you the chart for exporter 3.2.0 and you never have to
look up which pairs with which.

```bash
helm install obs-exporter \
  oci://ghcr.io/fjacquet/charts/obs-exporter --version 3.2.0 \
  -f values.yaml
```

The `oci://` prefix is the only unusual part of that command. It tells Helm the
chart is stored in a container registry, in the same way a container image is,
rather than in a chart repository you had to register beforehand.

The chart source is in `charts/obs-exporter/` if you prefer to install from a
checkout. One thing to know if you do: the two version numbers committed there —
the chart's own `version` and the `appVersion` recording which exporter release
it deploys — are placeholders for local installs and do not track releases. The real
numbers are stamped in when a tag is published. A local install therefore
reports a chart version that means nothing, so pull the published chart whenever
the version matters to you.

## Configuration

The chart does not model the exporter's settings one at a time. It takes the
whole `config.yaml` as a single value, renders it through Helm's templating,
writes the result into a Secret — Kubernetes' built-in store for sensitive files
— and mounts that Secret at `/etc/obs_exporter`, where the exporter reads it as
an ordinary file. It is the same file described in
[Configuration](../getting-started/configuration.md), delivered a different way.

```yaml
config: |
  server:
    host: "0.0.0.0"
    port: "9438"
  collection:
    interval: "5m"
  # labels:
  #   env: prod
  #   site: geneva
  clusters:
    - name: obs-prod-01
      host: "OBS1_HOSTNAME"
      username: "OBS1_USERNAME"
      password: "OBS1_PASSWORD"
      # collectDT: true
      # collectFlux: true
      # labels:
      #   site: zurich
```

That design has a consequence worth knowing before you go hunting for a chart
value that does not exist. Because the block is free-form text rather than a
typed schema, **every** exporter setting works here — including settings added
to the exporter after this chart version was published. Nothing needs a chart
change to become configurable.

## Secrets

Putting a password in `values.yaml` puts it in your release history, which Helm
keeps in the cluster and prints straight back out: `helm get values obs-exporter`
returns everything you installed with, password included, to anyone allowed to
read that release. Two better options:

**Bring your own Secret.** Create the Secret yourself and set `existingSecret`
to its name. The key inside it must be `config.yaml` and it must hold the
complete configuration, because the chart then renders no Secret of its own — it
mounts yours in place of the one it would have generated:

```yaml
existingSecret: obs-exporter-config
```

```bash
kubectl create secret generic obs-exporter-config \
  --from-file=config.yaml=./config.yaml
```

This is the route to take when the config file already exists somewhere you
trust, or when something other than Helm is responsible for producing it.

**Reference environment variables.** `config.yaml` interpolates `${VAR}` when
the exporter loads it, so the config can stay in the chart with only the secret
injected at runtime through the chart's `env` value. Source that variable from a
Secret you manage separately, or from an external secrets operator — a
controller you install in the cluster that copies values out of a secret manager
you already run and keeps matching Kubernetes Secrets in step with it, so the
credential never has to exist in a file you keep.

`passwordFile` is the third route: point it at a mounted file and the exporter
reads the value verbatim, which avoids the shell-quoting pitfalls that bite
passwords containing `$`, `#` or quotes.

## Service and scraping

The chart creates a Service of type `ClusterIP` on port **9438**. `ClusterIP`
means the exporter answers on an address that exists only inside the cluster:
convenient, because nothing outside can reach a process that holds your cluster
credentials, but it also means whatever scrapes it has to be inside the cluster
too, or reach in through an ingress or proxy you already run.

How you point Prometheus at that Service depends on how Prometheus itself is
run.

**If you run the Prometheus Operator** — the controller most clusters use to
manage Prometheus, which watches for configuration objects and rewrites
Prometheus's own configuration from whatever it finds — the chart can render one
of those objects for you. `prometheus.monitor` in `values.yaml` renders a
`ServiceMonitor`, which says "scrape this Service"; `prometheus.scrapeConfig`
renders a `ScrapeConfig`, the more general form of the same idea. Both are off
by default. Turn one of them on and the operator picks up the exporter without
you editing a Prometheus config file anywhere.

**If you do not run that operator**, leave both off. Nothing else in Kubernetes
reads a `ServiceMonitor`, so enabling one on a cluster without the operator
creates an object that is silently ignored and a target that is never scraped.
Point your Prometheus at the Service's address and port 9438 the way you would
point it at any other host.

Whichever route you take, set `collection.interval` to match the scrape interval
you configure here rather than leaving it higher. [Reading the
metrics](../metrics/reading.md#scrape-interval-and-collection-interval) explains
why these are two separate clocks and why matching them low beats matching them
high.

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

`/health` still exists and always answers 200, with a JSON body naming every
cluster's status (`ok`/`err` per cluster). It is not what the chart's probes
use, but it is still the right endpoint for a human checking in, or for a
monitoring system that wants to know *which* cluster is degraded — read the
body, not the status code ([ADR-0015](../adr/0015-health-always-200.md)).
[Verify and troubleshoot](../operate/troubleshooting.md#checking-health-without-scraping)
covers it in full.

Because `/livez` and `/readyz` don't wait on the first collection cycle, there
is no startup window to cover with `initialDelaySeconds` or a `startupProbe`.
`/health`'s body reports an empty `clusters` array until that first cycle
finishes (bounded by `collection.timeout`, 60 seconds by default), but its
status code is 200 throughout.

Alert on `ecs_up` and `ecs_collector_up` rather than on any probe. The
exporter is built to degrade per cluster and per collector instead of going
dark, and those two metrics are the ones that say which part is degraded.

## Reload

The exporter watches its config file, and Kubernetes propagates Secret updates
to the volumes that mount them, so editing the Secret reloads the collection
loop without a pod restart. Propagation is not instant, though: the kubelet
refreshes a mounted Secret on its own sync period, so there is a gap between the
moment you update the Secret and the moment the new file appears inside the
container. That gap is the node's schedule, not something you can set from the
chart. `kubectl rollout restart` is the immediate route when you would rather
not wait it out.
