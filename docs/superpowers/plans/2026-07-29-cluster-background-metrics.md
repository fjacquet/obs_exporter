# Cluster Background-Process Metrics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Export garbage-collection, recovery, erasure-coding and allocation-breakdown metrics from the local-zone dashboard response the exporter already fetches once per cycle.

**Architecture:** `localZoneResp` in `internal/ecs/cluster.go` embeds four anonymous structs, one per family. Go promotes embedded struct fields during JSON decoding, so the flat payload decodes unchanged. Each family lives in its own file with its fields and a pure `samples() []Sample` method side by side; `Cluster.Collect` concatenates the four. No new endpoint, no new API call, no feature flag.

**Tech Stack:** Go 1.26.5, standard-library `encoding/json`, standard `testing`, Grafana dashboard JSON (schemaVersion 39).

**Spec:** `docs/superpowers/specs/2026-07-29-cluster-background-metrics-design.md`

## Global Constraints

- Go 1.26.5 (`go.mod:3`).
- **Naming (ADR-0006):** `ecs_<object>_<metric>[_<unit>]`. Unit suffix **only** where the API documents a unit. `recovery_rate`, `ec_rate` and both `complete_time_estimate` metrics carry no documented unit and therefore **no unit suffix** — matching the existing `ecs_cluster_replication_ingress_traffic`.
- **Label-key invariant (ADR-0006):** one metric name = one ordered label-key set. GC metrics carry `scope`, the allocation metric carries `purpose`, everything else carries none (the collection loop adds `cluster` to all of them). `TestLabelKeyConsistency` must stay green.
- **Absent, never zero (ADR-0007):** a missing or unparseable field yields no sample. Never substitute `0`. This is the single most important rule in this plan: `ecs_cluster_gc_pending_bytes` at 0 means "nothing to reclaim", absence means "this cluster does not report GC".
- **No `combined` scope.** `gcCombined*` is exactly `user + system` (verified to the byte on a live 4.3 cluster). It is not decoded and not exported; `sum without(scope)` reproduces it.
- **No inline `nosemgrep` or `//nolint` suppressions** — restructure instead. Semgrep blocks on findings.
- **Fixture sync:** every edit to `internal/ecs/testdata/*.json` must be mirrored byte-for-byte into `cmd/mockecs/fixtures/` with the same name (CLAUDE.md rule).
- Every task ends with `go test ./internal/ecs/...` green. The final task runs `make ci` and the strict docs build.
- Branch `feat/cluster-background-metrics` already exists off `main` @ v2.7.1, with the spec committed as `e802da7`. Work on that branch; do not branch again.

## Deltas from the spec found while planning

1. **The live-payload fixture needs almost no sanitization.** The spec says to replace `storagePoolId` and the version build hash. Both were checked: the **local-zone** payload contains no UUID and no build-hash string — those live in the *nodes* payload (`storagePoolId`) and `/vdc/nodes` (`version`), neither of which this fixture comes from. Its `name` is already sanitized to `vdc-r-cluster-1` in the supplied trace. Task 6 therefore only normalizes `name`; there is nothing else to strip. Do not skip the verification step that proves this.
2. **The Grafana change is a new dashboard, not an edit.** The repo has six `obs-*` dashboards, each scoped to a subject (`obs-overview`, `obs-nodes`, `obs-replication`, …). Four new families do not belong in any of them, so Task 8 adds `grafana/dashboards/obs-storage-internals.json` following the existing conventions rather than growing a redesigned dashboard.

---

### Task 1: `Bool` tolerant boolean primitive

**Files:**
- Modify: `internal/ecs/points.go` (append after `Num.UnmarshalJSON`, which ends at line 68)
- Test: `internal/ecs/points_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Bool struct { Val bool; Set bool }` with `func (b *Bool) UnmarshalJSON(raw []byte) error`. Task 2 uses it for the two GC enable flags.

- [ ] **Step 1: Write the failing test**

Append to `internal/ecs/points_test.go`:

```go
func TestBoolUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantVal bool
		wantSet bool
	}{
		{name: "quoted true, as the dashboard sends it", payload: `"true"`, wantVal: true, wantSet: true},
		{name: "quoted false", payload: `"false"`, wantVal: false, wantSet: true},
		{name: "native JSON true", payload: `true`, wantVal: true, wantSet: true},
		{name: "native JSON false", payload: `false`, wantVal: false, wantSet: true},
		{name: "mixed case", payload: `"True"`, wantVal: true, wantSet: true},
		{name: "N/A leaves it unset", payload: `"N/A"`, wantSet: false},
		{name: "empty string leaves it unset", payload: `""`, wantSet: false},
		{name: "null leaves it unset", payload: `null`, wantSet: false},
		{name: "unrecognised word leaves it unset", payload: `"maybe"`, wantSet: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got Bool
			if err := json.Unmarshal([]byte(tc.payload), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Set != tc.wantSet {
				t.Fatalf("Set = %v, want %v", got.Set, tc.wantSet)
			}
			if got.Set && got.Val != tc.wantVal {
				t.Errorf("Val = %v, want %v", got.Val, tc.wantVal)
			}
		})
	}
}
```

If `internal/ecs/points_test.go` does not already import `encoding/json`, add it to that
file's import block.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ecs/ -run TestBoolUnmarshal -v`

Expected: FAIL — compile error `undefined: Bool`.

- [ ] **Step 3: Write the implementation**

Append to `internal/ecs/points.go`, after `Num.UnmarshalJSON` (which ends at line 68) and before `anyToFloat`:

```go
// Bool is a flag the ECS API encodes as a quoted string ("true"/"false"), which
// Num deliberately refuses. Unparseable values (including "N/A", "", null) leave
// Set false rather than failing the whole decode, so a flag the cluster does not
// report yields an absent sample rather than a misleading false.
type Bool struct {
	Val bool
	Set bool
}

// UnmarshalJSON implements tolerant boolean decoding.
func (b *Bool) UnmarshalJSON(raw []byte) error {
	s := strings.TrimSpace(strings.Trim(strings.TrimSpace(string(raw)), `"`))
	if s == "" || s == "null" || strings.EqualFold(s, "n/a") {
		return nil
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return nil
	}
	b.Val, b.Set = v, true
	return nil
}
```

`strconv` and `strings` are already imported by `points.go:3-6`; do not add imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ecs/ -run 'TestBoolUnmarshal|TestNum' -v`

Expected: PASS — 9 `TestBoolUnmarshal` subtests, plus the pre-existing `Num` tests still green.

- [ ] **Step 5: Commit**

```bash
git add internal/ecs/points.go internal/ecs/points_test.go
git commit -m "feat(ecs): tolerant Bool primitive for quoted-string flags

The dashboard encodes gcUserDataIsEnabled and friends as \"true\"/\"false\",
which Num deliberately refuses. Unset rather than false when unparseable, so an
unreported flag stays absent."
```

---

### Task 2: Garbage-collection family

**Files:**
- Create: `internal/ecs/cluster_gc.go`
- Create: `internal/ecs/cluster_gc_test.go`
- Modify: `internal/ecs/cluster.go` (struct at lines 14-67, `Collect` at lines 77-151)
- Modify: `internal/ecs/testdata/localzone.json`, `cmd/mockecs/fixtures/localzone.json`
- Modify: `internal/ecs/cluster_test.go`

**Interfaces:**
- Consumes: `Series` and its `Latest() (float64, bool)`, `Bool` from Task 1, `Sample`, `Label` — all in package `ecs`.
- Produces: `type gcFields struct{…}` with `func (g gcFields) samples() []Sample`, embedded in `localZoneResp`. Tasks 3-5 mirror this exact shape with their own types.

- [ ] **Step 1: Write the failing test**

Create `internal/ecs/cluster_gc_test.go`:

