# Kubernetes (Helm)

A chart is published to the GitHub Container Registry as an OCI artifact on
every release. Chart version and `appVersion` are both set from the git tag, so
the chart version always equals the exporter version it deploys.

```bash
helm install obs-exporter \
  oci://ghcr.io/fjacquet/charts/obs-exporter --version 3.2.0 \
  -f values.yaml
```

The chart source lives in `charts/obs-exporter/` if you prefer to install from a
checkout; the committed `version`/`appVersion` there are placeholders for local
installs and do not track releases.

## Configuration

The chart takes the exporter's whole `config.yaml` as one value. It is rendered
through Helm's templating first, then written to a Secret and mounted at
`/etc/obs_exporter`.

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

Because the value is a free-form blob rather than a typed schema, **every**
exporter setting works here — including flags added after this chart version was
published. Nothing needs a chart change to become configurable.

## Secrets

Putting a password in `values.yaml` puts it in your release history. Two better
options:

**Bring your own Secret.** Set `existingSecret` to the name of a Secret whose
key is `config.yaml`, containing the complete config. The chart then renders no
Secret of its own:

```yaml
existingSecret: obs-exporter-config
```

```bash
kubectl create secret generic obs-exporter-config \
  --from-file=config.yaml=./config.yaml
```

**Reference environment variables.** `config.yaml` interpolates `${VAR}` at load
time, so keep the config in the chart and inject only the secret through `env`,
sourced from a Secret you manage separately or from an external secrets
operator.

`passwordFile` is the third route: point it at a mounted file and the exporter
reads the value verbatim, which avoids the shell-quoting pitfalls that bite
passwords containing `$`, `#` or quotes.

## Service and scraping

The service is `ClusterIP` on port **9438**. The chart can also render a
Prometheus Operator `ServiceMonitor` or `ScrapeConfig` — see
`prometheus.monitor` and `prometheus.scrapeConfig` in `values.yaml`. Without the
operator, scrape the service directly.

Set `collection.interval` to match your scrape interval rather than exceeding
it. The exporter serves a snapshot, so scraping faster than it collects returns
the same values repeatedly — it costs the cluster nothing, but it does not make
the data fresher.

## Probes

The image is distroless with no shell, so probes must be HTTP. Use `/metrics`
for liveness and `/health` for readiness: `/health` answers 503 while any
cluster is failing, and in a multi-cluster deployment that means one unreachable
cluster would otherwise restart a pod that is serving every other cluster
correctly. Alert on `ecs_up` and `ecs_collector_up` instead — the exporter
degrades per cluster and per collector by design, and a restart does not fix an
unreachable cluster.

## Reload

The exporter watches its config file, and Kubernetes propagates Secret updates
to mounted volumes, so editing the Secret reloads the collection loop without a
pod restart. Propagation is not instant — it follows the kubelet sync period.
`kubectl rollout restart` is the immediate route.
