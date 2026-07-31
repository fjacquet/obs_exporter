# First run

This page takes you from an installed binary to a confirmed, working collection
against one real ObjectScale cluster. You run it in the foreground, watch it
work, and check one metric — then, once you know the credentials and the network
path are right, you make it a service.

Doing it in that order is worth the five minutes. A service that fails to reach
its cluster looks almost exactly like a service that is working, because this
exporter is designed to keep serving `/metrics` no matter what the cluster does.
Watching the first run in a terminal is the cheapest way to find out which of the
two you have.

You need three things before you start: the binary on a host
([Installation](installation.md)), a management account on the cluster with
monitoring rights, and network access from that host to the cluster's management
port, 4443 by default.

## The smallest config that works

The exporter reads one YAML file and takes everything else from defaults. This
is the whole of it for a single cluster:

```bash
export OBS1_PASSWORD='...'
cat > config.yaml <<'YAML'
clusters:
  - name: ecs-prod-01
    host: ecs01.example.com
    username: ecs-monitor
    password: "${OBS1_PASSWORD}"
YAML
```

Four fields, and each one is there for a reason.

`clusters` is a list because one exporter process polls many clusters. That is
the normal deployment: a single process, one entry per cluster, one set of
dashboards. Starting with one entry costs you nothing later — you add the second
cluster by adding a second list item.

`name` is the value of the `cluster` label that every metric this exporter
publishes will carry. It is how you tell two clusters apart in a query, a panel
or an alert, and it is the one field here worth thinking about for a moment,
because changing it later is not free: Prometheus treats a series with a
different label value as a *different* series, so a rename splits every graph and
silently breaks every alert that filters on the old value. Pick the name you will
still want in a year. If you omit `name` entirely the exporter falls back to
`host`, which works but leaves your dashboards labelled with a DNS name.

`host` is the cluster's management endpoint — the same host you point a browser
at for the ObjectScale management UI, not the S3 data endpoint your applications
use. The exporter appends port 4443 unless you set `mgmtPort`, and always speaks
HTTPS. Everything on this page comes from that one port. Only one optional
collector, `collectDT`, ever talks to anything else — per-node ports 9021 and
9101 — and it is off by default.

`username` and `password` are the monitoring account. The exporter authenticates
once with HTTP basic authentication, receives a session token, and reuses that
token for every subsequent request until it expires. Monitoring (read) rights are
enough — nothing the exporter does changes anything on the cluster.