```go
package ecs

import (
	"encoding/json"
	"testing"
)

func TestGCFieldsSamples(t *testing.T) {
	const payload = `{
		"gcUserPendingCurrent":        [{"t": "12345678", "Capacity": "700"}, {"t": "23456789", "Capacity": "900"}],
		"gcUserReclaimedCurrent":      [{"t": "23456789", "Capacity": "8100"}],
		"gcUserUnreclaimableCurrent":  [{"t": "23456789", "Capacity": "640"}],
		"gcUserTotalDetectedCurrent":  [{"t": "23456789", "Capacity": "9700"}],
		"gcUserDataIsEnabled":         "true",
		"gcSystemPendingCurrent":      [{"t": "23456789", "Capacity": "130"}],
		"gcSystemReclaimedCurrent":    [{"t": "23456789", "Capacity": "2500"}],
		"gcSystemUnreclaimableCurrent":[{"t": "23456789", "Capacity": "70"}],
		"gcSystemTotalDetectedCurrent":[{"t": "23456789", "Capacity": "2600"}]
	}`

	var g gcFields
	if err := json.Unmarshal([]byte(payload), &g); err != nil {
		t.Fatal(err)
	}
	got := g.samples()

	user := Label{"scope", "user"}
	system := Label{"scope", "system"}

	// "Current" is the newest point by t, not the first.
	mustSample(t, got, "ecs_cluster_gc_pending_bytes", 900, user)
	mustSample(t, got, "ecs_cluster_gc_reclaimed_bytes", 8100, user)
	mustSample(t, got, "ecs_cluster_gc_unreclaimable_bytes", 640, user)
	mustSample(t, got, "ecs_cluster_gc_detected_bytes", 9700, user)
	mustSample(t, got, "ecs_cluster_gc_enabled", 1, user)

	mustSample(t, got, "ecs_cluster_gc_pending_bytes", 130, system)
	mustSample(t, got, "ecs_cluster_gc_reclaimed_bytes", 2500, system)
	mustSample(t, got, "ecs_cluster_gc_unreclaimable_bytes", 70, system)
	mustSample(t, got, "ecs_cluster_gc_detected_bytes", 2600, system)

	// gcSystemMetadataIsEnabled was absent: the sample must be absent, not 0.
	if _, ok := findSample(got, "ecs_cluster_gc_enabled", system); ok {
		t.Error("gc_enabled{scope=system} must be absent when the flag is not reported")
	}
}

func TestGCFieldsSamplesDisabledFlagIsZeroNotAbsent(t *testing.T) {
	var g gcFields
	if err := json.Unmarshal([]byte(`{"gcUserDataIsEnabled": "false"}`), &g); err != nil {
		t.Fatal(err)
	}
	// A reported "false" is real information and must be emitted as 0 — only an
	// unreported flag is absent.
	mustSample(t, g.samples(), "ecs_cluster_gc_enabled", 0, Label{"scope", "user"})
}

func TestGCFieldsSamplesEmptyPayload(t *testing.T) {
	var g gcFields
	if err := json.Unmarshal([]byte(`{}`), &g); err != nil {
		t.Fatal(err)
	}
	if got := g.samples(); len(got) != 0 {
		t.Errorf("got %d samples from an empty payload, want 0", len(got))
	}
}

func TestGCFieldsSamplesUnparseableValue(t *testing.T) {
	const payload = `{
		"gcUserPendingCurrent":   [{"t": "23456789", "Capacity": "N/A"}],
		"gcUserReclaimedCurrent": [{"t": "23456789", "Capacity": "8100"}]
	}`
	var g gcFields
	if err := json.Unmarshal([]byte(payload), &g); err != nil {
		t.Fatal(err)
	}
	got := g.samples()
	user := Label{"scope", "user"}
	if _, ok := findSample(got, "ecs_cluster_gc_pending_bytes", user); ok {
		t.Error("an unparseable value must yield an absent sample, not zero")
	}
	// The rest of the family still emits.
	mustSample(t, got, "ecs_cluster_gc_reclaimed_bytes", 8100, user)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ecs/ -run TestGCFields -v`

Expected: FAIL — compile error `undefined: gcFields`.

- [ ] **Step 3: Write the implementation**

Create `internal/ecs/cluster_gc.go`:

```go
package ecs

// gcFields carries the garbage-collection block of the local-zone dashboard.
//
// Watch the API's asymmetric naming: the numeric series are keyed User/System
// while the two enable flags are keyed UserData/SystemMetadata. Both map onto the
// same "scope" label value — this is the API's inconsistency, not a typo to fix.
//
// gcCombined* is deliberately not decoded: it equals user + system exactly
// (verified to the byte against a live ObjectScale 4.3 cluster), so exporting it
// would make sum() double-count. `sum without(scope)` reproduces it.
type gcFields struct {
	GCUserPending       Series `json:"gcUserPendingCurrent"`
	GCUserReclaimed     Series `json:"gcUserReclaimedCurrent"`
	GCUserUnreclaimable Series `json:"gcUserUnreclaimableCurrent"`
	GCUserTotalDetected Series `json:"gcUserTotalDetectedCurrent"`
	GCUserDataIsEnabled Bool   `json:"gcUserDataIsEnabled"`

	GCSystemPending           Series `json:"gcSystemPendingCurrent"`
	GCSystemReclaimed         Series `json:"gcSystemReclaimedCurrent"`
	GCSystemUnreclaimable     Series `json:"gcSystemUnreclaimableCurrent"`
	GCSystemTotalDetected     Series `json:"gcSystemTotalDetectedCurrent"`
	GCSystemMetadataIsEnabled Bool   `json:"gcSystemMetadataIsEnabled"`
}

// samples maps the GC block to cluster-agnostic samples. Missing or unparseable
// values yield absent samples, never zeros.
func (g gcFields) samples() []Sample {
	var out []Sample

	series := func(name, scope string, s Series) {
		if v, ok := s.Latest(); ok {
			out = append(out, Sample{
				Name:   name,
				Labels: []Label{{Key: "scope", Value: scope}},
				Value:  v,
			})
		}
	}
	// A reported flag is emitted even when false — that is real information.
	// Only an unreported flag is absent.
	flag := func(scope string, b Bool) {
		if !b.Set {
			return
		}
		v := 0.0
		if b.Val {
			v = 1
		}
		out = append(out, Sample{
			Name:   "ecs_cluster_gc_enabled",
			Labels: []Label{{Key: "scope", Value: scope}},
			Value:  v,
		})
	}

	series("ecs_cluster_gc_pending_bytes", "user", g.GCUserPending)
	series("ecs_cluster_gc_reclaimed_bytes", "user", g.GCUserReclaimed)
	series("ecs_cluster_gc_unreclaimable_bytes", "user", g.GCUserUnreclaimable)
	series("ecs_cluster_gc_detected_bytes", "user", g.GCUserTotalDetected)
	flag("user", g.GCUserDataIsEnabled)

	series("ecs_cluster_gc_pending_bytes", "system", g.GCSystemPending)
	series("ecs_cluster_gc_reclaimed_bytes", "system", g.GCSystemReclaimed)
	series("ecs_cluster_gc_unreclaimable_bytes", "system", g.GCSystemUnreclaimable)
	series("ecs_cluster_gc_detected_bytes", "system", g.GCSystemTotalDetected)
	flag("system", g.GCSystemMetadataIsEnabled)

	return out
}
```

- [ ] **Step 4: Run the family test to verify it passes**

Run: `go test ./internal/ecs/ -run TestGCFields -v`

Expected: PASS — all four `TestGCFields*` tests.

- [ ] **Step 5: Wire the family into the collector**

In `internal/ecs/cluster.go`, add the embedded struct as the last field of
`localZoneResp` (after `ReplicationRpoTimestamp` at line 66, before the closing
brace at line 67):

```go
	gcFields
```

Then, in `Collect`, immediately before `return out, nil` (line 150):

```go
	out = append(out, z.gcFields.samples()...)
```

- [ ] **Step 6: Add the family to the shared fixture**

In **both** `internal/ecs/testdata/localzone.json` and
`cmd/mockecs/fixtures/localzone.json`, add these keys at the top level. Keep the
two files byte-identical.

