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
checkout. One thing to know if you do: the `version` and `appVersion` committed
there are placeholders for local installs and do not track releases — the real
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
  clusters:
    - name: obs-prod-01
      host: "OBS1_HOSTNAME"
      username: "OBS1_USERNAME"
      password: "OBS1_PASSWORD"
      # collectDT: true
      # collectFlux: true
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

Set `collection.interval` to match your scrape interval rather than exceeding
it. The exporter serves a snapshot, so scraping faster than it collects returns
the same values repeatedly — it costs the cluster nothing, but it does not make
the data fresher.

## Probes

The image is distroless — it contains the exporter binary and nothing else, no
shell and no `curl` — so a probe that runs a command inside the container has
nothing to run. Every probe has to be an HTTP request, which the kubelet — the
Kubernetes agent running on each node — makes from outside the container.

Which endpoint each probe uses is a real decision, not a formality. `/health`
answers 503 while **any** configured cluster is failing. In a deployment that
polls several clusters from one process, that means a single unreachable cluster
would fail a liveness probe and restart a pod that is collecting every other
cluster perfectly well — and the restart cannot help, because nothing about a
fresh process makes an unreachable cluster reachable. `/metrics` answers 200 as
long as the process is up and serving, which is what liveness is supposed to
mean.

So use `/metrics` for liveness and `/health` for readiness. The chart ships with
both probes pointing at `/health`, so on a multi-cluster deployment override the
liveness one:

```yaml
livenessProbe:
  httpGet:
    path: /metrics
    port: http
```

Then alert on `ecs_up` and `ecs_collector_up` rather than on either probe. The
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
