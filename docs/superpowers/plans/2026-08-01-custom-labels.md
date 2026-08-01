# Custom Labels Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an operator attach custom labels to every exported sample, with keys declared once globally and values overridable per cluster.

**Architecture:** `config.Load` parses and validates a top-level `labels:` block plus per-cluster value overrides, and `Config.EffectiveLabels` resolves them into a key-sorted slice. `main.go` hands that slice to each `ecs.Target`. The collection loop stamps it onto every sample through `Sample.WithIdentity`, which generalises the existing `WithCluster`. Both export paths read the finished label slice and need no change.

**Tech Stack:** Go 1.26.4, `gopkg.in/yaml.v2`, `github.com/sirupsen/logrus` (+ `logrus/hooks/test` for the warning test), `github.com/prometheus/client_golang`, OpenTelemetry Go SDK.

**Spec:** `docs/superpowers/specs/2026-08-01-custom-labels-design.md`

## Global Constraints

- Label keys match `^[a-zA-Z_][a-zA-Z0-9_]*$`; a `__` prefix is rejected (reserved by Prometheus).
- No label value is ever empty — global and per-cluster values are both validated non-empty after `${ENV}` interpolation.
- A cluster may override the value of a globally declared key; it may never introduce a key.
- Label order is `cluster`, then the custom labels sorted by key, then the collector's own labels. ADR-0006 makes the ordered key set part of a metric's schema, so the order must never depend on YAML authoring order or Go map iteration order.
- Custom labels apply to **every** sample, `ecs_up` and `ecs_collector_up` included.
- A custom key that collides with a dimension the collector already emits is skipped for that sample and logged `WARN` once per key per cluster. Never fail the cycle.
- No inline `nosemgrep` or `//nolint` suppressions — restructure instead.
- `make sure` must pass before every commit; `make ci` before the final one.

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/config/config.go` | `Label` type, `Labels` fields, interpolation, validation, `EffectiveLabels` |
| `internal/config/config_test.go` | validation and resolution tests |
| `internal/ecs/sample.go` | `WithIdentity`, `hasLabel`, `CollidingLabels`; `WithCluster` kept as a thin wrapper |
| `internal/ecs/sample_test.go` | ordering, collision skip, nil-equivalence tests |
| `internal/ecs/collector.go` | `Target.Labels`, three stamping call sites, once-per-key collision warning |
| `internal/ecs/collector_test.go` | labels reach `ecs_up`/`ecs_collector_up`; warning fires once |
| `internal/ecs/prometheus_test.go`, `internal/ecs/otlp_test.go` | both export paths carry the custom keys |
| `main.go` | `buildTargets` converts `config.Label` to `ecs.Label` |
| `config.yaml`, `charts/obs-exporter/values.yaml` | commented examples |
| `docs/…`, `README.md`, `CHANGELOG.md`, `docs/adr/0014-…md` | documentation |
| `grafana/dashboards/obs-*.json` | ad hoc filters variable |

---

### Task 1: Config schema, validation, and resolution

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `type config.Label struct { Key, Value string }`
  - `Config.Labels map[string]string` (yaml `labels`)
  - `Cluster.Labels map[string]string` (yaml `labels`)
  - `func (c Config) EffectiveLabels(cl Cluster) []Label` — global map with the cluster's overrides applied, sorted by key, `nil` when no global block exists.

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/config_test.go`:

```go
func TestLoadLabels(t *testing.T) {
	t.Setenv("TEAM_NAME", "storage-ops")
	p := write(t, `
labels:
  site: geneva
  env: prod
  owner: ${TEAM_NAME}