```json
  "gcUserPendingCurrent": [{"t": "12345678", "Capacity": "700"}, {"t": "23456789", "Capacity": "900"}],
  "gcUserReclaimedCurrent": [{"t": "23456789", "Capacity": "8100"}],
  "gcUserUnreclaimableCurrent": [{"t": "23456789", "Capacity": "640"}],
  "gcUserTotalDetectedCurrent": [{"t": "23456789", "Capacity": "9700"}],
  "gcUserDataIsEnabled": "true",
  "gcSystemPendingCurrent": [{"t": "23456789", "Capacity": "130"}],
  "gcSystemReclaimedCurrent": [{"t": "23456789", "Capacity": "2500"}],
  "gcSystemUnreclaimableCurrent": [{"t": "23456789", "Capacity": "70"}],
  "gcSystemTotalDetectedCurrent": [{"t": "23456789", "Capacity": "2600"}],
```

`gcSystemMetadataIsEnabled` is **deliberately omitted** so the fixture proves that
an unreported flag yields an absent sample.

- [ ] **Step 7: Assert the family in the collector integration test**

Append inside `TestClusterCollect` in `internal/ecs/cluster_test.go`, after the
existing `ecs_cluster_replication_rpo_timestamp_seconds` assertion (line 57):

```go
	gcUser := Label{"scope", "user"}
	gcSystem := Label{"scope", "system"}
	mustSample(t, samples, "ecs_cluster_gc_pending_bytes", 900, gcUser)
	mustSample(t, samples, "ecs_cluster_gc_reclaimed_bytes", 8100, gcUser)
	mustSample(t, samples, "ecs_cluster_gc_unreclaimable_bytes", 640, gcUser)
	mustSample(t, samples, "ecs_cluster_gc_detected_bytes", 9700, gcUser)
	mustSample(t, samples, "ecs_cluster_gc_enabled", 1, gcUser)
	mustSample(t, samples, "ecs_cluster_gc_pending_bytes", 130, gcSystem)
	// The fixture omits gcSystemMetadataIsEnabled on purpose.
	if _, ok := findSample(samples, "ecs_cluster_gc_enabled", gcSystem); ok {
		t.Error("gc_enabled{scope=system} should be absent: the fixture omits the flag")
	}
```

- [ ] **Step 8: Run the package tests**

Run: `go test ./internal/ecs/... -race`

Expected: PASS, including `TestLabelKeyConsistency` — every `ecs_cluster_gc_*`
series carries exactly the `scope` label key.

- [ ] **Step 9: Commit**

```bash
git add internal/ecs/cluster_gc.go internal/ecs/cluster_gc_test.go internal/ecs/cluster.go \
        internal/ecs/cluster_test.go internal/ecs/testdata/localzone.json \
        cmd/mockecs/fixtures/localzone.json
git commit -m "feat(ecs): export garbage-collection metrics

pending/reclaimed/unreclaimable/detected bytes plus the enable flag, labelled by
scope=user|system. gcCombined* is not exported: it equals user+system exactly, so
sum without(scope) reproduces it without double-counting."
```

---

### Task 3: Recovery family

**Files:**
- Create: `internal/ecs/cluster_recovery.go`
- Create: `internal/ecs/cluster_recovery_test.go`
- Modify: `internal/ecs/cluster.go` (`localZoneResp` struct, `Collect`)
- Modify: `internal/ecs/testdata/localzone.json`, `cmd/mockecs/fixtures/localzone.json`
- Modify: `internal/ecs/cluster_test.go`

**Interfaces:**
- Consumes: `Series`, `Num`, `Sample` from package `ecs`; the embedding pattern established in Task 2.
- Produces: `type recoveryFields struct{…}` with `func (r recoveryFields) samples() []Sample`, embedded in `localZoneResp`.

- [ ] **Step 1: Write the failing test**

Create `internal/ecs/cluster_recovery_test.go`:

```go
package ecs

import (
	"encoding/json"
	"testing"
)

func TestRecoveryFieldsSamples(t *testing.T) {
	const payload = `{
		"recoveryBadChunksTotalSizeCurrent": [{"t": "12345678", "Space": "20000"}, {"t": "23456789", "Space": "10992"}],
		"recoveryRateCurrent":               [{"t": "23456789", "Rate": "17.5"}],
		"recoveryCompleteTimeEstimate":      "45.5"
	}`

	var r recoveryFields
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		t.Fatal(err)
	}
	got := r.samples()

	// "Current" is the newest point by t, not the largest or the first.
	mustSample(t, got, "ecs_cluster_recovery_bad_chunks_bytes", 10992)
	mustSample(t, got, "ecs_cluster_recovery_rate", 17.5)
	mustSample(t, got, "ecs_cluster_recovery_complete_time_estimate", 45.5)
}

func TestRecoveryFieldsSamplesEmptyPayload(t *testing.T) {
	var r recoveryFields
	if err := json.Unmarshal([]byte(`{}`), &r); err != nil {
		t.Fatal(err)
	}
	if got := r.samples(); len(got) != 0 {
		t.Errorf("got %d samples from an empty payload, want 0", len(got))
	}
}

func TestRecoveryFieldsSamplesUnparseableRate(t *testing.T) {
	const payload = `{
		"recoveryBadChunksTotalSizeCurrent": [{"t": "23456789", "Space": "10992"}],
		"recoveryRateCurrent":               [{"t": "23456789", "Rate": "N/A"}]
	}`
	var r recoveryFields
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		t.Fatal(err)
	}
	got := r.samples()
	if _, ok := findSample(got, "ecs_cluster_recovery_rate"); ok {
		t.Error("an unparseable rate must yield an absent sample, not zero")
	}
	// A zero-byte bad-chunk total is meaningful and must still be emitted.
	mustSample(t, got, "ecs_cluster_recovery_bad_chunks_bytes", 10992)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ecs/ -run TestRecoveryFields -v`

Expected: FAIL — compile error `undefined: recoveryFields`.

- [ ] **Step 3: Write the implementation**

Create `internal/ecs/cluster_recovery.go`:

```go
package ecs

// recoveryFields carries the chunk-recovery block of the local-zone dashboard.
// recoveryBadChunksTotalSize is a durability signal: bytes of corrupted chunks
// still awaiting recovery.
//
// recoveryRate and recoveryCompleteTimeEstimate carry no unit in the API
// reference, so their metric names carry no unit suffix (ADR-0006).
type recoveryFields struct {
	RecoveryBadChunksTotalSize   Series `json:"recoveryBadChunksTotalSizeCurrent"`
	RecoveryRate                 Series `json:"recoveryRateCurrent"`
	RecoveryCompleteTimeEstimate Num    `json:"recoveryCompleteTimeEstimate"`
}

// samples maps the recovery block to cluster-agnostic samples. Missing or
// unparseable values yield absent samples, never zeros.
func (r recoveryFields) samples() []Sample {
	var out []Sample

	series := func(name string, s Series) {
		if v, ok := s.Latest(); ok {
			out = append(out, Sample{Name: name, Value: v})
		}
	}

	series("ecs_cluster_recovery_bad_chunks_bytes", r.RecoveryBadChunksTotalSize)
	series("ecs_cluster_recovery_rate", r.RecoveryRate)
	if r.RecoveryCompleteTimeEstimate.Set {
		out = append(out, Sample{
			Name:  "ecs_cluster_recovery_complete_time_estimate",
			Value: r.RecoveryCompleteTimeEstimate.Val,
		})
	}

	return out
}
```

- [ ] **Step 4: Run the family test to verify it passes**

Run: `go test ./internal/ecs/ -run TestRecoveryFields -v`

Expected: PASS — all three `TestRecoveryFields*` tests.

- [ ] **Step 5: Wire the family into the collector**

In `internal/ecs/cluster.go`, add to `localZoneResp` immediately after the
`gcFields` line added in Task 2:

```go
	recoveryFields
```

Then in `Collect`, immediately after the `z.gcFields.samples()` append:

```go
	out = append(out, z.recoveryFields.samples()...)
```

- [ ] **Step 6: Add the family to the shared fixture**

In **both** `internal/ecs/testdata/localzone.json` and
`cmd/mockecs/fixtures/localzone.json`, add:

```json
  "recoveryBadChunksTotalSizeCurrent": [{"t": "23456789", "Space": "10992"}],
  "recoveryRateCurrent": [{"t": "23456789", "Rate": "N/A"}],
  "recoveryCompleteTimeEstimate": "45.5",
```

`recoveryRateCurrent` deliberately carries `"N/A"` so the fixture proves an
unparseable value yields an absent sample while its siblings still emit.

- [ ] **Step 7: Assert the family in the collector integration test**

