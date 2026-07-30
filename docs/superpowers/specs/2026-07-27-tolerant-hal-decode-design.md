# Tolerant HAL list decoding — design

Date: 2026-07-27
Status: implemented in v2.7.1 (PR #19)
Target release: v2.7.1

## Context

PR [#18](https://github.com/fjacquet/obs_exporter/pull/18) (merged, released as
v2.7.0) changed `internal/ecs/nodes.go` and `internal/ecs/replication.go` to read
their HAL instance lists from `_embedded._instances` instead of the previous
`_embedded.instances`. The contributor validated the underscore form live against
an ObjectScale 4.3 cluster and reports it consistent from ECS 3.8 onward.

Two reviewers flagged the same risk afterwards: the exporter now accepts exactly
one key, so any cluster emitting the other form parses **zero** instances, emits
no `ecs_node_*` or `ecs_replication_group_*` metrics, and still reports
`ecs_collector_up 1`. That silent-empty failure is the same class of bug PR #18
set out to fix.

- CodeRabbit, [r3639388915](https://github.com/fjacquet/obs_exporter/pull/18#discussion_r3639388915):
  "Accept both keys in both decoders and add coverage, or replace the
  compatibility note with an explicit unsupported-cluster warning."
- Contributor, [comment 5093548679](https://github.com/fjacquet/obs_exporter/pull/18#issuecomment-5093548679):
  asks which form our clusters return, offers a follow-up PR.

### What the evidence actually supports

There is no live cluster available to this project. Provenance of each shape:

- `_instances` — field-confirmed by the contributor on ECS 3.8 through
  ObjectScale 4.3.
- `instances` — taken from the Dell 4.1 REST reference examples. ADR-0007 records
  that the fixture suite mirrors those examples; no cluster was ever observed
  emitting it.
- `docs/swagger/6972-4.1.0.json` — settles nothing. 306 of 309 operations declare
  an empty response schema (`{"type":"object","properties":{}}`), 3 declare none,
  and `components.schemas` is empty. The only `instances` token in the file is the
  unrelated path `/vdc/instances/storageservers`. ADR-0008 already records this
  limitation: response-field mappings "remain fixture-derived and cannot be
  checked against the spec".

So the underscore form is the only shape with real-world evidence behind it, and
no regression is expected on this project's side. The documented form cannot be
ruled out either, and the cost of tolerating both is roughly 60 lines. Both keys
get accepted.

ADR-0007 itself carries the wrong shape in its decision text
(`_embedded.instances[]`, line 23) and must be corrected, not merely extended.

## Goals

- Decode the nodes and replication-group HAL lists under either key.
- Leave a diagnosable trace when neither key is present, so the next shape change
  is not silent.
- Close the open documentation threads from PR #18.
- Keep the emitted metric set byte-identical: no new, renamed, or re-labelled
  series.

## Non-goals

- Flux / InfluxDB collector for the missing performance metrics. Open in
  principle, but requires its own ADR (ADR-0011) first.
- Moving `ecs_cluster_good_nodes` and friends to `ecs_cluster_nodes{state="…"}`.
  Better Prometheus practice, but a breaking rename — v3 candidate.
- Fixing ADR-0008 F1/F2/F3 blind. ADR-0008 explicitly warns against changing them
  without live verification.

## Design

### Architecture

One new file, `internal/ecs/hal.go`, holding the tolerance rule exactly once:

```go
// halList decodes a HAL "_embedded" list. Real clusters (ECS 3.8 through
// ObjectScale 4.3, field-confirmed) key it "_instances"; the Dell 4.1 reference
// examples show "instances". The bundled swagger settles nothing — every
// response schema in it is empty (ADR-0008). Accept either key: a mismatch
// yields zero samples with no error, the worst failure mode this exporter has.
type halList[T any] struct {
 Instances []T
 KeySeen   bool
}

func (h *halList[T]) UnmarshalJSON(b []byte) error {
 var raw struct {
  Underscore []T `json:"_instances"`
  Documented []T `json:"instances"`
 }
 if err := json.Unmarshal(b, &raw); err != nil {
  return err
 }
 switch {
 case raw.Underscore != nil:
  h.Instances, h.KeySeen = raw.Underscore, true
 case raw.Documented != nil:
  h.Instances, h.KeySeen = raw.Documented, true
 }
 return nil
}
```

Presence is tested with `!= nil`, not `len() > 0`: a legitimate `"_instances": []`
from an empty cluster must count as a key sighting, otherwise it triggers a false
warning. When both keys carry a list, `_instances` wins.

This follows the established idiom of the package — `Num` in
`internal/ecs/points.go` already implements a pointer-receiver `UnmarshalJSON` for
the same reason (tolerating ECS payload variance).

Rejected alternatives:

- **Dual fields per response struct plus an accessor.** No generics, no refactor,
  but the rule and its presence flag are duplicated across two files. Drift
  between the two copies reproduces the exact silent-empty bug being fixed.
- **Decode `_embedded` into `map[string]json.RawMessage` and look up either key.**
  Accepts arbitrary future keys, but drops the typed wrapper, adds a second
  unmarshal pass and per-cycle allocation, and the generality is speculative.

### Components

- `internal/ecs/hal.go` — new; `halList[T]` and its `UnmarshalJSON`.
- `internal/ecs/nodes.go` — lift the anonymous nested instance struct
  (`nodes.go:21-49`) into a named `nodeInstance`, so the response type becomes:

  ```go
  type localZoneNodesResp struct {
      Embedded halList[nodeInstance] `json:"_embedded"`
  }
  ```

  The loop body is unchanged (`r.Embedded.Instances`).
- `internal/ecs/replication.go` — same, with `replicationGroupInstance`.

No other collector reads an `_embedded` list: `cluster.go`, `info.go`,
`metering.go`, and `dt.go` are untouched.

### Data flow

Unchanged. `Collect` → `client.Get` → decode → `[]Sample`; the collection loop
stamps the `cluster` identity label via `Sample.WithCluster`. The snapshot model
(ADR-0002) and the label-key invariant (ADR-0006) are unaffected, since no metric
name or label set changes. No Grafana dashboard change is required.

### Error handling

Warning log only. `ecs_collector_up` stays `1`, and no error is returned:

```go
if !r.Embedded.KeySeen {
 log.WithField("path", pathLocalZoneNodes).
  Warn("HAL list key not found (_instances/instances); payload shape may have changed")
}
```

`logrus` is already the package logger (`internal/ecs/collector.go:87`). One line
per cycle per affected collector, not per instance.

Returning an error instead (flipping `ecs_collector_up` to `0`, making the
condition alertable) was considered and rejected: if some build omits `_embedded`
entirely on a genuinely empty cluster, `UnmarshalJSON` never runs, `KeySeen` stays
`false`, and drift becomes indistinguishable from emptiness — a false `up=0` is
worse than a missed alert here.

Deliberate consequence: a completely absent `_embedded` also logs the warning.
Both cases are shape drift and both deserve the breadcrumb.

The existing guard at `collector.go:104` (`domainSamples == 0` → `cs.OK = false`)
stays as is. It does not cover this case, because `cluster` and `info` keep
producing samples while `nodes` and `replication` go empty. The log is the only
detection added.

### Testing

`internal/ecs/hal_test.go` — table-driven over `halList[struct{ Name string }]`:

| input | expected |
| --- | --- |
| `{"_instances":[{…},{…}]}` | 2 items, `KeySeen=true` |
| `{"instances":[{…},{…}]}` | 2 items, `KeySeen=true` |
| `{"_instances":[]}` | 0 items, `KeySeen=true` (legitimately empty cluster) |
| `{"_links":{}}` | 0 items, `KeySeen=false` |
| both keys populated | contents of `_instances`, `KeySeen=true` |

`internal/ecs/nodes_test.go` and `internal/ecs/replication_test.go` — one case each
that reads the real fixture, replaces `"_instances"` with `"instances"`, serves it
through an `ecsclient.Mock`, and replays the existing assertions.

Deriving the documented-shape payload from the real fixture at test time is
deliberate: no second fixture file to drift, and nothing new to keep in sync under
`cmd/mockecs/fixtures/` (a standing CLAUDE.md rule). `internal/ecs/testdata/` stays
on `_instances`, the only field-confirmed shape.

The warning log is not asserted — logrus is global here, capture is awkward, and
the value is low.

Gate: `make ci`.

### Documentation

- **`docs/adr/0007-obs-4-1-api-alignment.md`** — correct `_embedded.instances[]`
  (line 23) to `_embedded._instances[]`. Add the tolerant-decode decision under
  *Defensive payload parsing*, noting that the Dell reference and real clusters
  diverge and that the bundled swagger cannot arbitrate.
- **`docs/adr/0008-swagger-4.2-validation-findings.md`** — record the HAL key as
  another mapping the empty-schema swagger cannot prove, and note that a live 4.3
  cluster is reachable through the PR #18 contributor for F1/F2/F3.
- **`docs/metrics.md`** — list all five `ecs_node_health_state` values (`good`,
  `bad`, `suspect`, `notaccessible`, `maintenance`) instead of three plus an
  ellipsis (CodeRabbit r3639388941); mark per-node CPU / memory / NIC and cluster
  `transaction*` metrics as possibly absent depending on cluster and version,
  since their absence is confirmed at raw-API level on 4.3.
- **`CHANGELOG.md`** — v2.7.1 entry; the "supporting both keys is under
  discussion" line left by PR #18 becomes resolved.

### Delivery

Branch `fix/tolerant-hal-decode` off `main`. Released as **v2.7.1** — a patch:
decoding robustness only, no metric added or renamed.

## Reply to the PR #18 contributor

Drafted here, posted by the maintainer. Five points:

1. **`_instances` vs `instances`** — no live cluster on this side; `instances`
   came from the Dell 4.1 reference examples, and the bundled swagger proves
   nothing (306 empty response schemas, empty `components.schemas`; already
   recorded in ADR-0008). No regression here: their `_instances` is the only form
   with hardware evidence. Tolerant decode ships in v2.7.1 regardless, implemented
   by the maintainer rather than as a follow-up PR.
2. **Missing performance fields** — never observed populated, unverifiable here.
   Their raw-API confirmation stands as the record; the metrics get documented as
   optional.
3. **Flux API** — open in principle, ADR first (ADR-0011). The proposal should
   answer: InfluxDB auth and config surface, bucket + measurement → metric
   mapping, collector inside obs_exporter vs a separate exporter, test strategy
   without a live cluster, and how it fits the snapshot model.
4. **`state` label vs separate metric names** — the separate names are a 1:1
   mirror of the API fields (`numGoodNodes` → `ecs_cluster_good_nodes`) under
   ADR-0006 naming, not a modelling decision. Agreed that `{state}` is better
   Prometheus practice; it is a breaking rename, so a v3 candidate. Their
   observation holds: the three cluster buckets can sum below `numNodes` when a
   node sits in Suspect or NotAccessible.
5. **Ask** — ADR-0008 tracks three live-verify items frozen since 2026-06-14 for
   want of a cluster. `ecsclient.Config.Trace` on their 4.3 would close all three.
   F1 is HIGH severity and may already be silently emptying their namespace
   metering metrics.

## Open questions

None blocking. ADR-0011 (Flux) is deferred pending a contributor proposal.
