# Config hot reload: rebuild and swap

## Status

Accepted (2026-06-10)

## Context

Operators add/remove clusters and rotate credentials; restarting the exporter for
that drops `/metrics` and loses the warm snapshot. The family standard (ppdd
ADR-0005) is SIGHUP + file-watch reload.

## Decision

A `config.Watcher` reloads and revalidates the file on **SIGHUP** or an fsnotify
event. It watches the config file's *parent directory* (editors and config
managers replace files via temp-file + rename, which kills an inode watch) and
filters events to the config basename. A failed reload is logged and dropped — the
running config stays.

On a successful reload the `collectorRunner` performs a **rebuild-and-swap**: stop
the current loop, log its clients out, build new clients + collectors from the new
config, run one immediate cycle (new clusters appear without waiting a full
interval), and start the new loop. The `SnapshotStore` is shared and never
replaced, so `/metrics` and `/health` serve continuously across the swap.

`server.*` changes (bind address/port/URI) still require a restart and are flagged
in the log.

## Consequences

- Cluster membership and credentials are operational changes, not deployments.
- One collection cycle of latency on reload; no metrics gap.