Append inside `TestClusterCollect`, after the GC assertions from Task 2:

```go
	mustSample(t, samples, "ecs_cluster_recovery_bad_chunks_bytes", 10992)
	mustSample(t, samples, "ecs_cluster_recovery_complete_time_estimate", 45.5)
	// The fixture sets recoveryRateCurrent to "N/A" on purpose.
	if _, ok := findSample(samples, "ecs_cluster_recovery_rate"); ok {
		t.Error("recovery_rate should be absent: the fixture value is unparseable")
	}
```

- [ ] **Step 8: Run the package tests**

Run: `go test ./internal/ecs/... -race`

Expected: PASS, `TestLabelKeyConsistency` included.

- [ ] **Step 9: Commit**

```bash
git add internal/ecs/cluster_recovery.go internal/ecs/cluster_recovery_test.go \
        internal/ecs/cluster.go internal/ecs/cluster_test.go \
        internal/ecs/testdata/localzone.json cmd/mockecs/fixtures/localzone.json
git commit -m "feat(ecs): export chunk-recovery metrics

Bad-chunk bytes awaiting recovery is a durability signal that was previously
invisible. Rate and time estimate carry no documented unit, so no unit suffix."
```

---

### Task 4: Erasure-coding family

**Files:**
- Create: `internal/ecs/cluster_ec.go`
- Create: `internal/ecs/cluster_ec_test.go`
- Modify: `internal/ecs/cluster.go` (`localZoneResp` struct, `Collect`)
- Modify: `internal/ecs/testdata/localzone.json`, `cmd/mockecs/fixtures/localzone.json`
- Modify: `internal/ecs/cluster_test.go`

**Interfaces:**
- Consumes: `Series`, `Num`, `Sample`; the embedding pattern from Task 2.
- Produces: `type erasureCodingFields struct{…}` with `func (e erasureCodingFields) samples() []Sample`, embedded in `localZoneResp`.

- [ ] **Step 1: Write the failing test**

Create `internal/ecs/cluster_ec_test.go`:

```go
package ecs

import (
	"encoding/json"
	"testing"
)

func TestErasureCodingFieldsSamples(t *testing.T) {
	const payload = `{
		"chunksEcApplicableTotalSealSizeCurrent": [{"t": "23456789", "Space": "59000"}],
		"chunksEcCodedTotalSealSizeCurrent":      [{"t": "23456789", "Space": "58000"}],
		"chunksEcCodedRatioCurrent":              [{"t": "12345678", "Percent": "97.5"}, {"t": "23456789", "Percent": "98.3"}],
		"chunksEcRateCurrent":                    [{"t": "23456789", "Rate": "12.5"}],
		"chunksEcCompleteTimeEstimate":           "3.25"
	}`

	var e erasureCodingFields
	if err := json.Unmarshal([]byte(payload), &e); err != nil {
		t.Fatal(err)
	}
	got := e.samples()

	mustSample(t, got, "ecs_cluster_ec_applicable_bytes", 59000)
	mustSample(t, got, "ecs_cluster_ec_coded_bytes", 58000)
	// Newest point by t wins.
	mustSample(t, got, "ecs_cluster_ec_coded_ratio_percent", 98.3)
	mustSample(t, got, "ecs_cluster_ec_rate", 12.5)
	mustSample(t, got, "ecs_cluster_ec_complete_time_estimate", 3.25)
}

func TestErasureCodingFieldsSamplesEmptyPayload(t *testing.T) {
	var e erasureCodingFields
	if err := json.Unmarshal([]byte(`{}`), &e); err != nil {
		t.Fatal(err)
	}
	if got := e.samples(); len(got) != 0 {
		t.Errorf("got %d samples from an empty payload, want 0", len(got))
	}
}

func TestErasureCodingFieldsSamplesPartialPayload(t *testing.T) {
	// A cluster reporting only the ratio must still yield that one sample.
	const payload = `{"chunksEcCodedRatioCurrent": [{"t": "23456789", "Percent": "98.3"}]}`
	var e erasureCodingFields
	if err := json.Unmarshal([]byte(payload), &e); err != nil {
		t.Fatal(err)
	}
	got := e.samples()
	if len(got) != 1 {
		t.Fatalf("got %d samples, want exactly 1", len(got))
	}
	mustSample(t, got, "ecs_cluster_ec_coded_ratio_percent", 98.3)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ecs/ -run TestErasureCodingFields -v`

Expected: FAIL — compile error `undefined: erasureCodingFields`.

- [ ] **Step 3: Write the implementation**

Create `internal/ecs/cluster_ec.go`:

```go
package ecs

// erasureCodingFields carries the erasure-coding block of the local-zone
// dashboard: how much data is eligible for EC, how much is already coded, and how
// fast the gap is closing.
//
// chunksEcRate and chunksEcCompleteTimeEstimate carry no unit in the API
// reference, so their metric names carry no unit suffix (ADR-0006).
type erasureCodingFields struct {
	ChunksEcApplicableTotalSealSize Series `json:"chunksEcApplicableTotalSealSizeCurrent"`
	ChunksEcCodedTotalSealSize      Series `json:"chunksEcCodedTotalSealSizeCurrent"`
	ChunksEcCodedRatio              Series `json:"chunksEcCodedRatioCurrent"`
	ChunksEcRate                    Series `json:"chunksEcRateCurrent"`
	ChunksEcCompleteTimeEstimate    Num    `json:"chunksEcCompleteTimeEstimate"`
}

// samples maps the erasure-coding block to cluster-agnostic samples. Missing or
// unparseable values yield absent samples, never zeros.
func (e erasureCodingFields) samples() []Sample {
	var out []Sample

	series := func(name string, s Series) {
		if v, ok := s.Latest(); ok {
			out = append(out, Sample{Name: name, Value: v})
		}
	}

	series("ecs_cluster_ec_applicable_bytes", e.ChunksEcApplicableTotalSealSize)
	series("ecs_cluster_ec_coded_bytes", e.ChunksEcCodedTotalSealSize)
	series("ecs_cluster_ec_coded_ratio_percent", e.ChunksEcCodedRatio)
	series("ecs_cluster_ec_rate", e.ChunksEcRate)
	if e.ChunksEcCompleteTimeEstimate.Set {
		out = append(out, Sample{
			Name:  "ecs_cluster_ec_complete_time_estimate",
			Value: e.ChunksEcCompleteTimeEstimate.Val,
		})
	}

	return out
}
```

- [ ] **Step 4: Run the family test to verify it passes**

Run: `go test ./internal/ecs/ -run TestErasureCodingFields -v`

Expected: PASS — all three `TestErasureCodingFields*` tests.

- [ ] **Step 5: Wire the family into the collector**

In `internal/ecs/cluster.go`, add to `localZoneResp` immediately after the
`recoveryFields` line added in Task 3:

```go
	erasureCodingFields
```

Then in `Collect`, immediately after the `z.recoveryFields.samples()` append:

```go
	out = append(out, z.erasureCodingFields.samples()...)
```

- [ ] **Step 6: Add the family to the shared fixture**

In **both** `internal/ecs/testdata/localzone.json` and
`cmd/mockecs/fixtures/localzone.json`, add:

```json
  "chunksEcApplicableTotalSealSizeCurrent": [{"t": "23456789", "Space": "59000"}],
  "chunksEcCodedTotalSealSizeCurrent": [{"t": "23456789", "Space": "58000"}],
  "chunksEcCodedRatioCurrent": [{"t": "12345678", "Percent": "97.5"}, {"t": "23456789", "Percent": "98.3"}],
  "chunksEcRateCurrent": [{"t": "23456789", "Rate": "12.5"}],
  "chunksEcCompleteTimeEstimate": "3.25",
```

- [ ] **Step 7: Assert the family in the collector integration test**

Append inside `TestClusterCollect`, after the recovery assertions from Task 3:

```go
	mustSample(t, samples, "ecs_cluster_ec_applicable_bytes", 59000)
	mustSample(t, samples, "ecs_cluster_ec_coded_bytes", 58000)
	mustSample(t, samples, "ecs_cluster_ec_coded_ratio_percent", 98.3)
	mustSample(t, samples, "ecs_cluster_ec_rate", 12.5)
	mustSample(t, samples, "ecs_cluster_ec_complete_time_estimate", 3.25)
```

