# Tolerant HAL List Decoding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decode the nodes and replication-group HAL lists under either `_embedded._instances` or `_embedded.instances`, and log a warning when neither key is present, so a payload-shape change can never again silently empty a collector while it reports healthy.

**Architecture:** A single generic wrapper type `halList[T]` in `internal/ecs/hal.go` implements a pointer-receiver `UnmarshalJSON` that tries both key spellings and records whether either was seen. `localZoneNodesResp` and `replicationGroupsResp` embed it in place of their current anonymous nested structs; their collect loops are unchanged. This mirrors the existing `Num` type in `internal/ecs/points.go`, which already tolerates ECS payload variance the same way.

**Tech Stack:** Go 1.26.5, standard-library `encoding/json`, `github.com/sirupsen/logrus` (imported as `log`), standard `testing`.

**Spec:** `docs/superpowers/specs/2026-07-27-tolerant-hal-decode-design.md`

## Global Constraints

- Go 1.26.5 (`go.mod:3`). Generics are available; no build-tag gymnastics needed.
- **No new, renamed, or re-labelled metrics.** The emitted series set must stay byte-identical. `TestLabelKeyConsistency` must keep passing (ADR-0006).
- **No inline `nosemgrep` or `//nolint` suppressions** — restructure instead. Semgrep blocks on findings.
- Unparseable or missing values yield **absent** samples, never zeros (ADR-0007).
- `internal/ecs/testdata/` and `cmd/mockecs/fixtures/` must stay in sync. This plan changes **no fixture file**, so nothing to sync — do not add fixture files.
- Every task ends with `go test ./internal/ecs/...` green. The final task runs the full `make ci` gate.
- Branch `fix/tolerant-hal-decode` already exists, forked from `origin/main` (commit `369db05`, the PR #18 merge), with the spec committed as `d9dbef1`. Work on that branch; do not branch again.

## Deltas from the spec found while planning

Both discovered by reading the merged tree; the spec was written before these were checked.

1. **`docs/metrics.md` already lists all five health states.** Spec documentation item (a) — CodeRabbit r3639388941 — was already fixed inside PR #18 by commit `f474f1a`. `docs/metrics.md:57` already reads `good` / `suspect` / `bad` / `notaccessible` / `maintenance`. **That work is dropped from this plan.** Only the "may be absent" note remains.
2. **`CHANGELOG.md` has no `## [2.7.0]` section.** The tag `v2.7.0` (2026-07-26) points at merge commit `369db05`, but the PR #18 entries still sit under `## [Unreleased]` (`CHANGELOG.md:7-34`). Task 5 promotes that block to a real `[2.7.0]` heading before adding `[2.7.1]`.
3. **Controller ruling, 2026-07-27 — the warning is a shared helper, not an inlined block.** The plan first had Tasks 2 and 3 each inline the same three-line `if !r.Embedded.KeySeen { log… }` block. That is verbatim duplication of a logic block, which the review rubric treats as a defect. Ruling: `hal.go` owns a `warnUnknownHalShape(cluster, path string, keySeen bool)` helper and both collectors call it in one line. `hal.go` therefore imports logrus; `nodes.go` and `replication.go` do not. Tasks 1-3 below already reflect this.
4. **Deviation from the spec on one CHANGELOG point.** The spec says the "supporting both keys is under discussion" sentence "becomes resolved". That sentence is inside content already shipped as v2.7.0, so this plan leaves the released text verbatim and states the resolution in the new `[2.7.1]` entry instead. Rewriting a released changelog section hides what users of v2.7.0 actually got.

---

### Task 1: `halList[T]` decoder

**Files:**
- Create: `internal/ecs/hal.go`
- Test: `internal/ecs/hal_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type halList[T any] struct { Instances []T; KeySeen bool }` with method `func (h *halList[T]) UnmarshalJSON(b []byte) error`, plus `func warnUnknownHalShape(cluster, path string, keySeen bool)`. Tasks 2 and 3 embed the type and call the helper.

- [ ] **Step 1: Write the failing test**

Create `internal/ecs/hal_test.go`:

```go
package ecs

import (
	"encoding/json"
	"testing"
)

// halTestItem is a minimal element type: halList must not care what T is.
type halTestItem struct {
	Name string `json:"name"`
}

func TestHalListUnmarshal(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		wantNames []string
		wantSeen  bool
	}{
		{
			name:      "underscore key, as real clusters emit",
			payload:   `{"_instances":[{"name":"a"},{"name":"b"}]}`,
			wantNames: []string{"a", "b"},
			wantSeen:  true,
		},
		{
			name:      "documented key without underscore",
			payload:   `{"instances":[{"name":"a"},{"name":"b"}]}`,
			wantNames: []string{"a", "b"},
			wantSeen:  true,
		},
		{
			// An empty list is a legitimately empty cluster, not shape drift:
			// the key was seen, so no warning must be triggered downstream.
			name:      "empty underscore list still counts as a key sighting",
			payload:   `{"_instances":[]}`,
			wantNames: nil,
			wantSeen:  true,
		},
		{
			name:      "neither key present",
			payload:   `{"_links":{"self":{"href":"/x"}}}`,
			wantNames: nil,
			wantSeen:  false,
		},
		{
			name:      "both keys present, underscore wins",
			payload:   `{"_instances":[{"name":"real"}],"instances":[{"name":"doc"}]}`,
			wantNames: []string{"real"},
			wantSeen:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got halList[halTestItem]
			if err := json.Unmarshal([]byte(tc.payload), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.KeySeen != tc.wantSeen {
				t.Errorf("KeySeen = %v, want %v", got.KeySeen, tc.wantSeen)
			}
			if len(got.Instances) != len(tc.wantNames) {
				t.Fatalf("got %d instances, want %d", len(got.Instances), len(tc.wantNames))
			}
			for i, want := range tc.wantNames {
				if got.Instances[i].Name != want {
					t.Errorf("instance %d name = %q, want %q", i, got.Instances[i].Name, want)
				}
			}
		})
	}
}

func TestHalListRejectsMalformedList(t *testing.T) {
	var got halList[halTestItem]
	err := json.Unmarshal([]byte(`{"_instances":"not-a-list"}`), &got)
	if err == nil {
		t.Fatal("want a decode error when _instances is not an array, got nil")
	}
}

func TestWarnUnknownHalShape(t *testing.T) {
	tests := []struct {
		name     string
		keySeen  bool
		wantLogs int
	}{
		{name: "key seen stays silent", keySeen: true, wantLogs: 0},
		{name: "key missing warns once", keySeen: false, wantLogs: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hook := test.NewGlobal()
			defer hook.Reset()

			warnUnknownHalShape("test-cluster", "/dashboard/zones/localzone/nodes", tc.keySeen)

			if got := len(hook.Entries); got != tc.wantLogs {
				t.Fatalf("got %d log entries, want %d", got, tc.wantLogs)
			}
			if tc.wantLogs == 0 {
				return
			}
			entry := hook.LastEntry()
			if entry.Level != logrus.WarnLevel {
				t.Errorf("level = %v, want warning", entry.Level)
			}
			if entry.Data["path"] != "/dashboard/zones/localzone/nodes" {
				t.Errorf("path field = %v, want the endpoint path", entry.Data["path"])
			}
			if entry.Data["cluster"] != "test-cluster" {
				t.Errorf("cluster field = %v, want the cluster name", entry.Data["cluster"])
			}
		})
	}
}
```

The import block for this file is:

```go
import (
	"encoding/json"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
)
```

`logrus/hooks/test` is a subpackage of the logrus module already required at
`go.mod:10` (v1.9.4) — no dependency is added, but run `go mod tidy` if the
build complains and confirm `go.mod` still lists logrus as a direct requirement
and nothing else changed.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ecs/ -run TestHalList -v`

Expected: FAIL — compile error `undefined: halList`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/ecs/hal.go`:

```go
package ecs

import (
	"encoding/json"

	log "github.com/sirupsen/logrus"
)

// halList decodes a HAL "_embedded" instance list.
//
// Real ECS/ObjectScale clusters key the array "_instances" (underscore) —
// field-confirmed from ECS 3.8 through ObjectScale 4.3. The Dell REST API
// reference examples show it without the underscore ("instances"). The bundled
// swagger cannot arbitrate: every response body in it declares an empty schema
// (see ADR-0008), so neither form can be proven from the spec.
//
// Both keys are therefore accepted. Picking only one is not a cosmetic choice:
// a key mismatch decodes zero instances and returns no error, so the collector
// emits no samples while ecs_collector_up still reports 1 — the worst failure
// mode this exporter has, and the bug fixed in v2.7.0.
type halList[T any] struct {
	// Instances holds the decoded array, empty when the payload carried none.
	Instances []T
	// KeySeen reports whether either spelling of the array key was present.
	// False means the payload shape is unrecognised, which callers surface as
	// a warning; it is distinct from a present-but-empty list.
	KeySeen bool
}

// UnmarshalJSON accepts either spelling of the instance-array key, preferring
// the "_instances" form that real clusters emit when both are present.
func (h *halList[T]) UnmarshalJSON(b []byte) error {
	var raw struct {
		Underscore []T `json:"_instances"`
		Documented []T `json:"instances"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	// Presence is tested against nil rather than length: an empty array is a
	// legitimately empty cluster and must still count as a key sighting.
	switch {
	case raw.Underscore != nil:
		h.Instances, h.KeySeen = raw.Underscore, true
	case raw.Documented != nil:
		h.Instances, h.KeySeen = raw.Documented, true
	}
	return nil
}

