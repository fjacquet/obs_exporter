# Try it without hardware

You can run the whole thing — exporter, Prometheus, Grafana and every provisioned
dashboard — without an ObjectScale cluster, without credentials, and without
asking anyone for access to production. One command starts a fake cluster
alongside the real exporter and gives you a browser tab full of populated
dashboards.

This is the fastest way to answer "what would this actually give me?" before you
spend anything on it. It is also useful once you are running it for real: it is a
safe place to try a dashboard edit, a new alert expression, or a build from a
branch.

The only prerequisite is Docker with the Compose plugin and a clone of the
repository. You do not need a Go toolchain — the binaries are compiled inside the
build containers — and you do not need to configure anything.

## What is in the stack

Four containers, wired together on a private Compose network:

**`mockecs`** is a fake ObjectScale management API. It answers on port 4443 over
TLS with a self-signed certificate it generates at startup, issues a session
token from `GET /login` exactly as a real cluster does, and serves recorded JSON
payloads for every endpoint the exporter calls. Those payloads are copies of the
fixtures the test suite runs against, derived from the ObjectScale 4.1 API
reference and corrected where a live 4.3 cluster contradicted it. It is not an
ObjectScale emulator — it replays responses, it does not simulate a cluster — and
it exists only for this stack. It is never published as an image.

**`obs_exporter`** is the real exporter, unmodified, running the real collectors
against `mockecs`. Its configuration is `config.demo.yaml` in the repository
root, and it differs from a production config in the ways you would expect: it
points at the `mockecs` service instead of a cluster, it accepts that service's
self-signed certificate, and it polls every 30 seconds instead of the 5-minute
default so that a demo draws a visible line on a graph in a couple of minutes
rather than an hour. It is a reasonable file to read as a worked example, but do
not deploy it.

**`prometheus`** scrapes the exporter every 30 seconds and stores the results.
Nothing is persisted outside the containers, so the history disappears when you
tear the stack down.

**`grafana`** starts with its Prometheus data source and all its dashboards
already provisioned from files in the repository — the same dashboard JSON you
would import into your own Grafana. There is no import step and no wiring to do.

## Start it

```bash
git clone https://github.com/fjacquet/obs_exporter
cd obs_exporter
make demo
```

`make demo` builds the exporter and `mockecs` from the source in your working
tree and then starts all four containers attached to your terminal, streaming
their logs. It stays in the foreground; open a second terminal to poke at it.

The first run takes a few minutes because it compiles two Go binaries and pulls
the Prometheus and Grafana images. If you only want to see the exporter — not to
test a change to it — the published image skips the exporter build entirely:

```bash
make demo-ghcr
```

That variant pulls `ghcr.io/fjacquet/obs_exporter:latest` from the GitHub
Container Registry and builds only `mockecs` locally, since `mockecs` is
demo-only and has no published image. Use it to evaluate a release; use
`make demo` when you are working on the code.

The stack binds three ports on your host — 3000, 9090 and 9438 — so they need to
be free.

## Open the dashboards

Grafana is on <http://localhost:3000>. Log in with **admin / admin**; these are
throwaway local credentials set in the Compose file, not something to reuse
anywhere.

The dashboards are in a folder called **ObjectScale**. Start with **ObjectScale —
Overview**, which is the on-call view and the one that shows what this exporter
is for. It is arranged as four questions, top to bottom: is anything on fire
(cluster up, good and bad nodes and disks, unacknowledged alerts, replication RPO
lag), how much capacity runway is left, what the request path is doing
(transaction latency, rate, bandwidth and error ratio), and finally whether the
exporter itself is healthy.

From there, the **ObjectScale dashboards** dropdown at the top of every dashboard
links the six focused drill-downs — Performance, Nodes, Namespaces,
Replication, Storage internals, and Maintenance & Directory Tables — keeping your
current time range as you move between them.

One dashboard in that folder is not ours: **Node Exporter Full** is the
well-known Linux host dashboard — [Grafana
1860](https://grafana.com/grafana/dashboards/1860-node-exporter-full/), shipped
as `node-exporter-full.json` and provisioned alongside the others. It draws
host-operating-system metrics — CPU, memory, disk and network as Linux reports
them — which come from
[`prom/node-exporter`](https://hub.docker.com/r/prom/node-exporter), not from
this exporter. Nothing in this stack feeds it, so it will be empty.

That omission is deliberate. `node_exporter` belongs on the hosts you actually
want to watch, which for ObjectScale means the cluster nodes themselves, not
bolted onto the exporter's Compose file where it would only report on the
container host. The dashboard is bundled because the two views answer different
halves of the same question — this exporter tells you what ObjectScale thinks it
is doing, and `node_exporter` tells you what the underlying Linux hosts are
doing. To populate it, run `prom/node-exporter` on those hosts and add a
`node-exporter` scrape job to your own Prometheus.

Two other endpoints are worth a look while the stack is up. The exporter's raw
output is at <http://localhost:9438/metrics>, which is the fastest way to read the
actual metric surface, and Prometheus is at <http://localhost:9090> if you want to
try a query against it. Everything you see there is described in the [metrics
reference](metrics/index.md).

## What the demo cannot tell you

The numbers are replayed from fixed fixtures. Every collection cycle asks
`mockecs` the same questions and gets the same answers back, so the values do not
move the way a real cluster's would.

That has consequences worth knowing before you read too much into a panel.
Capacity does not fill, so "projected time to full" is meaningless. Latency does
not spike and error ratios do not change, so the transaction panels draw flat
lines. Anything derived from change over time — a growth rate, a trend, a
threshold crossing — is showing you the arithmetic working, not a cluster
behaving.

Nothing here says anything about performance either, in any direction. The
exporter is talking to a local process that answers instantly from memory, so
collection timings from the demo tell you nothing about how long a real cluster
takes to answer, and the demo's 30-second interval is deliberately far tighter
than you would use against real hardware.

What the demo *does* show you faithfully is the part that is usually hard to
evaluate from a README: exactly which metrics exist, what labels they carry, how
they are named, and what the dashboards built on them look like when populated.
That is the thing worth deciding on.

## Stop it

Ctrl-C stops the containers. To remove them, along with their network and
anything left over:

```bash
make demo-down
```

That tears down both variants of the stack, so it cleans up after `make demo` and
`make demo-ghcr` alike. Nothing is left behind — no volumes, no stored metrics,
no state.

When you are ready to point it at a real cluster, [First
run](getting-started/first-run.md) is four lines of configuration and one command.