- [ ] **Step 8: Run the package tests**

Run: `go test ./internal/ecs/... -race`

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/ecs/cluster_ec.go internal/ecs/cluster_ec_test.go \
        internal/ecs/cluster.go internal/ecs/cluster_test.go \
        internal/ecs/testdata/localzone.json cmd/mockecs/fixtures/localzone.json
git commit -m "feat(ecs): export erasure-coding metrics

Applicable and coded seal sizes, coded ratio, rate and completion estimate."
```

---

### Task 5: Allocation-breakdown family

**Files:**
- Create: `internal/ecs/cluster_allocation.go`
- Create: `internal/ecs/cluster_allocation_test.go`
- Modify: `internal/ecs/cluster.go` (`localZoneResp` struct, `Collect`)
- Modify: `internal/ecs/testdata/localzone.json`, `cmd/mockecs/fixtures/localzone.json`
- Modify: `internal/ecs/cluster_test.go`

**Interfaces:**
- Consumes: `Series`, `Sample`, `Label`; the embedding pattern from Task 2.
- Produces: `type allocationComponentFields struct{…}` with `func (a allocationComponentFields) samples() []Sample`, embedded in `localZoneResp`.

- [ ] **Step 1: Write the failing test**

Create `internal/ecs/cluster_allocation_test.go`:

```go
package ecs

import (
	"encoding/json"
	"testing"
)

func TestAllocationComponentFieldsSamples(t *testing.T) {
	const payload = `{
		"diskSpaceAllocatedUserDataCurrent":        [{"t": "23456789", "Capacity": "3100"}],
		"diskSpaceAllocatedSystemMetadataCurrent":  [{"t": "23456789", "Capacity": "1200"}],
		"diskSpaceAllocatedLocalProtectionCurrent": [{"t": "23456789", "Capacity": "600"}]
	}`

	var a allocationComponentFields
	if err := json.Unmarshal([]byte(payload), &a); err != nil {
		t.Fatal(err)
	}
	got := a.samples()

	mustSample(t, got, "ecs_cluster_disk_space_allocated_component_bytes", 3100, Label{"purpose", "user_data"})
	mustSample(t, got, "ecs_cluster_disk_space_allocated_component_bytes", 1200, Label{"purpose", "system_metadata"})
	mustSample(t, got, "ecs_cluster_disk_space_allocated_component_bytes", 600, Label{"purpose", "local_protection"})

	// geo_cache and geo_copy were not reported: they must be absent, not zero.
	// Padding them with zeros would imply the breakdown is exhaustive, which it
	// is not — on a live 4.3 cluster the components sum to 12.8% less than
	// diskSpaceAllocatedCurrent.
	for _, purpose := range []string{"geo_cache", "geo_copy"} {
		if _, ok := findSample(got, "ecs_cluster_disk_space_allocated_component_bytes", Label{"purpose", purpose}); ok {
			t.Errorf("purpose=%s must be absent when the cluster does not report it", purpose)
		}
	}
	if len(got) != 3 {
		t.Errorf("got %d samples, want exactly 3", len(got))
	}
}

func TestAllocationComponentFieldsSamplesAllPurposes(t *testing.T) {
	const payload = `{
		"diskSpaceAllocatedUserDataCurrent":        [{"t": "1", "Capacity": "1"}],
		"diskSpaceAllocatedSystemMetadataCurrent":  [{"t": "1", "Capacity": "2"}],
		"diskSpaceAllocatedGeoCacheCurrent":        [{"t": "1", "Capacity": "3"}],
		"diskSpaceAllocatedGeoCopyCurrent":         [{"t": "1", "Capacity": "4"}],
		"diskSpaceAllocatedLocalProtectionCurrent": [{"t": "1", "Capacity": "5"}]
	}`
	var a allocationComponentFields
	if err := json.Unmarshal([]byte(payload), &a); err != nil {
		t.Fatal(err)
	}
	got := a.samples()
	if len(got) != 5 {
		t.Fatalf("got %d samples, want 5 (one per purpose)", len(got))
	}
	for _, tc := range []struct {
		purpose string
		want    float64
	}{
		{"user_data", 1}, {"system_metadata", 2}, {"geo_cache", 3},
		{"geo_copy", 4}, {"local_protection", 5},
	} {
		mustSample(t, got, "ecs_cluster_disk_space_allocated_component_bytes", tc.want, Label{"purpose", tc.purpose})
	}
}