// warnUnknownHalShape logs when a HAL payload carried neither spelling of the
// instance-array key, so an unrecognised shape leaves a trace instead of
// silently yielding zero instances. An empty-but-present list is not a warning:
// that is a legitimately empty cluster.
//
// This is deliberately a warning and not an error: a build that omits
// "_embedded" entirely on an empty cluster would be indistinguishable from
// shape drift, and a false ecs_collector_up=0 is worse than a missed alert.
//
// The cluster is included because the exporter polls many clusters per cycle; a
// warning naming only the endpoint cannot tell an operator which one drifted.
func warnUnknownHalShape(cluster, path string, keySeen bool) {
	if keySeen {
		return
	}
	log.WithFields(log.Fields{"cluster": cluster, "path": path}).
		Warn("HAL instance list key not found (_instances/instances); payload shape may have changed")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ecs/ -run 'TestHalList|TestWarnUnknownHalShape' -v`

Expected: PASS — 5 subtests under `TestHalListUnmarshal`, `TestHalListRejectsMalformedList`, and 2 subtests under `TestWarnUnknownHalShape`.

- [ ] **Step 5: Commit**

```bash
git add internal/ecs/hal.go internal/ecs/hal_test.go
git commit -m "feat(ecs): tolerant HAL list decoder accepting either instances key

Real clusters emit _embedded._instances; the Dell reference documents
_embedded.instances. The bundled swagger declares empty response schemas
throughout (ADR-0008) and cannot settle it, so accept both and record whether
either key was seen."
```

---

### Task 2: Wire the nodes collector to `halList`

**Files:**
- Modify: `internal/ecs/nodes.go:12-68`
- Test: `internal/ecs/nodes_test.go`

**Interfaces:**
- Consumes: `halList[T]` from Task 1 (`Instances []T`, `KeySeen bool`).
- Produces: `type nodeInstance struct{…}` — the named per-node payload struct, previously anonymous. `localZoneNodesResp.Embedded` becomes `halList[nodeInstance]`. No exported surface changes.

- [ ] **Step 1: Write the failing test**

Append to `internal/ecs/nodes_test.go`:

```go
// TestNodesCollectDocumentedInstancesKey serves the real fixture with the HAL
// array key rewritten to the spelling the Dell reference documents. The
// decoder must tolerate both, so the resulting samples are identical.
//
// The payload is derived from the fixture at test time on purpose: a second
// fixture file could drift from the first, and cmd/mockecs/fixtures/ would
// have to mirror it.
func TestNodesCollectDocumentedInstancesKey(t *testing.T) {
	mc := mockClient(t)
	mc.Responses[pathLocalZoneNodes] = strings.ReplaceAll(
		mc.Responses[pathLocalZoneNodes], `"_instances"`, `"instances"`)

	samples, err := Nodes{}.Collect(context.Background(), mc)
	if err != nil {
		t.Fatal(err)
	}

	n1 := Label{"node", "supr01-r01"}
	mustSample(t, samples, "ecs_node_healthy", 1, n1)
	mustSample(t, samples, "ecs_node_health_state", 1, n1, Label{"state", "good"})
	mustSample(t, samples, "ecs_node_disks", 40, n1)
	mustSample(t, samples, "ecs_node_disk_space_total_bytes", 510, n1)
	mustSample(t, samples, "ecs_node_cpu_utilization_percent", 43, n1)

	n2 := Label{"node", "supr01-r02"}
	mustSample(t, samples, "ecs_node_healthy", 0, n2)
	mustSample(t, samples, "ecs_node_health_state", 1, n2, Label{"state", "bad"})
	mustSample(t, samples, "ecs_node_bad_disks", 1, n2)
}
```

Add `"strings"` to the import block at `internal/ecs/nodes_test.go:3-6`, so it reads:

```go
import (
	"context"
	"strings"
	"testing"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ecs/ -run TestNodesCollectDocumentedInstancesKey -v`

Expected: FAIL — the current decoder only reads `_instances`, so it finds zero nodes and every `mustSample` reports a missing sample (first failure: `ecs_node_healthy`).

- [ ] **Step 3: Write the implementation**

In `internal/ecs/nodes.go`, replace the type block at lines 12-51 with a named element type plus the `halList` wrapper:

```go
// localZoneNodesResp models GET /dashboard/zones/localzone/nodes (OBS 4.1): a
// HAL-style list of per-node dashboard instances. See halList for why both
// spellings of the array key are accepted.
type localZoneNodesResp struct {
	Embedded halList[nodeInstance] `json:"_embedded"`
}

// nodeInstance is one per-node entry of the local-zone dashboard payload.
type nodeInstance struct {
	DisplayName  string `json:"displayName"`
	HealthStatus string `json:"healthStatus"`

	NumDisks               Num `json:"numDisks"`
	NumGoodDisks           Num `json:"numGoodDisks"`
	NumBadDisks            Num `json:"numBadDisks"`
	NumMaintenanceDisks    Num `json:"numMaintenanceDisks"`
	NumReadyToReplaceDisks Num `json:"numReadyToReplaceDisks"`

	DiskSpaceTotal     Series `json:"diskSpaceTotal"`
	DiskSpaceFree      Series `json:"diskSpaceFree"`
	DiskSpaceAllocated Series `json:"diskSpaceAllocated"`

	NodeCPUUtilization         Series `json:"nodeCpuUtilization"`
	NodeMemoryUtilization      Series `json:"nodeMemoryUtilization"`
	NodeMemoryUtilizationBytes Series `json:"nodeMemoryUtilizationBytes"`

	NodeNicReceivedBandwidth    Series `json:"nodeNicReceivedBandwidth"`
	NodeNicTransmittedBandwidth Series `json:"nodeNicTransmittedBandwidth"`
	NodeNicUtilization          Series `json:"nodeNicUtilization"`

	TransactionReadLatency             Series `json:"transactionReadLatency"`
	TransactionWriteLatency            Series `json:"transactionWriteLatency"`
	TransactionReadBandwidth           Series `json:"transactionReadBandwidth"`
	TransactionWriteBandwidth          Series `json:"transactionWriteBandwidth"`
	TransactionReadTransactionsPerSec  Series `json:"transactionReadTransactionsPerSec"`
	TransactionWriteTransactionsPerSec Series `json:"transactionWriteTransactionsPerSec"`
}
```

The import block is unchanged: the warning lives in the Task 1 helper, so this
file does not import logrus.

Inside `Collect`, insert the shape check immediately after the `c.Get` error
check (currently `nodes.go:64-66`) and before `var out []Sample`:

```go
	warnUnknownHalShape(c.Name(), pathLocalZoneNodes, r.Embedded.KeySeen)
```

Leave the loop body untouched — `r.Embedded.Instances` still resolves, now through the wrapper.

- [ ] **Step 4: Run the package tests to verify they pass**

Run: `go test ./internal/ecs/ -run 'TestNodes|TestHalList|TestLabelKeyConsistency' -v`

Expected: PASS — both `TestNodesCollect` (underscore fixture) and `TestNodesCollectDocumentedInstancesKey` (rewritten key) green, label-key invariant intact.

- [ ] **Step 5: Commit**

```bash
git add internal/ecs/nodes.go internal/ecs/nodes_test.go
git commit -m "fix(ecs): decode node HAL list under either instances key

Warns when neither key is present, so a future shape change leaves a trace
instead of silently emptying the collector."
```

---

### Task 3: Wire the replication collector to `halList`

**Files:**
- Modify: `internal/ecs/replication.go:11-44`
- Test: `internal/ecs/replication_test.go`

**Interfaces:**
- Consumes: `halList[T]` from Task 1.
- Produces: `type replicationGroupInstance struct{…}` — the named per-group payload struct, previously anonymous. `replicationGroupsResp.Embedded` becomes `halList[replicationGroupInstance]`.

- [ ] **Step 1: Write the failing test**

Append to `internal/ecs/replication_test.go`:

```go
// TestReplicationCollectDocumentedInstancesKey serves the real fixture with the
// HAL array key rewritten to the spelling the Dell reference documents. The
// decoder must tolerate both, so the resulting samples are identical.
func TestReplicationCollectDocumentedInstancesKey(t *testing.T) {
	mc := mockClient(t)
	mc.Responses[pathReplicationGroups] = strings.ReplaceAll(
		mc.Responses[pathReplicationGroups], `"_instances"`, `"instances"`)

	samples, err := Replication{}.Collect(context.Background(), mc)
	if err != nil {
		t.Fatal(err)
	}

	rg1 := Label{"rg", "rg_name1"}
	mustSample(t, samples, "ecs_replication_group_ingress_traffic", 12000, rg1)
	mustSample(t, samples, "ecs_replication_group_egress_traffic", 9500, rg1)
	mustSample(t, samples, "ecs_replication_group_rpo_lag_seconds", 7200, rg1)
	mustSample(t, samples, "ecs_replication_group_zones", 3, rg1)

	rg2 := Label{"rg", "rg_name2"}
	mustSample(t, samples, "ecs_replication_group_ingress_traffic", 100, rg2)
	mustSample(t, samples, "ecs_replication_group_zones", 2, rg2)
}
```

Add `"strings"` to the import block at `internal/ecs/replication_test.go:3-6`, so it reads:

```go
import (
	"context"
	"strings"
	"testing"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ecs/ -run TestReplicationCollectDocumentedInstancesKey -v`

Expected: FAIL — zero groups decoded, first failure on `ecs_replication_group_ingress_traffic`.

- [ ] **Step 3: Write the implementation**

In `internal/ecs/replication.go`, replace the type block at lines 11-29 with:

```go
// replicationGroupsResp models GET /dashboard/zones/localzone/replicationgroups
// (OBS 4.1): a HAL-style list of per-replication-group instances. See halList
// for why both spellings of the array key are accepted.
type replicationGroupsResp struct {
	Embedded halList[replicationGroupInstance] `json:"_embedded"`
}

// replicationGroupInstance is one per-group entry of the dashboard payload.
type replicationGroupInstance struct {
	Name                                     string `json:"name"`
	NumZones                                 Num    `json:"numZones"`
	ReplicationIngressTraffic                Series `json:"replicationIngressTraffic"`
	ReplicationEgressTraffic                 Series `json:"replicationEgressTraffic"`
	ChunksRepoPendingReplicationTotalSize    Num    `json:"chunksRepoPendingReplicationTotalSize"`
	ChunksJournalPendingReplicationTotalSize Num    `json:"chunksJournalPendingReplicationTotalSize"`
	ChunksPendingXorTotalSize                Num    `json:"chunksPendingXorTotalSize"`
	ReplicationRpoTimestamp                  Num    `json:"replicationRpoTimestamp"`
	ReplicationRpoLag                        Num    `json:"replicationRpoLag"`
}
```

The import block is unchanged: the warning lives in the Task 1 helper, so this
file does not import logrus.

Insert the shape check inside `Collect`, immediately after the `c.Get` error check (currently `replication.go:40-42`) and before `var out []Sample`:

```go
	warnUnknownHalShape(c.Name(), pathReplicationGroups, r.Embedded.KeySeen)
```

Leave the loop body untouched.

- [ ] **Step 4: Run the package tests to verify they pass**

Run: `go test ./internal/ecs/... -race`

Expected: PASS — the whole `ecs` package, including the Prometheus and OTLP export-path tests.

- [ ] **Step 5: Commit**

```bash
git add internal/ecs/replication.go internal/ecs/replication_test.go
git commit -m "fix(ecs): decode replication-group HAL list under either instances key"
```

---

### Task 4: Correct and extend the ADRs

**Files:**
- Modify: `docs/adr/0007-obs-4-1-api-alignment.md:22-25`, `:29-34`, `:42-43`
- Modify: `docs/adr/0008-swagger-4.2-validation-findings.md:10-17`, `:69-73`

No test — documentation only. Verified by the docs build in Task 7.

- [ ] **Step 1: Fix the wrong key in the ADR-0007 decision text**

`docs/adr/0007-obs-4-1-api-alignment.md:22-25` currently says `_embedded.instances[]`, which no cluster emits. Replace that bullet with:

```markdown
- **Documented node stats**: per-node metrics come from `GET
  /dashboard/zones/localzone/nodes` (`_embedded._instances[]`) through the
  management port — replacing v1's undocumented node-local scraping as the default
  node-metric source.
```

- [ ] **Step 2: Record the tolerant-decode decision**

In the same file, immediately after the **Defensive payload parsing** bullet (ends at line 34, `never zeros.`), insert:

```markdown
- **Tolerant HAL list decoding**: the nodes and replication-group endpoints return
  their arrays under `_embedded._instances`, which is what real clusters emit (ECS
  3.8 through ObjectScale 4.3, field-confirmed). The Dell reference examples show
  `_embedded.instances` instead, and the bundled swagger cannot arbitrate — every
  response body in it declares an empty schema (ADR-0008). Both spellings are
  therefore accepted by a shared `halList[T]` decoder, which also records whether
  either key was present so an unrecognised shape logs a warning instead of
  silently yielding zero instances.
```

- [ ] **Step 3: Correct the stale fixture claim**

`docs/adr/0007-obs-4-1-api-alignment.md:42-43` claims the fixtures mirror the 4.1 reference examples. Since v2.7.0 they mirror real cluster payloads, which is exactly where the two diverge. Replace that bullet with:

```markdown
- The fixture suite mirrored the 4.1 reference examples until v2.7.0, when it was
  realigned to payloads captured from a live ObjectScale 4.3 cluster. Where the two
  disagree — notably the HAL list key — the captured shape wins, because the
  reference was never observed on hardware.
```

- [ ] **Step 4: Note the HAL key in ADR-0008 and record the live-cluster opening**

In `docs/adr/0008-swagger-4.2-validation-findings.md`, append to the second bullet of **Context** (the empty-schema bullet ending at line 17, `checked against the spec.`):

```markdown
  A concrete instance surfaced in v2.7.0: the nodes and replication-group HAL
  arrays are keyed `_instances` on real clusters and `instances` in the reference
  examples, and the swagger contains neither — its only `instances` token is the
  unrelated path `/vdc/instances/storageservers`.
```

Then append to **Consequences** (after line 73):

```markdown
- A live ObjectScale 4.3 cluster became reachable through the PR #18 contributor in
  July 2026. F1 in particular should be settled there: if the swagger's wrapped
  billing body is required, every namespace metering metric is silently absent on
  every deployment, and no test can catch it because `cmd/mockecs` does not
  validate request bodies.
```

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0007-obs-4-1-api-alignment.md docs/adr/0008-swagger-4.2-validation-findings.md
git commit -m "docs(adr): correct HAL list key and record tolerant-decode decision

ADR-0007 documented _embedded.instances[], which no cluster emits. Also notes
that the fixture suite has mirrored real 4.3 payloads since v2.7.0, and records
in ADR-0008 that a live cluster is now reachable for the frozen F1/F2/F3 items."
```

---

### Task 5: Changelog and metric-availability note

**Files:**
- Modify: `CHANGELOG.md:7`
- Modify: `docs/metrics.md:65-66`

No test — documentation only.

- [ ] **Step 1: Promote the released v2.7.0 block**

`CHANGELOG.md:7` reads `## [Unreleased]` but the block below it shipped as v2.7.0 (tag dated 2026-07-26). Change that single line to:

```markdown
## [2.7.0] - 2026-07-26
```

Leave the body of the section verbatim, including the sentence "Some clusters may follow the documented `instances` form; supporting both keys is under discussion." It describes what v2.7.0 actually shipped; the resolution belongs in the next entry.

- [ ] **Step 2: Add the v2.7.1 entry**

Insert above the line you just changed, so it becomes the top section:

```markdown
## [2.7.1] - 2026-07-27

### Fixed
- The node and replication-group collectors now decode the HAL instance list
  under **either** `_embedded._instances` (what real ECS/ObjectScale clusters
  emit, confirmed from 3.8 through 4.3) or `_embedded.instances` (what the Dell
  REST API reference examples show), resolving the open question left in 2.7.0.
  Accepting only one spelling meant a cluster using the other emitted no
  `ecs_node_*` or `ecs_replication_group_*` metrics while `ecs_collector_up` still
  reported `1`. When neither key is present the collector now logs a warning, so
  a future payload change leaves a trace instead of failing silently.

```

- [ ] **Step 3: Document that some dashboard fields may be absent**

In `docs/metrics.md`, insert immediately after the nodes table (after line 65, `| ecs_node_transactions_read_per_second …`, and before the `## Namespaces` heading):

```markdown

!!! note "Availability varies by cluster and version"
    `ecs_node_cpu_utilization_percent`, `ecs_node_memory_*`, `ecs_node_nic_*` and
    the cluster-level `ecs_cluster_transaction*` metrics come from dashboard fields
    the API reference documents but that some clusters do not populate — their
    absence was confirmed at raw-API level on ObjectScale 4.3, where the keys are
    simply not present in the response. Missing fields yield absent series, never
    zeros, so these metrics may not appear at all on your cluster.
```

The `admonition` extension is enabled in `mkdocs.yml:12`, so `!!!` renders.

- [ ] **Step 4: Verify the docs build**

Run: `uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict`

Expected: build succeeds with no warnings escalated to errors.

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md docs/metrics.md
git commit -m "docs: v2.7.1 changelog; flag dashboard fields that clusters may omit

Also promotes the stale [Unreleased] heading to [2.7.0], which was tagged on
2026-07-26 without the section being renamed."
```

---

### Task 6: Draft the reply to the PR #18 contributor

**Files:**
- Create: `<scratchpad>/pr18-reply.md` (the session scratchpad directory; the file is deliberately outside the repository)

Written to the scratchpad, not the repo: it is a GitHub comment, not project content. The maintainer posts it.

- [ ] **Step 1: Write the draft**

Create the file with this content, addressing the five points from the spec in the contributor's own ordering:

```markdown
Thanks for the detailed follow-up, and for validating against real hardware — that
turned out to be the only usable evidence on the first point.

**1. `_instances` vs `instances` — no regression here, and both are now accepted.**

There is no live cluster on this side. The `instances` spelling came from the Dell
4.1 REST reference examples, which is also where the fixture suite came from
(ADR-0007). No cluster was ever observed emitting it.

I also went back to the bundled swagger (`docs/swagger/6972-4.1.0.json`) to settle
it, and it cannot: 306 of its 309 operations declare an empty response schema
(`{"type":"object","properties":{}}`), three declare none, and `components.schemas`
is empty. The only `instances` token in the whole file is the unrelated path
`/vdc/instances/storageservers`. ADR-0008 already recorded this limitation —
response-field mappings "remain fixture-derived and cannot be checked against the
spec" — I had just not connected it to this key.

So your `_instances` is the only form with real-world evidence behind it, and
v2.7.0 is not broken for me. That said, CodeRabbit's point stands: pinning one
spelling recreates the exact silent-empty failure mode, so v2.7.1 accepts both via
a shared decoder, and logs a warning when neither key is present. I have taken that
on rather than asking you for a follow-up PR — it is about 60 lines. ADR-0007 also
carried the wrong key in its decision text (`_embedded.instances[]`); that is fixed.

**2. Performance fields absent from the dashboard.**

I have never seen them populated, and cannot check. Your raw-API confirmation is
the only data point that exists, so I am treating it as the record:
`docs/metrics.md` now flags per-node CPU / memory / NIC and cluster
`transaction*` as fields some clusters do not populate.

**3. Flux API.**

Open in principle, not out of scope on the merits — but it needs an ADR before
code, since it adds a second data source with its own auth and dependency. If you
want to propose ADR-0011, the questions worth answering are: InfluxDB auth and
config surface, bucket + measurement → metric mapping, whether it belongs inside
obs_exporter or as a separate exporter, how it gets tested without a live cluster,
and how it fits the snapshot model (ADR-0002).

**4. Count-by-state: labels vs separate metric names.**

Honest answer: the separate names are a 1:1 mirror of the API fields
(`numGoodNodes` → `ecs_cluster_good_nodes`) under the ADR-0006 naming rule, not a
considered modelling decision. I agree `ecs_cluster_nodes{state="…"}` is the better
Prometheus shape, and that keeping `state="all"` would be worth it. It is a
breaking rename, so it is a v3 item rather than something to slip into a patch.
Your doc-supported point is right too: the three cluster buckets can sum below
`numNodes` when a node sits in Suspect or NotAccessible.

**5. A request, if you have the cycles.**

ADR-0008 tracks three findings that have been frozen since 2026-06-14 purely
because no live cluster was available, and you have one. The client has a `Trace`
mode (`ecsclient.Config.Trace`) that logs method, path, status and body without
leaking the auth token:

- **F1 (HIGH)** — the exporter sends `{"id":[...]}` to
  `POST /object/billing/namespace/info`; the swagger documents
  `{"namespace_list":{"id":[...]}}`. If the swagger is right, every namespace
  metering metric (`ecs_namespace_used_bytes`, `_objects`, `_mpu_*`) is silently
  absent — and no test catches it, because `cmd/mockecs` does not validate request
  bodies. This may already be affecting you.
- **F2 (MEDIUM)** — does `GET /vdc/nodes` still resolve, or 404? The 4.2 swagger
  lists `/vdc/vdc/nodes` instead. If it 404s, `ecs_cluster_info` and the whole DT
  collector are dead.
- **F3 (LOW)** — does the billing endpoint accept a JSON request body? The swagger
  documents `application/xml`.

Any of the three would be useful; F1 most of all.
```

- [ ] **Step 2: Surface it to the maintainer**

Send the file with `SendUserFile` (or print the path) so it can be reviewed and
pasted into the PR thread. Nothing to commit.

---

### Task 7: Full gate, push, and pull request

**Files:** none modified.

- [ ] **Step 1: Run the full CI gate**

Run: `make ci`

Expected: PASS — `fmt-check`, `vet`, `golangci-lint`, `go test -race`, `govulncheck`, and `build` all green. If `golangci-lint` flags the new generic type, fix it by restructuring; **do not** add a `//nolint` comment.

- [ ] **Step 2: Confirm the metric set is unchanged**

Run: `go test ./internal/ecs/ -run 'TestLabelKeyConsistency|TestPrometheus|TestOTLP' -v`

Expected: PASS. This is the guard on the "no new, renamed, or re-labelled metrics" constraint — both export paths gather the same series as before.

- [ ] **Step 3: Push the branch**

```bash
git push -u origin fix/tolerant-hal-decode
```

- [ ] **Step 4: Open the pull request**

```bash
gh pr create --title "fix(ecs): accept either HAL instances key; v2.7.1" --body "$(cat <<'EOF'
Follow-up to #18. Design: `docs/superpowers/specs/2026-07-27-tolerant-hal-decode-design.md`.

The nodes and replication-group collectors now decode the HAL instance list under
either `_embedded._instances` (emitted by real clusters, confirmed 3.8→4.3) or
`_embedded.instances` (the Dell reference spelling), and log a warning when
neither key is present. Addresses CodeRabbit r3639388915.

No metric added, renamed, or re-labelled — decoding robustness only.

Also corrects ADR-0007, which documented the wrong key, and records in ADR-0008
that the bundled swagger cannot arbitrate payload shapes (306 of 309 operations
declare an empty response schema).

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01P9vAnqpQXacSLWcgudSwcb
EOF
)"
```

- [ ] **Step 5: Report the PR URL**

Print the URL `gh pr create` returns, along with the `make ci` result, so the maintainer can post the Task 6 reply on #18 alongside it.

---

## Self-review notes

- **Spec coverage:** architecture → Task 1; components → Tasks 2-3; error handling → Tasks 2-3 Step 3; testing → Tasks 1-3 plus Task 7 Step 2; documentation → Tasks 4-5; reply → Task 6; delivery → Task 7. The spec's `docs/metrics.md` five-health-states item has no task because it was already done in `f474f1a` (recorded under "Deltas" above).
- **Type consistency:** `halList[T]` with fields `Instances` and `KeySeen` is defined in Task 1 and used under those exact names in Tasks 2 and 3. Element types `nodeInstance` and `replicationGroupInstance` are each defined in the task that introduces them.
- **Non-goals confirmed absent:** no Flux collector, no `state`-label rename, no blind F1/F2/F3 fix. Task 4 Step 4 and Task 6 only *ask* for live verification.