clusters:
  - name: ecs1
    host: ecs1.example.com
    labels:
      site: zurich
  - name: ecs2
    host: ecs2.example.com
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}

	got := cfg.EffectiveLabels(cfg.Clusters[0])
	want := []Label{{"env", "prod"}, {"owner", "storage-ops"}, {"site", "zurich"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EffectiveLabels(ecs1) = %v, want %v", got, want)
	}

	got = cfg.EffectiveLabels(cfg.Clusters[1])
	want = []Label{{"env", "prod"}, {"owner", "storage-ops"}, {"site", "geneva"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EffectiveLabels(ecs2) = %v, want %v", got, want)
	}
}

func TestEffectiveLabelsEmptyWithoutGlobalBlock(t *testing.T) {
	p := write(t, `
clusters:
  - name: ecs1
    host: ecs1.example.com
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.EffectiveLabels(cfg.Clusters[0]); got != nil {
		t.Errorf("EffectiveLabels = %v, want nil", got)
	}
}

func TestLoadLabelsRejected(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "invalid key",
			yaml: "labels:\n  \"my-site\": geneva\n",
			want: "must match",
		},
		{
			name: "reserved prefix",
			yaml: "labels:\n  __site: geneva\n",
			want: "reserved",
		},
		{
			name: "empty global value",
			yaml: "labels:\n  site: \"\"\n",
			want: "must not be empty",
		},
		{
			name: "undeclared cluster key",
			yaml: "labels:\n  site: geneva\n",
			want: "unknown label key",
		},
		{
			name: "cluster labels without global block",
			yaml: "",
			want: "unknown label key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cluster := "clusters:\n  - name: ecs1\n    host: ecs1.example.com\n"
			switch tc.name {
			case "undeclared cluster key", "cluster labels without global block":
				cluster += "    labels:\n      rack: r12\n"
			}
			_, err := Load(write(t, tc.yaml+cluster))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestLoadLabelsEmptyClusterValue(t *testing.T) {
	p := write(t, `
labels:
  site: geneva
clusters:
  - name: ecs1
    host: ecs1.example.com
    labels:
      site: ""
`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("err = %v, want an empty-value error", err)
	}
}

func TestLoadLabelsUnsetEnvVar(t *testing.T) {
	p := write(t, `
labels:
  owner: ${OBS_LABELS_UNSET_VAR}
clusters:
  - name: ecs1
    host: ecs1.example.com
`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "unset environment variable") {
		t.Fatalf("err = %v, want an unset-variable error", err)
	}
}
```

Add `"reflect"` and `"strings"` to the test file's imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config -run 'TestLoadLabels|TestEffectiveLabels' -v`
Expected: FAIL — compilation error, `Label`, `cfg.Labels` and `EffectiveLabels` are undefined.

- [ ] **Step 3: Implement the config changes**

In `internal/config/config.go`, add `"slices"` to the imports and add the `Labels` field to `Cluster` (after `CollectQuotas`):

```go
	// Labels holds this cluster's overrides for the globally declared custom
	// label values. A key absent from the top-level labels block is a config
	// error: the key set stays uniform across clusters (ADR-0006), only values
	// vary.
	Labels map[string]string `yaml:"labels"`
```

Add the `Labels` field to `Config`:

```go
// Config is the whole file.
type Config struct {
	Server     Server     `yaml:"server"`
	Collection Collection `yaml:"collection"`
	OTLP       OTLP       `yaml:"otlp"`
	// Labels declares the custom label KEYS with their default values. Clusters
	// may override a value; they may never add a key. Declaring the key set once
	// is what keeps "no value is ever empty" true by construction.
	Labels   map[string]string `yaml:"labels"`
	Clusters []Cluster         `yaml:"clusters"`
}

// Label is one resolved custom label. Values reach here already interpolated.
type Label struct {
	Key   string
	Value string
}

var labelKeyRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
```

Add the validation and resolution helpers:

```go
// interpolateLabels resolves ${ENV} references in a label map in place. Keys are
// never interpolated: they are part of the metric schema, not a secret.
func interpolateLabels(labels map[string]string, what string) error {
	for k, v := range labels {
		iv, err := interpolate(v)
		if err != nil {
			return fmt.Errorf("%s label %s: %w", what, k, err)
		}
		labels[k] = iv
	}
	return nil
}

// validateLabels enforces the custom-label model: globally declared keys with
// non-empty values, and per-cluster overrides that may only restate a declared
// key. Rejecting an undeclared cluster key at load is what keeps the label-key
// set identical across clusters, as ADR-0006 requires.
func validateLabels(cfg *Config) error {
	for k, v := range cfg.Labels {
		if !labelKeyRE.MatchString(k) {
			return fmt.Errorf("label %q: key must match [a-zA-Z_][a-zA-Z0-9_]*", k)
		}
		if strings.HasPrefix(k, "__") {
			return fmt.Errorf("label %q: keys starting with __ are reserved by Prometheus", k)
		}
		if v == "" {
			return fmt.Errorf("label %q: value must not be empty", k)
		}
	}
	for _, c := range cfg.Clusters {
		for k, v := range c.Labels {
			if _, ok := cfg.Labels[k]; !ok {
				return fmt.Errorf("cluster %q: unknown label key %q (declare it in the top-level labels block)", c.Name, k)
			}
			if v == "" {
				return fmt.Errorf("cluster %q: label %q: value must not be empty", c.Name, k)
			}
		}
	}
	return nil
}

// EffectiveLabels returns one cluster's custom labels: the global block with
// that cluster's value overrides applied, sorted by key. Sorted because ADR-0006
// makes the ordered label-key set part of a metric's schema, so the order must
// not depend on YAML authoring order or Go map iteration order.
func (c Config) EffectiveLabels(cl Cluster) []Label {
	if len(c.Labels) == 0 {
		return nil
	}
	out := make([]Label, 0, len(c.Labels))
	for k, v := range c.Labels {
		if ov, ok := cl.Labels[k]; ok {
			v = ov
		}
		out = append(out, Label{Key: k, Value: v})
	}
	slices.SortFunc(out, func(a, b Label) int { return strings.Compare(a.Key, b.Key) })
	return out
}
```

Wire them into `Load`. Immediately after `yaml.Unmarshal` succeeds, interpolate the global block:

```go
	if err := interpolateLabels(cfg.Labels, "global"); err != nil {
		return nil, err
	}
```

Inside the existing `for i := range cfg.Clusters` loop, after the `InsecureSkipVerify` resolution and before the port defaults:

```go
		if err := interpolateLabels(c.Labels, "cluster "+c.Name); err != nil {
			return nil, err
		}
```

And just before the final `return &cfg, nil`, after the `no clusters configured` check:

```go
	if err := validateLabels(&cfg); err != nil {
		return nil, err
	}
```

Validation runs last so cluster names have already been defaulted from `host`, and its error messages name the cluster the operator sees in the metrics.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config -v`
Expected: PASS, including the pre-existing tests.

- [ ] **Step 5: Commit**

```bash
make sure
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): declare custom labels globally with per-cluster value overrides"
```

---

### Task 2: Sample.WithIdentity

**Files:**
- Modify: `internal/ecs/sample.go`
- Test: `internal/ecs/sample_test.go`

**Interfaces:**
- Consumes: nothing from Task 1 — `ecs.Label` already exists and is independent of `config.Label`.
- Produces:
  - `func (s Sample) WithIdentity(name string, extra []Label) Sample`
  - `func (s Sample) CollidingLabels(extra []Label) []string`
  - `WithCluster(name)` stays, now equivalent to `WithIdentity(name, nil)`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/ecs/sample_test.go`:

```go
func TestWithIdentityOrder(t *testing.T) {
	s := Sample{
		Name:   "ecs_namespace_used_bytes",
		Labels: []Label{{Key: "namespace", Value: "ns1"}},
		Value:  42,
	}
	extra := []Label{{Key: "env", Value: "prod"}, {Key: "site", Value: "geneva"}}

	got := s.WithIdentity("c1", extra)
	want := []Label{
		{Key: "cluster", Value: "c1"},
		{Key: "env", Value: "prod"},
		{Key: "site", Value: "geneva"},
		{Key: "namespace", Value: "ns1"},
	}
	if !reflect.DeepEqual(got.Labels, want) {
		t.Errorf("labels = %v, want %v", got.Labels, want)
	}
	if got.Name != s.Name || got.Value != s.Value {
		t.Errorf("name/value not preserved: %+v", got)
	}
}

func TestWithIdentitySkipsCollisions(t *testing.T) {
	s := Sample{Name: "ecs_node_health_state", Labels: []Label{{Key: "node", Value: "n1"}}}
	extra := []Label{
		{Key: "cluster", Value: "wrong"},
		{Key: "env", Value: "prod"},
		{Key: "node", Value: "wrong"},
	}

	got := s.WithIdentity("c1", extra)
	want := []Label{
		{Key: "cluster", Value: "c1"},
		{Key: "env", Value: "prod"},
		{Key: "node", Value: "n1"},
	}
	if !reflect.DeepEqual(got.Labels, want) {
		t.Errorf("labels = %v, want %v", got.Labels, want)
	}

	collisions := s.CollidingLabels(extra)
	if !reflect.DeepEqual(collisions, []string{"cluster", "node"}) {
		t.Errorf("CollidingLabels = %v, want [cluster node]", collisions)
	}
}

func TestWithIdentityNilExtraMatchesWithCluster(t *testing.T) {
	s := Sample{Name: "ecs_up", Labels: []Label{{Key: "collector", Value: "cluster"}}, Value: 1}
	if !reflect.DeepEqual(s.WithIdentity("c1", nil).Labels, s.WithCluster("c1").Labels) {
		t.Error("WithIdentity(name, nil) must match WithCluster(name)")
	}
}

func TestWithIdentityKeepsEmptyValuedCollision(t *testing.T) {
	// A collector dimension whose value is empty is still a dimension: the
	// custom label must be skipped, not merged over it.
	s := Sample{Name: "ecs_cluster_alerts", Labels: []Label{{Key: "severity", Value: ""}}}
	got := s.WithIdentity("c1", []Label{{Key: "severity", Value: "critical"}})
	want := []Label{{Key: "cluster", Value: "c1"}, {Key: "severity", Value: ""}}
	if !reflect.DeepEqual(got.Labels, want) {
		t.Errorf("labels = %v, want %v", got.Labels, want)
	}
}
```

Add `"reflect"` to the test file's imports if it is not already there.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/ecs -run TestWithIdentity -v`
Expected: FAIL — `WithIdentity` and `CollidingLabels` undefined.

- [ ] **Step 3: Implement**

In `internal/ecs/sample.go`, replace the `WithCluster` function with:

```go
// WithCluster returns a copy with a leading {cluster=name} identity label.
// Collectors emit cluster-agnostic samples; the collection loop stamps the
// cluster identity so one exporter process can serve many clusters.
func (s Sample) WithCluster(name string) Sample { return s.WithIdentity(name, nil) }

// WithIdentity returns a copy carrying the {cluster=name} identity label first,
// then the operator's custom labels, then the sample's own collector labels.
// The caller passes extra already sorted by key: ADR-0006 makes the ordered
// label-key set part of a metric's schema.
//
// A custom label is skipped when its key is the reserved cluster identity or a
// dimension the sample already carries — the collector's own dimension wins.
// ADR-0006 guarantees one key set per metric name, so a skip applies uniformly
// to every series of that name and the label-key invariant still holds; the
// custom label is simply absent from that metric family. The collection loop
// logs the skip, so it is not silent.
func (s Sample) WithIdentity(name string, extra []Label) Sample {
	labels := make([]Label, 0, len(s.Labels)+len(extra)+1)
	labels = append(labels, Label{Key: "cluster", Value: name})
	for _, l := range extra {
		if l.Key == "cluster" || s.hasLabel(l.Key) {
			continue
		}
		labels = append(labels, l)
	}
	labels = append(labels, s.Labels...)
	return Sample{Name: s.Name, Labels: labels, Value: s.Value, Type: s.Type}
}

// CollidingLabels returns the keys of extra that WithIdentity will skip for this
// sample, in the order they appear in extra.
func (s Sample) CollidingLabels(extra []Label) []string {
	var out []string
	for _, l := range extra {
		if l.Key == "cluster" || s.hasLabel(l.Key) {
			out = append(out, l.Key)
		}
	}
	return out
}

// hasLabel reports whether the sample carries the key, regardless of its value.
// LabelValue cannot answer this: a dimension with an empty value is still a
// dimension.
func (s Sample) hasLabel(key string) bool {
	for _, l := range s.Labels {
		if l.Key == key {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/ecs -run 'TestWithIdentity|TestWithCluster' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
make sure
git add internal/ecs/sample.go internal/ecs/sample_test.go
git commit -m "feat(ecs): generalise WithCluster into WithIdentity with custom labels"
```

---

### Task 3: Stamp custom labels in the collection loop

**Files:**
- Modify: `internal/ecs/collector.go`
- Test: `internal/ecs/collector_test.go`

**Interfaces:**
- Consumes: `Sample.WithIdentity`, `Sample.CollidingLabels` (Task 2).
- Produces: `ecs.Target.Labels []Label`, read by `main.go` in Task 4.

- [ ] **Step 1: Write the failing tests**

Append to `internal/ecs/collector_test.go`:

```go
func labelKeys(s Sample) []string {
	keys := make([]string, len(s.Labels))
	for i, l := range s.Labels {
		keys[i] = l.Key
	}
	return keys
}

func TestCollectStampsCustomLabels(t *testing.T) {
	targets := testTargets(t)
	targets[0].Labels = []Label{{Key: "env", Value: "prod"}, {Key: "site", Value: "geneva"}}

	store := NewSnapshotStore()
	col := NewCollector(targets, store, time.Minute, 10*time.Second)
	snap := col.CollectOnce(context.Background())

	var checked int
	for _, s := range snap.Clusters[0].Samples {
		if s.LabelValue("env") != "prod" || s.LabelValue("site") != "geneva" {
			t.Fatalf("sample %s missing custom labels: %v", s.Name, s.Labels)
		}
		if s.Name == "ecs_up" || s.Name == "ecs_collector_up" {
			checked++
			want := []string{"cluster", "env", "site"}
			if s.Name == "ecs_collector_up" {
				want = append(want, "collector")
			}
			if !slices.Equal(labelKeys(s), want) {
				t.Errorf("%s label keys = %v, want %v", s.Name, labelKeys(s), want)
			}
		}
	}
	if checked == 0 {
		t.Fatal("neither ecs_up nor ecs_collector_up was present")
	}
	assertLabelKeySchema(t, snap.Clusters[0].Samples)
}

func TestCollectWarnsOnceOnLabelCollision(t *testing.T) {
	hook := test.NewGlobal()
	defer hook.Reset()

	targets := testTargets(t)
	targets[0].Labels = []Label{{Key: "collector", Value: "mine"}}

	store := NewSnapshotStore()
	col := NewCollector(targets, store, time.Minute, 10*time.Second)
	col.CollectOnce(context.Background())
	col.CollectOnce(context.Background())

	var warnings int
	for _, e := range hook.AllEntries() {
		if e.Level == logrus.WarnLevel && strings.Contains(e.Message, "custom label collides") {
			warnings++
		}
	}
	if warnings != 1 {
		t.Errorf("collision warnings = %d over two cycles, want 1", warnings)
	}

	snap := store.Load()
	for _, s := range snap.Clusters[0].Samples {
		if s.Name == "ecs_collector_up" && s.LabelValue("collector") == "mine" {
			t.Fatal("custom label overwrote the collector dimension")
		}
	}
}
```

Add `"strings"`, `"github.com/sirupsen/logrus"` and `"github.com/sirupsen/logrus/hooks/test"` to the test file's imports (`slices` is already imported).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/ecs -run 'TestCollectStampsCustomLabels|TestCollectWarnsOnce' -v`
Expected: FAIL — `Target` has no field `Labels`.

- [ ] **Step 3: Implement**

In `internal/ecs/collector.go`, add `"sync"` to the imports and extend `Target`:

```go
// Target pairs a cluster client with its resource collectors (which depend on
// per-cluster feature flags) and the operator's custom labels for that cluster.
type Target struct {
	Client     ecsclient.Client
	Collectors []ResourceCollector
	// Labels are the resolved custom labels, already sorted by key, stamped onto
	// every sample this target produces.
	Labels []Label
}
```

Extend `Collector` with the warning-dedup state:

```go
	// PostCycle, when set, runs after every published snapshot (the OTLP exporter
	// uses it to register instruments for newly appearing metric names).
	PostCycle func()

	// warnMu guards warned, which keeps the custom-label collision warning to one
	// line per cluster and key for the process's lifetime instead of one per
	// sample per cycle. collectAll runs clusters concurrently, so this is shared
	// mutable state.
	warnMu sync.Mutex
	warned map[string]struct{}
```

Add the warning helper below `collectCluster`:

```go
// warnCollision logs a dropped custom label once per cluster and key. metric
// names the first metric family the collision was seen on, which is enough for
// the operator to recognise the dimension they clashed with.
func (c *Collector) warnCollision(cluster, key, metric string) {
	c.warnMu.Lock()
	defer c.warnMu.Unlock()
	if c.warned == nil {
		c.warned = map[string]struct{}{}
	}
	id := cluster + "\x00" + key
	if _, seen := c.warned[id]; seen {
		return
	}
	c.warned[id] = struct{}{}
	log.WithFields(log.Fields{"cluster": cluster, "label": key, "metric": metric}).
		Warn("custom label collides with a collector dimension; dropped for that metric family")
}
```

In `collectCluster`, replace the three `WithCluster` call sites. The `ecs_collector_up` block becomes:

```go
		up := 1.0
		if err != nil {
			up = 0
			failures++
			lastErr = err
			log.WithFields(log.Fields{"cluster": name, "collector": rc.Name(), "err": err}).
				Warn("collector failed")
		}
		collectorUp := Sample{
			Name:   "ecs_collector_up",
			Labels: []Label{{Key: "collector", Value: rc.Name()}},
			Value:  up,
		}
		for _, k := range collectorUp.CollidingLabels(target.Labels) {
			c.warnCollision(name, k, collectorUp.Name)
		}
		cs.Samples = append(cs.Samples, collectorUp.WithIdentity(name, target.Labels))
		for _, s := range samples {
			for _, k := range s.CollidingLabels(target.Labels) {
				c.warnCollision(name, k, s.Name)
			}
			cs.Samples = append(cs.Samples, s.WithIdentity(name, target.Labels))
```

Leave the `unmappedNodesMetric` skip and the `domainSamples++` line that follow untouched.

The `ecs_up` line at the end of the function becomes:

```go
	cs.Samples = append(cs.Samples, Sample{Name: "ecs_up", Value: up}.WithIdentity(name, target.Labels))
```

`CollidingLabels` returns nil when `target.Labels` is empty, so a configuration with no custom labels does no extra work beyond one nil-slice loop per sample.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/ecs -v`
Expected: PASS, `TestLabelKeyConsistency` and `TestLabelKeyConsistencyFlux` included.

- [ ] **Step 5: Commit**

```bash
make sure
git add internal/ecs/collector.go internal/ecs/collector_test.go
git commit -m "feat(ecs): stamp custom labels on every sample in the collection loop"
```

---

### Task 4: Wire the config through main.go and assert both export paths

**Files:**
- Modify: `main.go:247-260` (`buildTargets`)
- Modify: `config.yaml`
- Test: `internal/ecs/prometheus_test.go`, `internal/ecs/otlp_test.go`

**Interfaces:**
- Consumes: `config.Config.EffectiveLabels` (Task 1), `ecs.Target.Labels` (Task 3).
- Produces: nothing further; this closes the runtime path.

- [ ] **Step 1: Write the failing tests**

Append to `internal/ecs/prometheus_test.go`:

```go
func TestPromCollectorExportsCustomLabels(t *testing.T) {
	targets := testTargets(t)
	targets[0].Labels = []Label{{Key: "env", Value: "prod"}}

	store := NewSnapshotStore()
	col := NewCollector(targets, store, time.Minute, 10*time.Second)
	col.CollectOnce(t.Context())

	reg := prometheus.NewRegistry()
	reg.MustRegister(NewPromCollector(store))
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(mfs) == 0 {
		t.Fatal("no metric families gathered")
	}
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			var found bool
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "env" && lp.GetValue() == "prod" {
					found = true
				}
			}
			if !found {
				t.Fatalf("metric %s exported without env=prod", mf.GetName())
			}
		}
	}
}
```

Append to `internal/ecs/otlp_test.go`:

```go
func TestOTLPExporterExportsCustomLabels(t *testing.T) {
	targets := testTargets(t)
	targets[0].Labels = []Label{{Key: "env", Value: "prod"}}

	store := NewSnapshotStore()
	col := NewCollector(targets, store, time.Minute, 10*time.Second)
	reader := sdkmetric.NewManualReader()
	exp := newOTLPExporter(reader, store, "test")
	col.PostCycle = func() {
		if err := exp.EnsureInstruments(); err != nil {
			t.Errorf("EnsureInstruments: %v", err)
		}
	}
	col.CollectOnce(context.Background())

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}

	var points int
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			g, ok := m.Data.(metricdata.Gauge[float64])
			if !ok {
				continue
			}
			for _, dp := range g.DataPoints {
				points++
				v, ok := dp.Attributes.Value(attribute.Key("env"))
				if !ok || v.AsString() != "prod" {
					t.Fatalf("metric %s data point without env=prod", m.Name)
				}
			}
		}
	}
	if points == 0 {
		t.Fatal("no gauge data points observed")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/ecs -run 'ExportsCustomLabels' -v`
Expected: PASS for the collector-level assertions only if Task 3 landed; run it now and confirm. If either fails, the export path is dropping labels and must be fixed before continuing. These two tests are a regression guard, not a new feature: they exist because the repo requires export behaviour to be asserted through both paths.

- [ ] **Step 3: Wire buildTargets**

In `main.go`, replace `buildTargets` (lines 247-260) with:

```go
// buildTargets constructs one ECS client (plus its collector set and resolved
// custom labels) per configured cluster.
func buildTargets(cfg *config.Config, trace bool) []ecs.Target {
	targets := make([]ecs.Target, 0, len(cfg.Clusters))
	for _, cl := range cfg.Clusters {
		client := ecsclient.NewClusterClient(ecsclient.Config{
			Name: cl.Name, BaseURL: cl.BaseURL(), Username: cl.Username,
			Password: cl.Password, InsecureSkipVerify: cl.InsecureSkipVerify.Bool(),
			Trace: trace,
		})
		resolved := cfg.EffectiveLabels(cl)
		labels := make([]ecs.Label, 0, len(resolved))
		for _, l := range resolved {
			labels = append(labels, ecs.Label{Key: l.Key, Value: l.Value})
		}
		targets = append(targets, ecs.Target{
			Client: client, Collectors: ecs.Registry(cl), Labels: labels,
		})
	}
	return targets
}
```

`config` does not import `ecs`, so the conversion lives here. Hot reload needs nothing more: `collectorRunner` already calls `buildTargets` on every reload.

- [ ] **Step 4: Add the commented example to config.yaml**

In `config.yaml`, above the `clusters:` block:

```yaml
# Optional custom labels stamped onto every exported sample, ecs_up included.
# The top-level block declares the KEYS with their default values; a cluster may
# override a VALUE but never introduce a key, so every series carries the same
# label-key set. Values accept ${ENV_VAR} interpolation.
# labels:
#   env: prod
#   site: geneva
#   owner: ${TEAM_NAME}
```

And inside the first cluster entry, beside the existing commented flags:

```yaml
    # labels:
    #   site: zurich    # overrides the global value for this cluster only
```

- [ ] **Step 5: Verify the whole path end to end**

Run: `go build ./... && go test ./... && ./bin/obs_exporter --help >/dev/null`
Then build and dump against the mock: `make cli && make demo` is heavier than needed here; instead confirm `go vet ./...` is clean and the unit suites pass.
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
make sure
git add main.go config.yaml internal/ecs/prometheus_test.go internal/ecs/otlp_test.go
git commit -m "feat: resolve custom labels per cluster and assert both export paths"
```

---

### Task 5: Documentation, chart, and ADR-0014

**Files:**
- Create: `docs/adr/0014-custom-labels.md`
- Modify: `docs/adr/index.md`, `README.md`, `docs/getting-started/configuration.md`, `docs/getting-started/first-run.md`, `docs/deployment/kubernetes.md`, `docs/metrics/reading.md`, `charts/obs-exporter/values.yaml`, `CHANGELOG.md`

**Interfaces:**
- Consumes: the config schema from Task 1 — every example must match it exactly.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Write ADR-0014**

Create `docs/adr/0014-custom-labels.md`, following the shape of `docs/adr/0006-metric-naming-units-and-label-invariant.md`:

```markdown
# Custom labels: global keys, per-cluster values

## Status

Accepted (2026-08-01)

## Context

One exporter process serves many clusters, so Prometheus sees a single scrape
target and target relabeling cannot attach a label that varies per cluster:
every series from the process would get the same value. Only the exporter knows
which cluster a sample came from. Operators asked for site, environment and
ownership labels that differ per cluster.

ADR-0006 makes the ordered label-key set part of a metric's schema, so any
label mechanism has to keep that key set uniform across clusters.

## Decision

- A top-level `labels:` block declares the label **keys** with default values.
- A cluster's `labels:` block may override a declared key's **value**; an
  undeclared key is a config-load error.
- Values are validated non-empty and support `${ENV_VAR}` interpolation; keys
  match `[a-zA-Z_][a-zA-Z0-9_]*` and may not start with `__`.
- Labels are stamped onto **every** sample, `ecs_up` and `ecs_collector_up`
  included, by `Sample.WithIdentity` in the collection loop — the same choke
  point that already stamps the `cluster` identity.
- Order is `cluster`, then the custom labels sorted by key, then the collector's
  own labels.
- A custom key colliding with a collector's own dimension is dropped for that
  metric family and logged once per key per cluster. The collector's dimension
  wins.
- OTLP carries them as data-point attributes, not resource attributes.

## Consequences

- The key set stays uniform and no value is ever empty, both by construction, so
  the ADR-0006 invariant holds without a completion pass.
- Operators cannot add a cluster-specific key; that is the price of the
  invariant.
- A colliding key silently loses that metric family's labelling, mitigated only
  by the log line — the collision is uniform per metric name, so it never
  produces mixed series schemas.
- Grafana dashboards cannot hardcode operator-defined keys; they ship an ad hoc
  filters variable instead.
```

Add the row to `docs/adr/index.md` in the same format as the existing entries.

- [ ] **Step 2: Write the configuration reference section**

In `docs/getting-started/configuration.md`, add a `## Custom labels` section covering: the YAML example from `config.yaml`, the keys-global/values-per-cluster rule and why (ADR-0006 uniformity, no empty values), `${ENV_VAR}` support, the collision rule and its log line, that labels land on every metric including `ecs_up`, and the reason this cannot be done with Prometheus target relabeling (one process, one target, many clusters).

- [ ] **Step 3: Update the remaining docs**

- `README.md` — the configuration example gains the commented `labels:` block; the feature list gains one line.
- `docs/getting-started/first-run.md` — mention that custom labels appear on every series in the first scrape.
- `docs/deployment/kubernetes.md` — show `labels:` inside the chart's inline `config` value.
- `docs/metrics/reading.md` — note that operator-defined labels may appear on every series in addition to the documented dimensions.
- `charts/obs-exporter/values.yaml` — add the commented `labels:` block inside the `config: |` string (the chart passes the config through verbatim into a Secret, so no template changes are needed).
- `CHANGELOG.md` — an `Added` entry under the unreleased heading: custom labels, global keys with per-cluster value overrides, applied to every metric.

Not `docs/metrics/index.md`: no new metric.

- [ ] **Step 4: Verify the docs build**

Run: `uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict`
Expected: build succeeds with no warnings.

- [ ] **Step 5: Commit**

```bash
git add docs README.md CHANGELOG.md charts/obs-exporter/values.yaml
git commit -m "docs: document custom labels and record ADR-0014"
```

---

### Task 6: Grafana ad hoc filters variable

**Files:**
- Modify: `grafana/dashboards/obs-overview.json`, `obs-maintenance.json`, `obs-namespaces.json`, `obs-nodes.json`, `obs-performance.json`, `obs-replication.json`, `obs-storage-internals.json`
- Leave alone: `grafana/dashboards/node-exporter-full.json` (imported upstream)

**Interfaces:**
- Consumes: nothing from earlier tasks; dashboards never name a custom key.
- Produces: nothing.

- [ ] **Step 1: Add the variable to each dashboard**

In every `obs-*.json`, append this object to `templating.list`, after the existing `cluster` (and, where present, `node`/`namespace`) variables:

```json
{
  "name": "custom",
  "label": "Custom labels",
  "type": "adhoc",
  "datasource": {
    "type": "prometheus",
    "uid": "prometheus"
  },
  "filters": []
}
```

No panel query changes: Grafana appends the operator's chosen matchers to every Prometheus query in the dashboard and autocompletes keys and values from the datasource, so nothing hardcodes a key name.

- [ ] **Step 2: Verify the JSON is well formed**

Run: `for f in grafana/dashboards/obs-*.json; do python3 -m json.tool "$f" >/dev/null || echo "BAD $f"; done`
Expected: no output.

- [ ] **Step 3: Check the variable landed in all seven**

Run: `grep -l '"type": "adhoc"' grafana/dashboards/obs-*.json | wc -l`
Expected: `7`.

- [ ] **Step 4: Document the two limits**

In `docs/getting-started/configuration.md`, at the end of the custom-labels section, state plainly:

- ad hoc filters apply to panel queries, not to variable queries, so the `cluster` picker still lists every cluster even while filtering on `env=prod`. Confirm the behaviour against the Grafana version in use — it has changed across releases;
- they filter, they do not group: `sum by (env)` stays the user's job.

- [ ] **Step 5: Commit**

```bash
git add grafana/dashboards docs/getting-started/configuration.md
git commit -m "feat(grafana): add an ad hoc filters variable for custom labels"
```

---

### Task 7: Full gate

- [ ] **Step 1: Run the CI gate**

Run: `make ci`
Expected: golangci-lint clean, `go test -race ./...` green, build succeeds, govulncheck clean.

- [ ] **Step 2: Smoke-test against the mock cluster**

Run: `make demo`, then in another shell:

```bash
curl -s localhost:9438/metrics | grep -c 'env="prod"'
```

after adding a `labels:` block to the demo config. Confirm the count is non-zero and that `ecs_up` carries the label:

```bash
curl -s localhost:9438/metrics | grep '^ecs_up'
```

Expected: `ecs_up{cluster="…",env="prod"} 1`. Tear the stack down afterwards.

- [ ] **Step 3: Commit any fixes**

```bash
make sure
git commit -am "fix: address CI gate findings for custom labels"
```

(Skip if the gate was clean.)

## Self-Review

Checked against `docs/superpowers/specs/2026-08-01-custom-labels-design.md`:

- Configuration schema and all five validation rules → Task 1.
- `${ENV}` interpolation of label values → Task 1, Step 3 (`interpolateLabels`), tested in `TestLoadLabelsUnsetEnvVar` and `TestLoadLabels`.
- Sorted `EffectiveLabels` → Task 1; ordering asserted in `TestLoadLabels` and `TestWithIdentityOrder`.
- Hot reload → Task 4, Step 3: `collectorRunner` already re-runs `buildTargets`, no new code, noted explicitly.
- `WithIdentity`, `WithCluster` retained, collision skip → Task 2.
- Labels on every sample including `ecs_up`/`ecs_collector_up` → Task 3, asserted in `TestCollectStampsCustomLabels`.
- Once-per-key warning with mutex → Task 3, asserted in `TestCollectWarnsOnceOnLabelCollision`.
- Both export paths unchanged but asserted → Task 4.
- Grafana ad hoc variable and its two documented limits → Task 6.
- Documentation list and ADR-0014 → Task 5.
- Out-of-scope items (per-collector labels, OTLP resource attributes, relabeling, probes) appear in no task.

Type consistency: `config.Label{Key, Value}` and `ecs.Label{Key, Value}` are distinct types converted once in `buildTargets`; `EffectiveLabels`, `WithIdentity`, `CollidingLabels`, `hasLabel` and `warnCollision` are spelled identically everywhere they appear.