func TestAllocationComponentFieldsSamplesEmptyPayload(t *testing.T) {
	var a allocationComponentFields
	if err := json.Unmarshal([]byte(`{}`), &a); err != nil {
		t.Fatal(err)
	}
	if got := a.samples(); len(got) != 0 {
		t.Errorf("got %d samples from an empty payload, want 0", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ecs/ -run TestAllocationComponentFields -v`

Expected: FAIL — compile error `undefined: allocationComponentFields`.

- [ ] **Step 3: Write the implementation**

Create `internal/ecs/cluster_allocation.go`:

```go
package ecs

// allocationComponentFields carries the breakdown of allocated space by purpose.
//
// This is deliberately a separate metric name rather than a label on the existing
// ecs_cluster_disk_space_allocated_bytes: adding a label to a published metric
// would break the ADR-0006 invariant and every existing query.
//
// The breakdown is NOT exhaustive. On a live ObjectScale 4.3 cluster these five
// components summed to 12.8% less than diskSpaceAllocatedCurrent. Never pad a
// missing component with zero — that would imply the parts account for the whole.
type allocationComponentFields struct {
	UserData        Series `json:"diskSpaceAllocatedUserDataCurrent"`
	SystemMetadata  Series `json:"diskSpaceAllocatedSystemMetadataCurrent"`
	GeoCache        Series `json:"diskSpaceAllocatedGeoCacheCurrent"`
	GeoCopy         Series `json:"diskSpaceAllocatedGeoCopyCurrent"`
	LocalProtection Series `json:"diskSpaceAllocatedLocalProtectionCurrent"`
}

// samples maps the allocation breakdown to cluster-agnostic samples. Missing or
// unparseable components yield absent samples, never zeros.
func (a allocationComponentFields) samples() []Sample {
	var out []Sample

	component := func(purpose string, s Series) {
		if v, ok := s.Latest(); ok {
			out = append(out, Sample{
				Name:   "ecs_cluster_disk_space_allocated_component_bytes",
				Labels: []Label{{Key: "purpose", Value: purpose}},
				Value:  v,
			})
		}
	}

	component("user_data", a.UserData)
	component("system_metadata", a.SystemMetadata)
	component("geo_cache", a.GeoCache)
	component("geo_copy", a.GeoCopy)
	component("local_protection", a.LocalProtection)

	return out
}
```

- [ ] **Step 4: Run the family test to verify it passes**

Run: `go test ./internal/ecs/ -run TestAllocationComponentFields -v`

Expected: PASS — all three `TestAllocationComponentFields*` tests.

- [ ] **Step 5: Wire the family into the collector**

In `internal/ecs/cluster.go`, add to `localZoneResp` immediately after the
`erasureCodingFields` line added in Task 4:

```go
	allocationComponentFields
```

Then in `Collect`, immediately after the `z.erasureCodingFields.samples()` append:

```go
	out = append(out, z.allocationComponentFields.samples()...)
```

- [ ] **Step 6: Add the family to the shared fixture**

In **both** `internal/ecs/testdata/localzone.json` and
`cmd/mockecs/fixtures/localzone.json`, add:

```json
  "diskSpaceAllocatedUserDataCurrent": [{"t": "23456789", "Capacity": "3100"}],
  "diskSpaceAllocatedSystemMetadataCurrent": [{"t": "23456789", "Capacity": "1200"}],
  "diskSpaceAllocatedLocalProtectionCurrent": [{"t": "23456789", "Capacity": "600"}],
```

`diskSpaceAllocatedGeoCacheCurrent` and `diskSpaceAllocatedGeoCopyCurrent` are
**deliberately omitted**. The three present components sum to 4900 against the
fixture's `ecs_cluster_disk_space_allocated_bytes` of 5000, mirroring the
non-exhaustiveness measured on the real cluster.

- [ ] **Step 7: Assert the family in the collector integration test**

Append inside `TestClusterCollect`, after the erasure-coding assertions from Task 4:

```go
	mustSample(t, samples, "ecs_cluster_disk_space_allocated_component_bytes", 3100, Label{"purpose", "user_data"})
	mustSample(t, samples, "ecs_cluster_disk_space_allocated_component_bytes", 1200, Label{"purpose", "system_metadata"})
	mustSample(t, samples, "ecs_cluster_disk_space_allocated_component_bytes", 600, Label{"purpose", "local_protection"})
	// The fixture omits the geo components on purpose: the breakdown is not
	// exhaustive, and 3100+1200+600 = 4900 against an allocated total of 5000.
	if _, ok := findSample(samples, "ecs_cluster_disk_space_allocated_component_bytes", Label{"purpose", "geo_cache"}); ok {
		t.Error("purpose=geo_cache should be absent: the fixture omits it")
	}
```

- [ ] **Step 8: Run the package tests**

Run: `go test ./internal/ecs/... -race`

Expected: PASS, `TestLabelKeyConsistency` included — the new metric name carries
exactly the `purpose` label key on every series.

- [ ] **Step 9: Commit**

```bash
git add internal/ecs/cluster_allocation.go internal/ecs/cluster_allocation_test.go \
        internal/ecs/cluster.go internal/ecs/cluster_test.go \
        internal/ecs/testdata/localzone.json cmd/mockecs/fixtures/localzone.json
git commit -m "feat(ecs): export allocated-space breakdown by purpose

A distinct metric name rather than a label on the published allocated total, and
absent rather than zero for unreported components: the breakdown does not sum to
the total (12.8% gap measured on a live 4.3 cluster)."
```

---

### Task 6: Real-payload shape test

**Files:**
- Create: `internal/ecs/testdata/localzone-live-4.3.json`
- Create: `internal/ecs/testdata/README.md`
- Create: `internal/ecs/cluster_livepayload_test.go`

**Interfaces:**
- Consumes: `localZoneResp` with all four families embedded (Tasks 2-5), `fixture(t, name)` from `internal/ecs/fixtures_test.go:12`.
- Produces: nothing consumed by later tasks.

This fixture is **not** served by `mockClient` and is **not** copied to
`cmd/mockecs/fixtures/` — it is read directly by one test. Nothing to sync.

- [ ] **Step 1: Extract and sanitize the live payload**

The source is a sanitized `Trace` log supplied by the PR #18 contributor at
`/Users/fjacquet/Downloads/trace.sanitized.log`. Run this from the repo root:

```bash
python3 - <<'EOF'
import re, json
src = '/Users/fjacquet/Downloads/trace.sanitized.log'
lines = open(src).read().splitlines()

payload = None
for line in lines:
    m = re.search(r'msg="API trace:\\n(.*?)" cluster=', line, re.S)
    if not m:
        continue
    d = json.loads(m.group(1).encode().decode('unicode_escape'))
    if d.get('_links', {}).get('self', {}).get('href') == '/dashboard/zones/localzone':
        payload = d
        break
assert payload is not None, 'local-zone payload not found in the trace'

# Sanitization check: prove there is nothing else to strip before writing.
blob = json.dumps(payload)
uuids = re.findall(r'[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}', blob)
builds = re.findall(r'\d+\.\d+\.\d+\.\d+\.\d+\.[0-9a-f]+', blob)
assert not uuids, f'unexpected UUIDs, sanitize them: {set(uuids)}'
assert not builds, f'unexpected build hashes, sanitize them: {set(builds)}'

payload['name'] = 'vdc-example'

out = 'internal/ecs/testdata/localzone-live-4.3.json'
with open(out, 'w') as f:
    json.dump(payload, f, indent=2, sort_keys=True)
    f.write('\n')
print('wrote', out, 'with', len(payload), 'top-level keys')
EOF
```

Expected: `wrote internal/ecs/testdata/localzone-live-4.3.json with 97 top-level keys`,
and both assertions pass. **If either assertion fires, stop and report it** — it
means the payload carries an identifier the plan did not anticipate, and it must
be sanitized before the file is committed.

- [ ] **Step 2: Record the fixture's provenance**

Create `internal/ecs/testdata/README.md`:

```markdown
# Test fixtures

`localzone.json`, `nodes.json`, `replicationgroups.json`, `vdc-nodes.json`,
`namespaces.json`, `billing.json`, `quota-*.json` are derived from the Dell
ObjectScale 4.1 REST reference examples, with targeted corrections where a live
4.3 cluster contradicted the reference (see ADR-0007). Values are chosen to be
distinct and non-zero, and some fields are deliberately omitted or made
unparseable so the suite can tell an absent sample from a zero one. Their copies
under `cmd/mockecs/fixtures/` must stay byte-identical.

`localzone-live-4.3.json` is different: it is an unedited capture of
`GET /dashboard/zones/localzone` from a real ObjectScale 4.3.0.0 cluster,
contributed as a sanitized trace on PR #18, with only `name` normalized. It is
read by exactly one test (`cluster_livepayload_test.go`), which asserts **shape,
never values** — the cluster was idle, so most values are zero and would make
weak assertions. Its job is to catch a misspelled JSON tag against an authentic
payload, which hand-written fixtures structurally cannot do. Do not add it to
`mockClient`, and do not copy it to `cmd/mockecs/fixtures/`.
```

- [ ] **Step 3: Write the failing test**

Create `internal/ecs/cluster_livepayload_test.go`:

```go
package ecs

import (
	"encoding/json"
	"testing"
)

// TestLocalZoneLivePayloadShape decodes an unedited capture from a real
// ObjectScale 4.3 cluster and asserts that every family produces samples.
//
// It asserts no values on purpose: the source cluster was idle, so most values
// are zero and any value assertion would be weak. What it proves is that the
// struct tags match a payload the vendor's own cluster actually emits — a
// misspelled tag passes hand-written fixtures, because those carry the same
// misspelling as the code, but it cannot pass this.
func TestLocalZoneLivePayloadShape(t *testing.T) {
	var z localZoneResp
	if err := json.Unmarshal([]byte(fixture(t, "localzone-live-4.3.json")), &z); err != nil {
		t.Fatalf("decoding the live payload failed: %v", err)
	}

	families := []struct {
		name    string
		samples []Sample
	}{
		{"gc", z.gcFields.samples()},
		{"recovery", z.recoveryFields.samples()},
		{"erasure coding", z.erasureCodingFields.samples()},
		{"allocation components", z.allocationComponentFields.samples()},
	}
	for _, f := range families {
		if len(f.samples) == 0 {
			t.Errorf("%s family produced no samples from the live payload: a JSON tag is probably misspelled", f.name)
		}
	}

	// Every emitted series must carry the label keys its metric name declares,
	// or the Prometheus collector drops it at scrape time (ADR-0006).
	wantKeys := map[string][]string{
		"ecs_cluster_gc_pending_bytes":                      {"scope"},
		"ecs_cluster_gc_reclaimed_bytes":                    {"scope"},
		"ecs_cluster_gc_unreclaimable_bytes":                {"scope"},
		"ecs_cluster_gc_detected_bytes":                     {"scope"},
		"ecs_cluster_gc_enabled":                            {"scope"},
		"ecs_cluster_disk_space_allocated_component_bytes":  {"purpose"},
	}
	for _, f := range families {
		for _, s := range f.samples {
			want, ok := wantKeys[s.Name]
			if !ok {
				if len(s.Labels) != 0 {
					t.Errorf("%s: expected no labels, got %v", s.Name, s.Labels)
				}
				continue
			}
			if len(s.Labels) != len(want) {
				t.Errorf("%s: got %d labels, want %d", s.Name, len(s.Labels), len(want))
				continue
			}
			for i, key := range want {
				if s.Labels[i].Key != key {
					t.Errorf("%s: label %d key = %q, want %q", s.Name, i, s.Labels[i].Key, key)
				}
			}
		}
	}
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/ecs/ -run TestLocalZoneLivePayloadShape -v`

Expected: PASS. If a family reports "no samples", the corresponding struct tag in
that family's file does not match the real payload — fix the tag, not the test.

- [ ] **Step 5: Commit**

```bash
git add internal/ecs/testdata/localzone-live-4.3.json internal/ecs/testdata/README.md \
        internal/ecs/cluster_livepayload_test.go
git commit -m "test(ecs): shape test against a real 4.3 local-zone payload

Hand-written fixtures carry the same typos as the code they test. This one is an
unedited capture, so a misspelled JSON tag cannot survive it."
```

---

### Task 7: Export-path coverage

**Files:**
- Modify: `internal/ecs/prometheus_test.go:31-49`
- Modify: `internal/ecs/otlp_test.go`

**Interfaces:**
- Consumes: the metric names emitted by Tasks 2-5, reaching the export paths via `mockClient` and the fixture edits from those tasks.
- Produces: nothing consumed by later tasks.

Both export paths must be asserted for every new metric family (CLAUDE.md rule),
including at least one labelled series — that is where label collisions surface.

- [ ] **Step 1: Extend the Prometheus gather assertions**

In `internal/ecs/prometheus_test.go`, add these entries to the string slice in
`TestPromCollectorGather` (currently lines 31-45), after `"ecs_cluster_info"`:

```go
		"ecs_cluster_gc_pending_bytes",
		"ecs_cluster_gc_enabled",
		"ecs_cluster_recovery_bad_chunks_bytes",
		"ecs_cluster_ec_coded_ratio_percent",
		"ecs_cluster_disk_space_allocated_component_bytes",
```

Then add these series-count assertions after the existing `ecs_node_healthy`
check (currently lines 53-55):

```go
	// scope=user and scope=system both report pending bytes.
	if got := families["ecs_cluster_gc_pending_bytes"]; got != 2 {
		t.Errorf("gc pending series = %d, want 2 (one per scope)", got)
	}
	// The fixture omits gcSystemMetadataIsEnabled, so only scope=user is enabled.
	if got := families["ecs_cluster_gc_enabled"]; got != 1 {
		t.Errorf("gc enabled series = %d, want 1 (the fixture omits the system flag)", got)
	}
	// The fixture omits the two geo components.
	if got := families["ecs_cluster_disk_space_allocated_component_bytes"]; got != 3 {
		t.Errorf("allocation component series = %d, want 3 (the fixture omits the geo purposes)", got)
	}
```

- [ ] **Step 2: Run the Prometheus test**

Run: `go test ./internal/ecs/ -run TestPromCollector -v`

Expected: PASS — all three counts match the fixture built in Tasks 2-5.

- [ ] **Step 3: Extend the OTLP assertions**

`internal/ecs/otlp_test.go` builds a `got` map of metric name to value in
`TestOTLPExporterObservesSnapshot` and asserts individual values (see the existing
`ecs_cluster_disk_space_reserved_bytes` check around line 54). Add these
assertions alongside them, matching that file's existing style:

```go
	if got["ecs_cluster_recovery_bad_chunks_bytes"] != 10992 {
		t.Errorf("ecs_cluster_recovery_bad_chunks_bytes = %v, want 10992", got["ecs_cluster_recovery_bad_chunks_bytes"])
	}
	if got["ecs_cluster_ec_coded_ratio_percent"] != 98.3 {
		t.Errorf("ecs_cluster_ec_coded_ratio_percent = %v, want 98.3", got["ecs_cluster_ec_coded_ratio_percent"])
	}
```

Read the file first: if `got` is keyed by metric name only, a labelled metric such
as `ecs_cluster_gc_pending_bytes` would collide across its two `scope` values. In
that case assert only the two unlabelled metrics above, and add a comment saying
the labelled families are covered by the Prometheus gather test, which counts
series per family. Do not silently drop the coverage without that note.

- [ ] **Step 4: Run the OTLP test**

Run: `go test ./internal/ecs/ -run TestOTLP -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ecs/prometheus_test.go internal/ecs/otlp_test.go
git commit -m "test(ecs): cover the four new families on both export paths"
```

---

### Task 8: Documentation and Grafana dashboard

**Files:**
- Modify: `docs/metrics.md`
- Modify: `CHANGELOG.md`
- Create: `grafana/dashboards/obs-storage-internals.json`

**Interfaces:**
- Consumes: the metric names from Tasks 2-5.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Document the four families**

In `docs/metrics.md`, add a new section after the `## Cluster (VDC-wide)` table
and before `## Replication groups`:

```markdown
## Cluster background processes

From the same local-zone dashboard response as the cluster metrics above — no
additional API call.

| Metric | Labels | Description |
|---|---|---|
| `ecs_cluster_gc_pending_bytes` | `scope` (`user`/`system`) | space detected as reclaimable but not yet reclaimed |
| `ecs_cluster_gc_reclaimed_bytes` | `scope` | space reclaimed so far |
| `ecs_cluster_gc_unreclaimable_bytes` | `scope` | space detected but not reclaimable |
| `ecs_cluster_gc_detected_bytes` | `scope` | total space detected by GC |
| `ecs_cluster_gc_enabled` | `scope` | `1` when that GC scope is enabled, `0` when explicitly disabled |
| `ecs_cluster_recovery_bad_chunks_bytes` | | corrupted chunk data still awaiting recovery |
| `ecs_cluster_recovery_rate` | | recovery throughput (unit as reported by the dashboard API) |
| `ecs_cluster_recovery_complete_time_estimate` | | estimated time to finish recovery (unit as reported by the dashboard API) |
| `ecs_cluster_ec_applicable_bytes` | | sealed data eligible for erasure coding |
| `ecs_cluster_ec_coded_bytes` | | sealed data already erasure-coded |
| `ecs_cluster_ec_coded_ratio_percent` | | coded share of applicable data |
| `ecs_cluster_ec_rate` | | erasure-coding throughput (unit as reported by the dashboard API) |
| `ecs_cluster_ec_complete_time_estimate` | | estimated time to finish coding (unit as reported by the dashboard API) |
| `ecs_cluster_disk_space_allocated_component_bytes` | `purpose` | allocated space broken down by what holds it |

`purpose` is one of `user_data`, `system_metadata`, `geo_cache`, `geo_copy`,
`local_protection`.

!!! warning "The allocation breakdown is not exhaustive"
    `ecs_cluster_disk_space_allocated_component_bytes` does **not** sum to
    `ecs_cluster_disk_space_allocated_bytes`. On a real ObjectScale 4.3 cluster the
    five components accounted for 87.2% of the allocated total. Do not compute
    percentages of the total from these components, and do not treat the remainder
    as a category — it is simply unreported.

!!! note "There is no `scope=\"combined\"`"
    The API also reports combined GC figures, which equal `user + system` exactly
    (verified to the byte on a live cluster). Exporting them would make
    `sum(ecs_cluster_gc_pending_bytes)` double-count, so they are omitted:
    `sum without(scope) (ecs_cluster_gc_pending_bytes)` reproduces them.
```

- [ ] **Step 2: Add the changelog entry**

In `CHANGELOG.md`, insert a new section directly above `## [2.7.1] - 2026-07-29`:

```markdown
## [Unreleased]

### Added
- Cluster background-process metrics, all from the local-zone dashboard response
  the exporter already fetches once per cycle (no additional API call):
  - garbage collection — `ecs_cluster_gc_pending_bytes`, `_reclaimed_bytes`,
    `_unreclaimable_bytes`, `_detected_bytes` and `ecs_cluster_gc_enabled`,
    labelled `scope="user"|"system"`. Combined figures are not exported: they
    equal `user + system` exactly, so `sum without(scope)` reproduces them
    without double-counting.
  - chunk recovery — `ecs_cluster_recovery_bad_chunks_bytes` (corrupted data
    awaiting recovery), `ecs_cluster_recovery_rate`,
    `ecs_cluster_recovery_complete_time_estimate`.
  - erasure coding — `ecs_cluster_ec_applicable_bytes`, `_coded_bytes`,
    `_coded_ratio_percent`, `ecs_cluster_ec_rate`,
    `ecs_cluster_ec_complete_time_estimate`.
  - allocated-space breakdown —
    `ecs_cluster_disk_space_allocated_component_bytes{purpose}`. Note this does
    **not** sum to `ecs_cluster_disk_space_allocated_bytes`; the breakdown is not
    exhaustive.
- Grafana dashboard "ObjectScale — Storage internals" covering the four families.

```

- [ ] **Step 3: Create the dashboard**

Create `grafana/dashboards/obs-storage-internals.json`. Copy the top-level
structure of `grafana/dashboards/obs-replication.json` verbatim — `schemaVersion`,
`editable`, `style`, `timezone`, `refresh`, `time`, `links`, and the `cluster`
templating variable — changing only:

- `"title": "ObjectScale — Storage internals"`
- `"uid": "obs-storage-internals"`
- `"tags"`: keep `objectscale`, `ecs`, `dell`, `object-storage`, `obs-nav`, and
  replace `replication` with `storage-internals`

Then define six panels. Every panel uses
`"datasource": {"type": "prometheus", "uid": "prometheus"}` at both panel and
target level, and every `expr` filters on `{cluster=~"$cluster"}`, exactly as the
template does. Here is the first panel complete — follow its structure for the
rest:

```json
{
  "id": 1,
  "type": "timeseries",
  "title": "Corrupted data awaiting recovery",
  "description": "Bytes of bad chunks not yet recovered. Should trend to zero; a rising floor is a durability concern.",
  "datasource": {"type": "prometheus", "uid": "prometheus"},
  "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0},
  "fieldConfig": {
    "defaults": {"unit": "bytes", "custom": {"spanNulls": false}},
    "overrides": []
  },
  "options": {
    "legend": {"displayMode": "table", "placement": "right", "calcs": ["last", "max"]}
  },
  "targets": [
    {
      "datasource": {"type": "prometheus", "uid": "prometheus"},
      "expr": "ecs_cluster_recovery_bad_chunks_bytes{cluster=~\"$cluster\"}",
      "legendFormat": "{{cluster}}",
      "refId": "A"
    }
  ]
}
```

The remaining five panels, in order, with `gridPos` laid out two per row
(`w: 12`, `x: 0` then `x: 12`, `y` increasing by 8):

| id | Title | Type | Unit | Expressions (legend) |
|---|---|---|---|---|
| 2 | GC backlog by scope | `timeseries` | `bytes` | `ecs_cluster_gc_pending_bytes{cluster=~"$cluster"}` (`pending {{scope}}`), `ecs_cluster_gc_unreclaimable_bytes{cluster=~"$cluster"}` (`unreclaimable {{scope}}`) |
| 3 | GC reclaimed vs detected | `timeseries` | `bytes` | `ecs_cluster_gc_detected_bytes{cluster=~"$cluster"}` (`detected {{scope}}`), `ecs_cluster_gc_reclaimed_bytes{cluster=~"$cluster"}` (`reclaimed {{scope}}`) |
| 4 | Erasure-coded share | `stat` | `percent` | `ecs_cluster_ec_coded_ratio_percent{cluster=~"$cluster"}` (`{{cluster}}`) |
| 5 | Erasure-coding backlog | `timeseries` | `bytes` | `ecs_cluster_ec_applicable_bytes{cluster=~"$cluster"} - ecs_cluster_ec_coded_bytes{cluster=~"$cluster"}` (`uncoded {{cluster}}`) |
| 6 | Allocated space by purpose | `timeseries` | `bytes` | `ecs_cluster_disk_space_allocated_component_bytes{cluster=~"$cluster"}` (`{{purpose}} {{cluster}}`) |

Panel 6 needs this description, because the caveat matters at a glance:
`"These components do not sum to the allocated total — the breakdown is not exhaustive."`

- [ ] **Step 4: Validate the dashboard JSON and the docs build**

Run:

```bash
python3 -c "import json; d=json.load(open('grafana/dashboards/obs-storage-internals.json')); print(d['uid'], len(d['panels']), 'panels')"
uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict
```

Expected: `obs-storage-internals 6 panels`, and the docs build succeeds.

- [ ] **Step 5: Commit**

```bash
git add docs/metrics.md CHANGELOG.md grafana/dashboards/obs-storage-internals.json
git commit -m "docs: document the background-process metrics and add their dashboard

Both caveats are stated where they can be read: the allocation breakdown does not
sum to the allocated total, and there is no scope=combined."
```

---

### Task 9: Full gate and pull request

**Files:** none modified.

- [ ] **Step 1: Run the full CI gate**

Run: `make ci`

Expected: PASS — `fmt-check`, `vet`, `golangci-lint` (0 issues), `go test -race`,
`govulncheck` (no vulnerabilities), `build`. If `golangci-lint` flags anything,
restructure; **do not** add a suppression comment.

- [ ] **Step 2: Confirm the label-key invariant and both export paths**

Run: `go test ./internal/ecs/ -run 'TestLabelKeyConsistency|TestPromCollector|TestOTLP|TestClusterCollect|TestLocalZoneLivePayloadShape' -v`

Expected: PASS. This is the guard on ADR-0006 and on the CLAUDE.md rule that both
export paths cover new metrics.

- [ ] **Step 3: Verify the fixture copies are identical**

Run:

```bash
diff internal/ecs/testdata/localzone.json cmd/mockecs/fixtures/localzone.json && echo "localzone fixtures in sync"
```

Expected: no diff, then `localzone fixtures in sync`. The live-payload fixture is
intentionally absent from `cmd/mockecs/fixtures/` — do not copy it there.

- [ ] **Step 4: Push the branch**

```bash
git push -u origin feat/cluster-background-metrics
```

- [ ] **Step 5: Open the pull request**

```bash
gh pr create --title "feat(ecs): cluster background-process metrics" --body "$(cat <<'EOF'
Design: `docs/superpowers/specs/2026-07-29-cluster-background-metrics-design.md`.

Adds four metric families from the local-zone dashboard response the exporter
already fetches once per cycle — garbage collection, chunk recovery, erasure
coding, and the allocated-space breakdown. No new endpoint, no extra API call,
no feature flag.

Grounded in a sanitized trace from a live ObjectScale 4.3 cluster contributed on
#18. Two measurements shaped the design:

- GC `combined` equals `user + system` exactly, to the byte, on all four
  measures, so it is not exported — `sum without(scope)` reproduces it without
  double-counting.
- The five allocation components do **not** sum to the allocated total (12.8%
  unaccounted), so they get their own metric name rather than a label on the
  published total, and the docs say plainly that percentages must not be computed
  from them.

Missing or unparseable fields yield absent samples, never zeros. A family absent
in its entirety raises no warning: unlike a missing HAL key, that is a legitimate
version difference on 4.1 and 4.2 clusters.

A new test decodes an unedited capture of the real payload and asserts that every
family produces samples — hand-written fixtures cannot catch a misspelled struct
tag, because they carry the same misspelling as the code.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01P9vAnqpQXacSLWcgudSwcb
EOF
)"
```

- [ ] **Step 6: Report the result**

Print the PR URL and the `make ci` outcome.

---

## Self-review notes

- **Spec coverage:** metrics table → Tasks 2-5; embedded-struct architecture →
  Tasks 2-5 Step 5; `Bool` primitive → Task 1; per-family contract → Tasks 2-5
  Step 3; absence handling → the "empty payload" and "unparseable value" cases in
  every family test; additive fixture with deliberate omissions → Tasks 2-5 Step 6
  (`gcSystemMetadataIsEnabled` omitted, geo purposes omitted, `recoveryRateCurrent`
  set to `"N/A"`); real-payload fixture → Task 6; per-family tests → Tasks 2-5;
  integration and export paths → Tasks 2-5 Step 7 and Task 7; documentation and
  dashboard → Task 8; gate → Task 9.
- **Type consistency:** `gcFields`, `recoveryFields`, `erasureCodingFields` and
  `allocationComponentFields` are each declared once and referenced by those exact
  names in `localZoneResp`, in `Collect`, and in Task 6's shape test. Every family
  exposes `samples() []Sample` with a value receiver. `Bool` is declared in Task 1
  with fields `Val`/`Set` and used under those names in Task 2.
- **Non-goals confirmed absent:** no chunk-inventory metrics, no
  `gc*ReclaimedOverTimeRange` or `*PerInterval`, no `allocatedCapacityForecast`,
  no node storage-pool label, no new endpoint.