The password is written as `${OBS1_PASSWORD}`, an environment-variable reference
that the exporter resolves when it loads the file, so the secret lives in the
environment rather than in a file you might copy into a ticket. If the variable
is not set, the exporter refuses to start and names it. That is deliberate: a
typo in a secret's *name* becomes one clear error at startup instead of an
authentication failure repeating every five minutes for a week.
[Configuration](configuration.md#secrets) covers the alternative, `passwordFile`,
which reads the password from a file — trimmed of surrounding whitespace, so a
trailing newline from `echo` does you no harm — and is the better choice when a
secret manager already puts one on disk for you.

Everything not in that file has a default: the exporter listens on `:9438` and
serves `/metrics`, polls every cluster every 5 minutes with a 60-second budget
per cluster, and enables the namespace metering and quota collectors while
leaving the two opt-in collectors off. [Configuration](configuration.md) is the
full reference for all of it.

One thing to check before you run, because it is the most common first-run
failure: the exporter verifies the cluster's TLS certificate against the host's
trust store. If the cluster presents a self-signed certificate, or one from an
internal CA this host does not know, login fails during the TLS handshake and
every collector fails with it. The right fix is to install the CA certificate on
the exporter host. There is an escape hatch, `insecureSkipVerify: true` on the
cluster entry, but it turns off certificate verification for that cluster —
acceptable in a lab, a deliberate downgrade in production.

## Start it in the foreground

```bash
obs_exporter --config config.yaml
```

It stays attached to your terminal and logs two lines:

```text
INFO[0000] running initial collection cycle
INFO[0000] serving metrics                               addr=":9438"
```

Those two lines are the whole of a healthy startup. They are written
independently and may appear in either order, which does not matter; what matters
is the thing they tell you together, which is that the HTTP server comes up
*before* the first collection has finished. That is deliberate. Logging in to
every cluster and polling all of its endpoints can take longer than a Prometheus
scrape timeout, and an endpoint that hangs looks to Prometheus exactly like a
dead process. So the exporter answers immediately, and a scrape that arrives
during that first cycle gets a valid response containing only
`obs_exporter_build_info` — no cluster metrics yet, but no failed scrape either.

All of this logging goes to standard error, and when *that* is not a terminal the
logging library switches to a machine-readable form. This is the format you will
see in the journal, in `docker logs`, and in the rest of these pages — note that
it takes `2>` or `2>&1` to reproduce locally, since plain `> out.log` redirects
only standard output and leaves the logging on your terminal in the form above:

```text
time="2026-07-31T09:31:02+02:00" level=info msg="running initial collection cycle"
time="2026-07-31T09:31:02+02:00" level=info msg="serving metrics" addr=":9438"
```

Then nothing. A healthy exporter is silent: it does not log a line per cycle, so
after those two lines a working process produces no output at all until it shuts
down. Silence is the success signal, and it is a reliable one, because a
collector that *fails* logs a warning at the default level — you do not need
`--debug` to see failures. If you see a line like this, one domain failed to
collect and the error text names the request that failed:

```text
WARN[0123] collector failed                              cluster=ecs-prod-01 collector=quotas err="GET /object/namespaces: status 403"
```

That is not fatal. A failing collector does not fail the cycle, the cluster or
the scrape — the other collectors keep publishing. [Verify and
troubleshoot](../operate/troubleshooting.md) works through what each error means.

## Confirm the cluster answered

Leave the exporter running and, in a second terminal, ask it the one question
that matters:

```bash
curl -s localhost:9438/metrics | grep '^ecs_up'
```

```text
ecs_up{cluster="ecs-prod-01"} 1
```

`ecs_up` is 1 when the last collection cycle produced at least one real
measurement for that cluster. It is not a ping and not a TCP check: a 1 means the
exporter logged in, called the management API, and got numbers back. This is the
metric to alert on later, one alert per cluster.

A 0 means the cycle produced nothing usable — in practice almost always
credentials, name resolution, the management port, or TLS trust, because those
four break every collector at once. The log line above will name which.

If `ecs_up` is 1, one more check tells you how much of the cluster you are
actually reading:

```bash
curl -s localhost:9438/metrics | grep '^ecs_collector_up'
```

```text
ecs_collector_up{cluster="ecs-prod-01",collector="cluster"} 1
ecs_collector_up{cluster="ecs-prod-01",collector="info"} 1
ecs_collector_up{cluster="ecs-prod-01",collector="metering"} 1
ecs_collector_up{cluster="ecs-prod-01",collector="nodes"} 1
ecs_collector_up{cluster="ecs-prod-01",collector="quotas"} 1
ecs_collector_up{cluster="ecs-prod-01",collector="replication"} 1
```

Six collectors run by default, one per domain, and each reports its own success.
`ecs_up` can be 1 while one of these is 0 — that is the design, and it is why the
per-collector metric exists. A cluster is rarely all-or-nothing: a monitoring
account may be allowed to read the dashboard endpoints and denied the namespace
ones, and the exporter would rather publish five domains than none.

There is also a way to run a single collection cycle, log the result and exit,
without starting the HTTP server at all — useful as a connectivity check after
every config change, including on a host where the real service is already
running. See [Verify and troubleshoot](../operate/troubleshooting.md), which
covers that flag and the two diagnostic flags that go with it.

## Stop it cleanly

Press Ctrl-C. You get one more line:

```text
INFO[0004] shutting down: logging out of all clusters
```

That line is not decoration. ObjectScale limits how many session tokens a single
user may hold at once, and a token that is never logged out stays allocated until
it expires. Stop the exporter enough times without letting it log out — `kill
-9`, or a container runtime that goes straight to SIGKILL — and the monitoring
account eventually runs out of sessions and cannot log in at all, which looks
from the outside like wrong credentials. The exporter handles SIGINT and SIGTERM,
so a normal Ctrl-C, `systemctl stop` or `docker stop` all do the right thing.

## Next: run it as a service

A foreground run proves the configuration. The next question is how to keep it
running, which the Deploy section answers three ways:
[systemd](../deployment/systemd.md) for a package or binary install on a Linux
host, [Docker](../deployment/docker.md) for the published container image, and
[Kubernetes](../deployment/kubernetes.md).

Two things worth reading alongside whichever you choose. [Reading the
metrics](../metrics/reading.md) explains how the exporter's collection interval
relates to your Prometheus scrape interval — they are separate clocks, and
scraping faster than the exporter collects gains you nothing. And if you have not
seen the dashboards yet, [Try it without hardware](../demo.md) starts the whole
stack, dashboards included, against a fake cluster in one command.
