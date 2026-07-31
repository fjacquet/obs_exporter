# Flux live-validation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring the shipped Flux collector in line with the live 4.3 capture — fix
what the traces contradict, collect the two families they prove reachable, and
leave behind a demo harness and a tracing route for the September validation
round.

**Architecture:** No new architecture. The Flux collector stays one
`ResourceCollector` (ADR-0009) driven by a table of `fluxQuery` values, and the
work is four extensions to that table's machinery (a per-row staleness guard, a
per-query host-tag column, a bucket-field mode, and a per-query failure split),
one new error type in the shared client, and a fixture/demo/tracing harness
around them.

**Tech Stack:** Go 1.26.4, resty/v2, logrus, prometheus/client_golang,
OpenTelemetry Go SDK, cobra, standard `testing`.

## Global Constraints

Copied from the spec and the repo's standing rules; every task's requirements
implicitly include this section.

- **Absent, never zero, and never stale** (ADR-0007). A missing measurement, an
  unparseable value, an unresolvable host, or a point older than `fluxMaxAge`
  yields *no sample*. Never a zero, never a placeholder.
- **One metric name, one owner, one ordered label-key set** (ADR-0006).
  Ownership is decided in `Registry` (`internal/ecs/resource.go`) before any
  request is issued, never per cycle.
- **`ecs_<object>_<metric>[_<unit>]`**, unit-explicit where the API documents a
  unit. Per-second values are gauges and must never be `rate()`d.
- **One request per measurement per cycle**, closed with `|> last()`, no host
  filter. The external Flux API is a six-operation whitelist —
  `influxDBFrom, filter, range, last, drop, keep` — so no aggregation can be
  pushed server-side.
- **Docs follow the metric, everywhere it is already documented.** A new or
  renamed metric means `docs/metrics/` *and* `grafana/dashboards/`.
- **No inline `nosemgrep` / `//nolint`.** Restructure instead; semgrep blocks on
  findings.
- **Fixtures are mirrored byte-identically** between `internal/ecs/testdata/` and
  `cmd/mockecs/fixtures/`; `TestFixturesMatchMockecs` enforces it and only
  enumerates top-level `.json` files, so fixtures stay flat.
- Gate before every commit: `make sure` (fmt-check, vet, test, build). Gate
  before the final commit of the branch: `make ci`.
- Branch: `feat/flux-live-validation`. Target release: v3.3.0.
- Source capture: `~/Downloads/rapport-flux/traces/` — 135 files, each
  `<name>.json.txt` holding a request preamble, the literal line
  `# === RESPONSE brute ===`, then the JSON body.

---

## File Structure

**Created**

| Path | Responsibility |
| --- | --- |
| `internal/ecsclient/apierror.go` | `APIError`, the decoded ECS error envelope, and the permanent-vs-transient verdict. Nothing else in the client knows the envelope's shape. |
| `internal/ecsclient/apierror_test.go` | Envelope decoding and the `Permanent` verdict, table-driven. |
| `internal/ecs/fluxtime.go` | The staleness guard: `fluxMaxAge`, and parsing a row's `_time`. Separate from `flux.go` because it is the one rule that reasons about time. |
| `internal/ecs/fluxtime_test.go` | Staleness in isolation. |
| `cmd/mockecs/flux.go` | The mock Flux endpoint: measurement-to-fixture routing and `_time` rewriting. Keeps `main.go` a route table. |
| `internal/ecs/testdata/flux_*.json` | Real captures (see Task 2 for the full list). |
| `cmd/mockecs/fixtures/flux_*.json` | Byte-identical mirrors. |
| `main_fluxcapture.go` | The `flux-capture` cobra subcommand. |
| `docs/operate/flux-validation.md` | The September checklist. |

**Modified**

| Path | Change |
| --- | --- |
| `internal/ecsclient/client.go:88-93` | Retry condition consults the decoded body. |
| `internal/ecsclient/client.go:146-148` | Returns `*APIError` instead of a bare `fmt.Errorf`. |
| `internal/ecsclient/client.go:97-108` | Trace hook logs the Flux measurement. |
| `internal/ecs/flux.go` | `hostTag`, bucket mode, staleness, failure split, warn-once, per-measurement debug line, two new queries. |
| `internal/ecs/fluxjson.go` | Nothing structural; a comment correcting the envelope example against the real capture. |
| `internal/ecs/info.go:14-20` | `vdcNodesResp` gains `private_ip` and `data2_ip`. |
| `internal/ecs/nodes.go:88-98` | `FluxOwnsPerf` also suppresses `ecs_node_transaction_latency_milliseconds`. |
| `internal/ecs/resource.go:29-43` | `Flux{DTOwnedByDT: cl.CollectDT}`. |
| `internal/ecs/flux_test.go` | Rewritten expectations against the real fixtures. |
| `cmd/mockecs/main.go` | Registers the Flux route. |
| `docs/metrics/flux.md`, `docs/metrics/index.md` | The new rows and notes. |
| `docs/adr/0011-…md`, `docs/adr/0004-…md` | Live confirmation, the whitelist constraint, the DT narrowing, the body-aware retry. |
| `grafana/dashboards/obs-overview.json` | Latency-quantile and per-node DT panels. |
| `mkdocs.yml`, `CHANGELOG.md` | Nav entry, release notes. |

---

## Task 1: The ECS error envelope and a retry that reads it

**Files:**
- Create: `internal/ecsclient/apierror.go`
- Create: `internal/ecsclient/apierror_test.go`
- Modify: `internal/ecsclient/client.go:85-93`, `internal/ecsclient/client.go:146-148`
- Test: `internal/ecsclient/apierror_test.go`, `internal/ecsclient/client_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `ecsclient.APIError` with exported fields `Method, Path string`,
  `Status, Code int`, `Description string`, `Retryable, Decoded bool`,
  `Body string`; the constant `ecsclient.CodeInsufficientPermissions = 6401`;
  the methods `func (e *APIError) Error() string` and
  `func (e *APIError) Permanent() bool`. Task 4 matches this type with
  `errors.As`.

- [ ] **Step 1: Write the failing envelope tests**

Create `internal/ecsclient/apierror_test.go`:

```go
package ecsclient

import (
	"errors"
	"strings"
	"testing"
)

func TestParseAPIErrorClassifiesRealBodies(t *testing.T) {
	for _, tc := range []struct {
		name          string
		body          string
		status        int
		wantDecoded   bool
		wantPermanent bool
		wantCode      int
	}{
		{
			// The live 4.3 capture: an authenticated account without
			// SYSTEM_MONITOR or SYSTEM_ADMIN is refused with an HTTP 500.
			name:          "permission refusal",
			status:        500,
			body:          `{"code":6401,"description":"Insufficient permissions","retryable":false}`,
			wantDecoded:   true,
			wantPermanent: true,
			wantCode:      6401,
		},
		{
			// The same status code, a query bug rather than a refusal. ECS makes
			// no retryable claim, so we make none either.
			name:          "flux compile error",
			status:        500,
			body:          `{"error":"failed to compile query: undefined identifier mean"}`,
			wantDecoded:   false,
			wantPermanent: false,
		},
		{
			name:          "unsupported media type",
			status:        406,
			body:          `{"error":"unsupported response media type: text/csv"}`,
			wantDecoded:   false,
			wantPermanent: false,
		},
		{
			// An appliance behind a proxy can answer HTML. Nothing is claimed, so
			// the retry policy must fall back to the status class.
			name:          "html body",
			status:        502,
			body:          "<html><body>Bad Gateway</body></html>",
			wantDecoded:   false,
			wantPermanent: false,
		},
		{
			// Decoded, but ECS says the failure is transient: retry it.
			name:          "retryable envelope",
			status:        503,
			body:          `{"code":1000,"description":"Service busy","retryable":true}`,
			wantDecoded:   true,
			wantPermanent: false,
			wantCode:      1000,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := parseAPIError("POST", "/flux/api/external/v2/query", tc.status, []byte(tc.body))
			if e.Decoded != tc.wantDecoded {
				t.Errorf("Decoded = %v, want %v", e.Decoded, tc.wantDecoded)
			}
			if e.Permanent() != tc.wantPermanent {
				t.Errorf("Permanent() = %v, want %v", e.Permanent(), tc.wantPermanent)
			}
			if e.Code != tc.wantCode {
				t.Errorf("Code = %d, want %d", e.Code, tc.wantCode)
			}
			if e.Status != tc.status {
				t.Errorf("Status = %d, want %d", e.Status, tc.status)
			}
		})
	}
}

func TestAPIErrorMessageCarriesTheCause(t *testing.T) {
	// "status 500" told an operator nothing. The description is the whole point.
	e := parseAPIError("POST", "/flux/api/external/v2/query", 500,
		[]byte(`{"code":6401,"description":"Insufficient permissions","retryable":false}`))
	msg := e.Error()
	for _, want := range []string{"500", "Insufficient permissions", "6401"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error() = %q, missing %q", msg, want)
		}
	}
}

func TestAPIErrorIsMatchableWithErrorsAs(t *testing.T) {
	// The Flux collector separates a global refusal from a per-query failure by
	// unwrapping this type; if it stops matching, that split silently inverts.
	var wrapped error = parseAPIError("POST", "/x", 500, []byte(`{"retryable":false}`))
	wrapped = errors.Join(errors.New("flux monitoring_op/cpu"), wrapped)
	var api *APIError
	if !errors.As(wrapped, &api) {
		t.Fatal("errors.As did not match *APIError through a wrap")
	}
	if !api.Permanent() {
		t.Error("unwrapped error lost its permanent verdict")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/ecsclient/ -run TestParseAPIError -v`
Expected: FAIL — `undefined: parseAPIError`.

- [ ] **Step 3: Write the implementation**

Create `internal/ecsclient/apierror.go`:

```go
package ecsclient

import (
	"encoding/json"
	"fmt"
)

// CodeInsufficientPermissions is the ECS error code for an account that
// authenticated but whose roles do not cover the endpoint. It arrives with an
// HTTP 500, not a 403.
const CodeInsufficientPermissions = 6401

// APIError is a non-2xx management API response, decoded far enough to tell a
// permanent refusal from a transient failure.
//
// ObjectScale overloads HTTP 500. A permission refusal carries
// {"code":6401,"description":"Insufficient permissions","retryable":false} and
// will never succeed; an invalid Flux query carries {"error":"failed to compile
// query: …"}; and a genuinely overloaded appliance carries whatever its proxy
// felt like. The status code alone separates none of them, so the body decides.
type APIError struct {
	Method      string
	Path        string
	Status      int
	Code        int
	Description string
	// Retryable is ECS's own claim. It is meaningful only when Decoded is true.
	Retryable bool
	// Decoded reports whether the body carried the structured ECS error
	// envelope. A body we could not read makes no claim, so it must not be
	// mistaken for a claim of "do not retry".
	Decoded bool
	Body    string
}

// errorEnvelope is the ECS error body. Retryable is a pointer so an omitted
// flag stays distinguishable from an explicit false.
type errorEnvelope struct {
	Code        int    `json:"code"`
	Description string `json:"description"`
	Retryable   *bool  `json:"retryable"`
}

// parseAPIError builds an APIError from a response, decoding the body when it
// carries the ECS envelope and leaving Decoded false when it does not.
func parseAPIError(method, path string, status int, body []byte) *APIError {
	e := &APIError{Method: method, Path: path, Status: status, Body: string(body)}
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return e
	}
	if env.Code == 0 && env.Description == "" && env.Retryable == nil {
		return e // valid JSON, but not this envelope (e.g. {"error":"…"})
	}
	e.Decoded = true
	e.Code = env.Code
	e.Description = env.Description
	// An envelope that omits the flag makes no claim; treat it as retryable and
	// let the status class decide, exactly as before this type existed.
	e.Retryable = env.Retryable == nil || *env.Retryable
	return e
}

// Error renders the cause, not just the status. The raw body is truncated: a
// proxy's HTML error page must not fill the log.
func (e *APIError) Error() string {
	if e.Decoded {
		return fmt.Sprintf("%s %s: status %d: %s (code %d, retryable %t)",
			e.Method, e.Path, e.Status, e.Description, e.Code, e.Retryable)
	}
	body := e.Body
	if len(body) > 256 {
		body = body[:256] + "…"
	}
	if body == "" {
		return fmt.Sprintf("%s %s: status %d", e.Method, e.Path, e.Status)
	}
	return fmt.Sprintf("%s %s: status %d: %s", e.Method, e.Path, e.Status, body)
}

// Permanent reports whether retrying this request could ever succeed. Only a
// decoded envelope can say so — either by setting retryable to false or by
// carrying the permission code, which no amount of retrying will change.
func (e *APIError) Permanent() bool {
	if !e.Decoded {
		return false
	}
	return !e.Retryable || e.Code == CodeInsufficientPermissions
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/ecsclient/ -run 'TestParseAPIError|TestAPIError' -v`
Expected: PASS.

- [ ] **Step 5: Write the failing transport tests**

Append to `internal/ecsclient/client_test.go`:

```go
func TestClientDoesNotRetryPermanentRefusal(t *testing.T) {
	// A SECURITY_ADMIN-only account is refused with an HTTP 500. The transport
	// retries 5xx twice, so without reading the body this costs three requests
	// per measurement per cycle, forever, for an outcome that cannot change.
	var calls int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.Header().Set("X-SDS-AUTH-TOKEN", "tok")
			return
		}
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":6401,"description":"Insufficient permissions","retryable":false}`))
	}))
	defer srv.Close()

	c := NewClusterClient(Config{Name: "t", BaseURL: srv.URL, HTTPClient: srv.Client()})
	var out map[string]any
	err := c.Post(t.Context(), "/flux/api/external/v2/query", map[string]string{"query": "x"}, &out)
	if err == nil {
		t.Fatal("Post must return an error on a permission refusal")
	}
	var api *APIError
	if !errors.As(err, &api) || !api.Permanent() {
		t.Fatalf("err = %v, want a permanent *APIError", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("issued %d requests, want 1: a permanent refusal must not be retried", got)
	}
}

func TestClientStillRetriesUnclaimedServerErrors(t *testing.T) {
	// A 5xx that makes no retryable claim keeps the old behaviour: two retries.
	var calls int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.Header().Set("X-SDS-AUTH-TOKEN", "tok")
			return
		}
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>Bad Gateway</html>"))
	}))
	defer srv.Close()

	c := NewClusterClient(Config{Name: "t", BaseURL: srv.URL, HTTPClient: srv.Client()})
	var out map[string]any
	if err := c.Get(t.Context(), "/dashboard/zones/localzone", &out); err == nil {
		t.Fatal("Get must return an error on a 502")
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("issued %d requests, want 3 (initial + 2 retries)", got)
	}
}
```

Add `"errors"`, `"sync/atomic"` to that file's imports if absent.

- [ ] **Step 6: Run to verify they fail**

Run: `go test ./internal/ecsclient/ -run 'TestClientDoesNotRetry|TestClientStillRetries' -v`
Expected: FAIL — the refusal test reports 3 requests, and `errors.As` finds no
`*APIError`.

- [ ] **Step 7: Wire the client to the new type**

In `internal/ecsclient/client.go`, replace the retry condition (currently lines
85-93):

```go
	// Retry on transport errors and 5xx, but never on 4xx (do not retry
	// auth/permission failures), and never on a 5xx whose body says retrying
	// cannot help. ObjectScale answers a permission refusal with HTTP 500 and
	// {"code":6401,…,"retryable":false} (ADR-0004), so the status class alone
	// would loop on an outcome that can never change. resty passes r == nil on
	// transport/TLS errors, so guard the dereference to avoid a panic.
	rc.SetRetryCount(2).AddRetryCondition(func(r *resty.Response, err error) bool {
		if err != nil {
			return true
		}
		if r == nil || r.StatusCode() < 500 {
			return false
		}
		return !parseAPIError(r.Request.Method, r.Request.URL, r.StatusCode(), r.Body()).Permanent()
	})
```

and replace the error return in `call` (currently lines 146-148):

```go
	if resp.StatusCode() >= 300 {
		return parseAPIError(method, path, resp.StatusCode(), resp.Body())
	}
```

- [ ] **Step 8: Run the client suite**

Run: `go test ./internal/ecsclient/ -v`
Expected: PASS, including the two new tests and every pre-existing one.

- [ ] **Step 9: Commit**

```bash
make sure
git add internal/ecsclient/
git commit -m "fix(client): read the ECS error body instead of retrying a refusal

ObjectScale answers a permission refusal with HTTP 500 and
{code:6401,description:Insufficient permissions,retryable:false}, and an
invalid Flux query with a 500 too. The retry condition saw only the status
class, so an under-privileged account cost three requests per measurement per
cycle for an outcome that could never change, reported as \"status 500\".

APIError decodes the envelope, Permanent() honours ECS's own retryable claim,
and a body that decodes to nothing makes no claim -- so an HTML 502 still
retries exactly as before.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Real fixtures from the live capture

**Files:**
- Create: `internal/ecs/testdata/flux_mem.json`, `flux_dt_dist.json`,
  `flux_throughput.json`, `flux_latency.json`, `flux_empty.json`,
  `flux_cq_transaction.json`, `flux_cq_throughput.json`
- Modify: `internal/ecs/testdata/flux_cpu.json`, `flux_net.json`,
  `flux_dt_status.json`, `flux_transactions.json` (replaced by real captures)
- Modify: `internal/ecs/testdata/README.md`
- Create: the byte-identical mirrors under `cmd/mockecs/fixtures/`
- Modify: `internal/ecs/flux_test.go` (expectations follow the new values)
- Test: `go test ./internal/ecs/`

**Interfaces:**
- Consumes: nothing.
- Produces: fixture files named `flux_<measurement>.json`, each holding the
  verbatim `{"Series":[…]}` body of a real 4.3 response with host identifiers
  remapped. Tasks 3, 5, 6 and 8 read them by name.

**Why identifiers are remapped.** The captures name nodes `node-1…node-5` at
`192.168.2.x`; the repo's demo inventory (`vdc-nodes.json`, `nodes.json`,
`localzone.json`) names them `supr01-r01`/`supr01-r02` at `10.0.0.x`/`10.1.0.x`.
Serving both in `make demo` would produce two disjoint node sets and no panel
would join. Benjamin's hostnames are already pseudonyms — nothing real is lost by
mapping `node-1 → supr01-r01`, `node-2 → supr01-r02`, `192.168.2.1 → 10.1.0.1`,
`192.168.2.2 → 10.1.0.2`, and dropping rows for nodes 3-5 so the fixture matches
the two-node inventory. Every column, datatype, timestamp and value stays
verbatim. `flux_net.json` additionally keeps one row under
`not-in-this-cluster.example.com`, as the current hand-written fixture does, so
`TestFluxCountsUnmappedHosts` still has an unmappable host to count.

- [ ] **Step 1: Extract and remap the captures**

Run this from the repo root. It reads the capture, keeps two nodes, remaps
identifiers, and writes formatted JSON:

```bash
python3 - <<'PY'
import json, os, re

SRC = os.path.expanduser("~/Downloads/rapport-flux/traces")
DST = "internal/ecs/testdata"

HOSTS = {"node-1": "supr01-r01", "node-2": "supr01-r02"}
IPS   = {"192.168.2.1": "10.1.0.1", "192.168.2.2": "10.1.0.2"}
KEEP  = set(HOSTS) | set(IPS)

def body(name):
    raw = open(os.path.join(SRC, name + ".json.txt")).read()
    return json.loads(raw[raw.index("# === RESPONSE brute ===") + 24:].strip())

def remap(d, keycol):
    """Keep only rows whose keycol is one of the two nodes, then rename."""
    for s in d.get("Series") or []:
        cols = s.get("Columns")
        if not cols:
            continue
        if keycol and keycol in cols:
            i = cols.index(keycol)
            s["Values"] = [v for v in s["Values"] if v[i] in KEEP]
        for row in s.get("Values") or []:
            for j, cell in enumerate(row):
                if cell in HOSTS:
                    row[j] = HOSTS[cell]
                elif cell in IPS:
                    row[j] = IPS[cell]
    return d

PLAN = [
    ("01-cpu-usage_user",                        "flux_cpu.json",            "host"),
    ("02-mem-all",                               "flux_mem.json",            "host"),
    ("02-net-all",                               "flux_net.json",            "host"),
    ("04-dtquery-status",                        "flux_dt_status.json",      None),
    ("04-dtquery-dist",                          "flux_dt_dist.json",        "dt_node_id"),
    ("05-transactions",                          "flux_transactions.json",   "host"),
    ("05-throughput",                            "flux_throughput.json",     "host"),
    ("05-latency",                               "flux_latency.json",        "host"),
    ("06-nonexistent",                           "flux_empty.json",          None),
]

for src, dst, keycol in PLAN:
    d = remap(body(src), keycol)
    with open(os.path.join(DST, dst), "w") as f:
        json.dump(d, f, indent=2)
        f.write("\n")
    rows = sum(len(s.get("Values") or []) for s in d.get("Series") or [])
    print(f"{dst}: {rows} rows")
PY
```

- [ ] **Step 2: Add the unmappable-host row to `flux_net.json`**

`TestFluxCountsUnmappedHosts` needs a row no inventory node claims. Append one
`bytes_recv` row to the first Series' `Values`, copying an existing row and
replacing its `host` cell with `not-in-this-cluster.example.com`:

```bash
python3 - <<'PY'
import json
p = "internal/ecs/testdata/flux_net.json"
d = json.load(open(p))
s = d["Series"][0]
i = s["Columns"].index("host")
f = s["Columns"].index("_field")
tmpl = next(r for r in s["Values"] if r[f] == "bytes_recv")
row = list(tmpl)
row[i] = "not-in-this-cluster.example.com"
s["Values"].append(row)
json.dump(d, open(p, "w"), indent=2); open(p, "a").write("\n")
print("appended unmappable host row")
PY
```

- [ ] **Step 3: Hand-write the two unattached `monitoring_vdc` fixtures**

The reporter confirmed `cq_performance_transaction` and
`cq_performance_throughput` emit, but attached no payload for either. These are
written from the envelope shape the other `monitoring_vdc` captures establish
(no `host`, no tags, `Datatypes` present, all values strings), and they say so.

Create `internal/ecs/testdata/flux_cq_transaction.json`:

```json
{
  "_comment": "SYNTHESIZED, not a live capture. The reporter confirmed in prose that cq_performance_transaction emits with these fields on 4.3, but attached no payload. Shape copied from the real x-vdc-cq_performance_error capture: no host, no tags, every value a string. Replace with a real capture when one arrives (see docs/operate/flux-validation.md).",
  "Series": [
    {
      "Datatypes": ["long", "dateTime:RFC3339", "dateTime:RFC3339", "dateTime:RFC3339", "double", "string", "string"],
      "Columns": ["table", "_start", "_stop", "_time", "_value", "_field", "_measurement"],
      "Values": [
        ["0", "2026-07-31T08:08:25Z", "2026-07-31T08:38:25Z", "2026-07-31T08:35:00Z", "142.5", "succeed_request_counter", "cq_performance_transaction"],
        ["1", "2026-07-31T08:08:25Z", "2026-07-31T08:38:25Z", "2026-07-31T08:35:00Z", "0.75", "failed_request_counter", "cq_performance_transaction"]
      ]
    }
  ]
}
```

Create `internal/ecs/testdata/flux_cq_throughput.json` with the same header
comment (naming `cq_performance_throughput`) and two rows for
`total_read_requests_size` = `"41943040"` and `total_write_requests_size` =
`"20971520"`.

`_comment` is ignored by `fluxResp`, which decodes only `Series`.

- [ ] **Step 4: Mirror every fixture into mockecs and record the provenance**

```bash
cp internal/ecs/testdata/flux_*.json cmd/mockecs/fixtures/
```

Append to `internal/ecs/testdata/README.md`:

```markdown
## Flux fixtures (`flux_*.json`)

Real `POST /flux/api/external/v2/query` responses captured on an ObjectScale
4.3.0.0.142978 acceptance cluster on 2026-07-31, verbatim except for host
identifiers: the capture's already-pseudonymous `node-1`/`node-2` and
`192.168.2.1`/`192.168.2.2` are remapped onto this repo's demo inventory
(`supr01-r01`/`supr01-r02`, `10.1.0.1`/`10.1.0.2`) and rows for the capture's
other three nodes are dropped, so `make demo` shows one coherent cluster rather
than two disjoint node sets. `flux_net.json` carries one extra row under
`not-in-this-cluster.example.com` so the unmapped-host counter has something to
count.

`flux_cq_transaction.json` and `flux_cq_throughput.json` are **synthesized**:
their measurements are confirmed in prose with no attached payload. Each says so
in a `_comment` key. Replace them when a real capture arrives.

`flux_empty.json` is the live answer to a measurement the store does not carry —
HTTP 200 with `Series:[{Datatypes:null,Columns:null,Values:null}]`.
```

- [ ] **Step 5: Point the existing tests at the new values**

Run the suite to see what moved:

Run: `go test ./internal/ecs/ -run TestFlux -v`
Expected: FAIL — `TestFluxCollectPerNodeGauges` still asserts 31.5/12.25,
`TestFluxCollectClusterScopedDT` still asserts 128/2/1, and the network counters
assert the hand-written byte totals.

Read the fixtures for the real values and update every assertion in
`internal/ecs/flux_test.go` to match, keeping the tests' intent and comments
unchanged. `flux_net.json` uses real interface names from the capture, so the
`interface` label values change too.

- [ ] **Step 6: Run the whole package**

Run: `go test ./internal/ecs/ -v`
Expected: PASS, including `TestFixturesMatchMockecs` (it must report a higher
`compared` count than before).

- [ ] **Step 7: Commit**

```bash
make sure
git add internal/ecs/testdata/ cmd/mockecs/fixtures/ internal/ecs/flux_test.go
git commit -m "test(flux): replace transcribed fixtures with the live 4.3 capture

Every Flux fixture was transcribed from the 4.3 admin guide's worked example.
These are real POST /flux/api/external/v2/query responses from a 4.3.0.0.142978
cluster -- with _time, Datatypes, the full field set per measurement, and the
column order the store actually emits.

Host identifiers are remapped onto the repo's demo inventory: the capture's
node-1/node-2 are themselves pseudonyms, and two disjoint node sets would make
every joined panel in make demo empty.

cq_performance_transaction and cq_performance_throughput are confirmed in prose
with no payload attached; their fixtures are hand-written and say so.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Absent, never stale

**Files:**
- Create: `internal/ecs/fluxtime.go`
- Create: `internal/ecs/fluxtime_test.go`
- Modify: `internal/ecs/flux.go` (`samples` takes a `now`, drops stale rows and
  counts them; `Collect` passes `time.Now()`)
- Test: `internal/ecs/fluxtime_test.go`, `internal/ecs/flux_test.go`

**Interfaces:**
- Consumes: the fixtures from Task 2 (their `_time` values are fixed instants in
  2026-07-31T08:3x).
- Produces: `const fluxMaxAge = 10 * time.Minute`; `fluxQuery.maxAge
  time.Duration` (zero means `fluxMaxAge`); `func (r fluxRow) age(now time.Time)
  (time.Duration, bool)`; the signature
  `func (q fluxQuery) samples(rows []fluxRow, mapper *nodeMapper, now time.Time)
  (out []Sample, unmapped, stale float64)`. Tasks 5 and 6 call `samples` with
  this signature.

- [ ] **Step 1: Write the failing staleness tests**

Create `internal/ecs/fluxtime_test.go`:

```go
package ecs

import (
	"testing"
	"time"
)

func rowWithTime(ts string) fluxRow {
	cols := map[string]string{"_field": "usage_user", "_value": "1"}
	if ts != "" {
		cols["_time"] = ts
	}
	return fluxRow{cols: cols}
}

func TestRowAge(t *testing.T) {
	now := time.Date(2026, 7, 31, 8, 40, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		ts      string
		wantAge time.Duration
		wantOK  bool
	}{
		{"fresh", "2026-07-31T08:35:09Z", 4*time.Minute + 51*time.Second, true},
		{"fractional seconds", "2026-07-31T08:35:09.481Z", 4*time.Minute + 50*time.Second + 519*time.Millisecond, true},
		{"clock skew puts the point ahead", "2026-07-31T08:41:00Z", -time.Minute, true},
		{"missing column", "", 0, false},
		{"unparseable", "not-a-timestamp", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rowWithTime(tc.ts).age(now)
			if ok != tc.wantOK {
				t.Fatalf("age() ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.wantAge {
				t.Errorf("age() = %v, want %v", got, tc.wantAge)
			}
		})
	}
}

func TestStaleRowYieldsNoSample(t *testing.T) {
	// last() returns the newest point in the window whatever its age, and these
	// samples carry no timestamp, so Prometheus stamps them at scrape time. A
	// node that stopped emitting eleven minutes ago must go absent, not look
	// current. This is ADR-0007 along the time axis.
	q := fluxQuery{
		bucket: "monitoring_op", measurement: "cpu",
		fields: []fluxField{{field: "usage_user", name: "ecs_node_cpu_utilization_percent"}},
	}
	now := time.Date(2026, 7, 31, 8, 40, 0, 0, time.UTC)
	rows := []fluxRow{{cols: map[string]string{
		"_field": "usage_user", "_value": "5.1", "_time": "2026-07-31T08:28:00Z",
	}}}
	out, _, stale := q.samples(rows, nil, now)
	if len(out) != 0 {
		t.Errorf("a 12-minute-old point produced %d samples, want none", len(out))
	}
	if stale != 1 {
		t.Errorf("stale = %v, want 1", stale)
	}
}

func TestFreshRowIsKept(t *testing.T) {
	q := fluxQuery{
		bucket: "monitoring_op", measurement: "cpu",
		fields: []fluxField{{field: "usage_user", name: "ecs_node_cpu_utilization_percent"}},
	}
	now := time.Date(2026, 7, 31, 8, 40, 0, 0, time.UTC)
	rows := []fluxRow{{cols: map[string]string{
		"_field": "usage_user", "_value": "5.1", "_time": "2026-07-31T08:36:00Z",
	}}}
	out, _, stale := q.samples(rows, nil, now)
	if len(out) != 1 || out[0].Value != 5.1 {
		t.Fatalf("samples = %v, want one sample valued 5.1", out)
	}
	if stale != 0 {
		t.Errorf("stale = %v, want 0", stale)
	}
}

func TestRowWithoutTimeIsDropped(t *testing.T) {
	// A row we cannot date cannot be shown to be current, and an undated value
	// published as a live gauge is indistinguishable from a fresh one.
	q := fluxQuery{
		bucket: "monitoring_op", measurement: "cpu",
		fields: []fluxField{{field: "usage_user", name: "ecs_node_cpu_utilization_percent"}},
	}
	rows := []fluxRow{{cols: map[string]string{"_field": "usage_user", "_value": "5.1"}}}
	out, _, stale := q.samples(rows, nil, time.Now())
	if len(out) != 0 {
		t.Errorf("an undated row produced %d samples, want none", len(out))
	}
	if stale != 1 {
		t.Errorf("stale = %v, want 1", stale)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/ecs/ -run 'TestRowAge|TestStaleRow|TestFreshRow|TestRowWithoutTime' -v`
Expected: FAIL — `r.age` undefined and `samples` takes two arguments.

- [ ] **Step 3: Write the staleness guard**

Create `internal/ecs/fluxtime.go`:

```go
package ecs

import "time"

// fluxMaxAge is how old a point may be and still be published.
//
// Every measurement this collector queries writes points five minutes apart on
// a live 4.3, so twice that leaves one missed write of slack before a series
// goes absent. It must stay well under fluxRange: last() returns the newest
// point in the window whatever its age, and these samples carry no timestamp of
// their own, so Prometheus stamps them at scrape time. Without this guard a
// node that stopped emitting keeps a value that looks current for the full
// width of the window.
//
// A measurement in one of the store's slower cadence classes (10-25 minutes, or
// sparse) would need its own value — hence fluxQuery.maxAge.
const fluxMaxAge = 10 * time.Minute

// age returns how far in the past this row's point was written, and whether the
// row could be dated at all. A row with no _time, or one we cannot parse, is
// undatable: it cannot be shown to be current, so it is not published.
func (r fluxRow) age(now time.Time) (time.Duration, bool) {
	ts, ok := r.value("_time")
	if !ok {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0, false
	}
	return now.Sub(t), true
}

// maxAgeOrDefault is this query's staleness threshold.
func (q fluxQuery) maxAgeOrDefault() time.Duration {
	if q.maxAge > 0 {
		return q.maxAge
	}
	return fluxMaxAge
}
```

In `internal/ecs/flux.go`, add the field to `fluxQuery`:

```go
	// maxAge overrides fluxMaxAge for a measurement outside the five-minute
	// cadence class. Zero means the default.
	maxAge time.Duration
```

Change `samples` to take `now` and return the stale count, dropping stale rows
before anything else is decided about them:

```go
// samples maps one measurement's rows, returning the samples, how many rows were
// dropped for an unresolvable host, and how many were dropped as stale.
func (q fluxQuery) samples(rows []fluxRow, mapper *nodeMapper, now time.Time) ([]Sample, float64, float64) {
	var out []Sample
	var unmapped, stale float64
	limit := q.maxAgeOrDefault()
	for _, row := range rows {
		age, dated := row.age(now)
		if !dated || age > limit {
			stale++
			continue
		}
		field, ok := row.value("_field")
		// … the rest of the existing body, unchanged …
	}
	return out, unmapped, stale
}
```

In `Collect`, take `now` once per cycle so every measurement is judged against
one instant, and thread the new return value:

```go
	now := time.Now()
	…
		samples, miss, stale := q.samples(rows, mapper, now)
		out = append(out, samples...)
		unmapped += miss
		_ = stale // reported by the per-measurement debug line in Task 9
```

Add `"time"` to the file's imports.

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/ecs/ -run 'TestRowAge|TestStaleRow|TestFreshRow|TestRowWithoutTime' -v`
Expected: PASS.

- [ ] **Step 5: Pin the Flux tests to the capture's instant**

Every fixture from Task 2 is dated 2026-07-31T08:3x, so `time.Now()` makes all
of them stale and every Flux collector test now fails. Give the tests a fixed
clock rather than rewriting the fixtures' timestamps — the timestamps are part
of what makes them real.

In `internal/ecs/flux.go`, split the clock out of `Collect`:

```go
// Flux is the opt-in collector … (existing doc comment unchanged)
type Flux struct {
	// now overrides the clock. Zero means time.Now; tests set it so fixtures
	// captured at a fixed instant are not all judged stale.
	now func() time.Time
}

func (f Flux) clock() time.Time {
	if f.now == nil {
		return time.Now()
	}
	return f.now()
}
```

Note `Flux` is now a struct with a field, so `Flux{}` literals keep compiling and
`Registry` is unaffected. In `internal/ecs/flux_test.go`, give `collectFlux` the
capture's instant:

```go
// captureInstant is a moment shortly after every flux_*.json fixture was
// written on the live cluster, so fixture rows read as fresh (see fluxMaxAge).
var captureInstant = time.Date(2026, 7, 31, 8, 38, 30, 0, time.UTC)

func collectFlux(t *testing.T, byMeasurement map[string]string) []Sample {
	t.Helper()
	f := Flux{now: func() time.Time { return captureInstant }}
	samples, err := f.Collect(t.Context(), fluxMock(t, byMeasurement))
	if err != nil {
		t.Fatal(err)
	}
	return samples
}
```

Update the other direct `Flux{}.Collect` call sites in that file the same way,
and add `"time"` to its imports.

- [ ] **Step 6: Add the end-to-end staleness test**

Append to `internal/ecs/flux_test.go`:

```go
func TestFluxDropsStaleFixtureRows(t *testing.T) {
	// The same fixture, read an hour later: every row is older than fluxMaxAge,
	// so the collector must publish nothing rather than an hour-old CPU reading
	// that Prometheus will stamp as current.
	f := Flux{now: func() time.Time { return captureInstant.Add(time.Hour) }}
	samples, err := f.Collect(t.Context(), fluxMock(t, map[string]string{"cpu": "flux_cpu.json"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findSample(samples, "ecs_node_cpu_utilization_percent"); ok {
		t.Error("an hour-old point was published as a live gauge")
	}
}
```

- [ ] **Step 7: Run the package**

Run: `go test ./internal/ecs/ -v`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
make sure
git add internal/ecs/
git commit -m "feat(flux): drop points older than fluxMaxAge

last() returns the newest point in the window whatever its age, and these
samples carry no timestamp, so Prometheus stamps them at scrape time. A node
that stopped emitting kept a value that looked current for the full fifteen
minutes of the window.

Rows are now dated from _time and dropped past ten minutes -- twice the
five-minute cadence the live cluster writes at, leaving one missed write of
slack. A row we cannot date is dropped too: it cannot be shown to be current.

Absent-never-zero, along the time axis.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Global failure versus one bad measurement

**Files:**
- Modify: `internal/ecs/flux.go` (`Collect`)
- Test: `internal/ecs/flux_test.go`

**Interfaces:**
- Consumes: `ecsclient.APIError` and `Permanent()` from Task 1.
- Produces: `func fluxFatal(err error) bool` in `flux.go`; `Collect` returns an
  error only for a fatal cause or when every query failed.

- [ ] **Step 1: Write the failing tests**

Append to `internal/ecs/flux_test.go`:

```go
// erroringFluxClient fails the named measurements and serves fixtures for the
// rest, so a partial failure can be told from a total one.
type erroringFluxClient struct {
	*fluxClient
	errByMeasurement map[string]error
}

func (e *erroringFluxClient) Post(ctx context.Context, path string, body, out any) error {
	q, _ := body.(map[string]string)
	for measurement, err := range e.errByMeasurement {
		if strings.Contains(q["query"], `"`+measurement+`"`) {
			return err
		}
	}
	return e.fluxClient.Post(ctx, path, body, out)
}

func permissionRefusal() error {
	return &ecsclient.APIError{
		Method: "POST", Path: fluxPath, Status: 500,
		Code: ecsclient.CodeInsufficientPermissions,
		Description: "Insufficient permissions", Decoded: true,
	}
}

func TestFluxPermissionRefusalFailsTheWholeCollector(t *testing.T) {
	// Nothing this collector asks for will work; failing fast is both correct
	// and the difference between one request per cycle and ten.
	c := &erroringFluxClient{
		fluxClient:       &fluxClient{Client: mockClient(t), bodies: map[string]string{"cpu": "flux_cpu.json"}, t: t},
		errByMeasurement: map[string]error{"cpu": permissionRefusal()},
	}
	f := Flux{now: func() time.Time { return captureInstant }}
	if _, err := f.Collect(t.Context(), c); err == nil {
		t.Fatal("a permission refusal must fail the collector")
	}
}

func TestFluxOneBadQueryLeavesTheOthersStanding(t *testing.T) {
	// A compile error is scoped to one query. Taking the other nine down with it
	// costs the operator every metric this collector exists to provide.
	c := &erroringFluxClient{
		fluxClient: &fluxClient{Client: mockClient(t), bodies: map[string]string{
			"cpu": "flux_cpu.json",
			"mem": "flux_mem.json",
		}, t: t},
		errByMeasurement: map[string]error{
			"mem": &ecsclient.APIError{Method: "POST", Path: fluxPath, Status: 500,
				Body: `{"error":"failed to compile query: undefined identifier"}`},
		},
	}
	f := Flux{now: func() time.Time { return captureInstant }}
	samples, err := f.Collect(t.Context(), c)
	if err != nil {
		t.Fatalf("one failing query must not fail the collector: %v", err)
	}
	if _, ok := findSample(samples, "ecs_node_cpu_utilization_percent"); !ok {
		t.Error("cpu samples were lost to an unrelated query's failure")
	}
	if _, ok := findSample(samples, "ecs_node_memory_utilization_percent"); ok {
		t.Error("the failed measurement emitted samples")
	}
}

func TestFluxFailsWhenEveryQueryFails(t *testing.T) {
	// Tolerating per-query failures must not turn a completely broken collector
	// into a healthy one.
	c := &erroringFluxClient{
		fluxClient:       &fluxClient{Client: mockClient(t), bodies: nil, t: t},
		errByMeasurement: nil,
	}
	c.fluxClient.postErr = errors.New("500 Internal Server Error")
	f := Flux{now: func() time.Time { return captureInstant }}
	if _, err := f.Collect(t.Context(), c); err == nil {
		t.Fatal("Collect must fail when no query succeeded")
	}
}
```

Replace `fluxClient`'s existing `fail bool` field with `postErr error` and its
guard with `if f.postErr != nil { return f.postErr }`, updating
`TestFluxCollectFailsOnEndpointError` to set
`postErr: errors.New("401 Unauthorized")`. Add `"github.com/fjacquet/obs_exporter/internal/ecsclient"`
to the test file's imports if absent.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/ecs/ -run 'TestFluxPermission|TestFluxOneBad|TestFluxFailsWhenEvery' -v`
Expected: FAIL — every query error currently aborts `Collect`, so
`TestFluxOneBadQueryLeavesTheOthersStanding` reports an error.

- [ ] **Step 3: Implement the split**

In `internal/ecs/flux.go`, add:

```go
// fluxFatal reports whether an error condemns the whole collector rather than
// one measurement.
//
// Anything that is not a per-request API error — a transport failure, a login
// failure, a cancelled context — is global by construction: it says nothing
// about the query and everything about the connection. An API error is global
// only when ECS itself says retrying can never help, which is how a permission
// refusal (HTTP 500, code 6401) is told from a query bug (HTTP 500, a compile
// error) that leaves the other measurements perfectly collectable.
func fluxFatal(err error) bool {
	var api *ecsclient.APIError
	if !errors.As(err, &api) {
		return true
	}
	return api.Permanent()
}
```

and rewrite the loop body in `Collect`:

```go
	var out []Sample
	var unmapped float64
	var attempted, succeeded int
	for _, q := range fluxQueries {
		attempted++
		var resp fluxResp
		if err := c.Post(ctx, fluxPath, map[string]string{"query": q.script()}, &resp); err != nil {
			wrapped := fmt.Errorf("flux %s/%s: %w", q.bucket, q.measurement, err)
			if fluxFatal(err) {
				return nil, wrapped
			}
			log.WithFields(log.Fields{
				"cluster": c.Name(), "bucket": q.bucket, "measurement": q.measurement, "err": err,
			}).Warn("Flux query failed; its samples are absent this cycle")
			continue
		}
		succeeded++
		…
	}
	if attempted > 0 && succeeded == 0 {
		return nil, fmt.Errorf("flux: all %d queries failed", attempted)
	}
```

Add `"errors"` to the imports.

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/ecs/ -run TestFlux -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
make sure
git add internal/ecs/
git commit -m "fix(flux): stop one bad query taking the other nine down

Collect aborted on any query error, so a single renamed measurement or a
single malformed query cost the operator every metric this collector exists to
provide -- while the code already tolerated an *empty* result per measurement
for exactly that reason.

Errors are now split by blast radius. A permission refusal, a login failure or
a transport failure condemns everything and returns at once, without issuing
the remaining queries. A query-scoped 500 is logged, its measurement skipped,
and the rest attempted. The collector still fails when no query succeeded.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Per-node directory tables

**Files:**
- Modify: `internal/ecs/flux.go` (`hostTag`, the new query, `samples`)
- Modify: `internal/ecs/info.go:14-20` (`vdcNodesResp`)
- Modify: `internal/ecs/resource.go:29-43` (arbitration)
- Test: `internal/ecs/flux_test.go`, `internal/ecs/collector_test.go`

**Interfaces:**
- Consumes: `flux_dt_dist.json` (Task 2), `samples(rows, mapper, now)` (Task 3).
- Produces: `fluxQuery.hostTag string` (empty means `"host"`);
  `Flux{DTOwnedByDT bool}`; the metric `ecs_node_dt_total{node}` when
  `collectDT` is off.

- [ ] **Step 1: Write the failing tests**

Append to `internal/ecs/flux_test.go`:

```go
func TestFluxCollectsPerNodeDT(t *testing.T) {
	// dtquery_dt_dist_host_dt_node_id has no host tag, which is why ADR-0011
	// concluded Flux could not report DT per node. It identifies the node under
	// dt_node_id instead, holding the data_ip the inventory already indexes.
	f := Flux{now: func() time.Time { return captureInstant }}
	samples, err := f.Collect(t.Context(), fluxMock(t, map[string]string{
		"dtquery_dt_dist_host_dt_node_id": "flux_dt_dist.json",
	}))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := findSample(samples, "ecs_node_dt_total", Label{"node", "supr01-r01"})
	if !ok {
		t.Fatal("no per-node DT sample for supr01-r01")
	}
	if s.Value <= 0 {
		t.Errorf("ecs_node_dt_total = %v, want the capture's count", s.Value)
	}
	if _, ok := findSample(samples, "ecs_node_dt_total", Label{"node", "supr01-r02"}); !ok {
		t.Error("no per-node DT sample for supr01-r02")
	}
}

func TestFluxSkipsPerNodeDTWhenDTCollectorOwnsIt(t *testing.T) {
	// collectDT serves unready and unknown per node as well, so where it is
	// reachable it keeps the name and Flux must not issue the query at all.
	c := mockClient(t)
	fc := &fluxClient{Client: c, bodies: map[string]string{
		"dtquery_dt_dist_host_dt_node_id": "flux_dt_dist.json",
	}, t: t}
	f := Flux{DTOwnedByDT: true, now: func() time.Time { return captureInstant }}
	samples, err := f.Collect(t.Context(), fc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findSample(samples, "ecs_node_dt_total"); ok {
		t.Error("Flux emitted ecs_node_dt_total while collectDT owns it")
	}
	for _, q := range fc.queries {
		if strings.Contains(q, "dtquery_dt_dist_host_dt_node_id") {
			t.Error("Flux issued the per-node DT query it does not own")
		}
	}
}

func TestFluxClusterDTIsUnaffectedByArbitration(t *testing.T) {
	// The cluster totals have no per-node equivalent and no other owner.
	f := Flux{DTOwnedByDT: true, now: func() time.Time { return captureInstant }}
	samples, err := f.Collect(t.Context(), fluxMock(t, map[string]string{
		"dtquery_dt_status": "flux_dt_status.json",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findSample(samples, "ecs_cluster_dt_total"); !ok {
		t.Error("cluster DT totals must survive per-node arbitration")
	}
}
```

Record the issued queries in `fluxClient` so the second test can assert an
absence: add a `queries []string` field and append `q["query"]` to it at the top
of `Post`.

Append to `internal/ecs/collector_test.go`:

```go
func TestRegistryGivesDTOwnershipToTheDTCollector(t *testing.T) {
	both := Registry(config.Cluster{CollectFlux: true, CollectDT: true})
	fluxOnly := Registry(config.Cluster{CollectFlux: true})
	find := func(rcs []ResourceCollector) Flux {
		t.Helper()
		for _, rc := range rcs {
			if f, ok := rc.(Flux); ok {
				return f
			}
		}
		t.Fatal("no Flux collector in the registry")
		return Flux{}
	}
	if !find(both).DTOwnedByDT {
		t.Error("with collectDT on, the DT collector must own the per-node name")
	}
	if find(fluxOnly).DTOwnedByDT {
		t.Error("with collectDT off, Flux must own the per-node name")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/ecs/ -run 'TestFluxCollectsPerNodeDT|TestFluxSkips|TestFluxClusterDT|TestRegistryGivesDT' -v`
Expected: FAIL — `Flux` has no `DTOwnedByDT` field and no per-node DT query.

- [ ] **Step 3: Widen the node inventory**

In `internal/ecs/info.go`, add two fields to `vdcNodesResp`'s node struct:

```go
		Nodename  string `json:"nodename"`
		MgmtIP    string `json:"mgmt_ip"`
		DataIP    string `json:"data_ip"`
		Data2IP   string `json:"data2_ip"`
		PrivateIP string `json:"private_ip"`
```

and index them in `newNodeMapper` (`internal/ecs/flux.go`), extending the key
list:

```go
		for _, key := range []string{n.Nodename, shortHost(n.Nodename), n.MgmtIP, n.DataIP, n.Data2IP, n.PrivateIP} {
```

The captured cluster reports `data2_ip` equal to `data_ip`, which is harmless:
the collision guard only blanks a key two *different* nodes claim, and here one
node claims it twice.

- [ ] **Step 4: Add the host-tag column and the query**

In `internal/ecs/flux.go`, add to `fluxQuery`:

```go
	// hostTag is the column carrying the node's identity. Empty means "host".
	// dtquery_dt_dist_host_dt_node_id identifies the node under dt_node_id
	// instead, holding its data_ip rather than a hostname.
	hostTag string
```

and use it in `samples`, replacing `row.value("host")`:

```go
		if q.perNode {
			tag := q.hostTag
			if tag == "" {
				tag = "host"
			}
			host, ok := row.value(tag)
			…
		}
```

Add the query to `fluxQueries`, after the `dtquery_dt_status` entry:

```go
	{
		// Tagged {dt_node_id, process, tag}: no host column, but dt_node_id
		// carries the node's data_ip, which the inventory indexes. On the live
		// 4.3 capture the per-node counts sum to dtquery_dt_status's cluster
		// total, so this is that total's breakdown under another column name.
		// Owned by the DT collector when that one runs — see Registry.
		bucket: "monitoring_op", measurement: "dtquery_dt_dist_host_dt_node_id",
		perNode: true, hostTag: "dt_node_id", dtPerNode: true,
		fields: []fluxField{
			{field: "count_i", name: "ecs_node_dt_total"},
		},
	},
```

Add the marker field to `fluxQuery`:

```go
	// dtPerNode marks the query the DT collector owns when it is enabled.
	dtPerNode bool
```

and the skip plus the flag on `Flux`:

```go
type Flux struct {
	// DTOwnedByDT suppresses the per-node DT query when the opt-in DT collector
	// runs: it serves unready and unknown per node as well, so where it is
	// reachable it is the richer source and keeps the name (ADR-0006).
	DTOwnedByDT bool
	now         func() time.Time
}
```

in `Collect`'s loop, before `attempted++`:

```go
		if q.dtPerNode && f.DTOwnedByDT {
			continue
		}
```

Note `Collect`'s receiver must become `func (f Flux) Collect(...)`.

- [ ] **Step 5: Wire the arbitration**

In `internal/ecs/resource.go`:

```go
	if cl.CollectFlux {
		// The DT collector, where it runs, owns ecs_node_dt_total: it is the only
		// source of unready and unknown per node, and Flux has no breakdown of
		// either. Decided here, once, like Nodes' arbitration above.
		rcs = append(rcs, Flux{DTOwnedByDT: cl.CollectDT})
	}
```

- [ ] **Step 6: Run to verify they pass**

Run: `go test ./internal/ecs/ -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
make sure
git add internal/ecs/
git commit -m "feat(flux): report directory tables per node

ADR-0011 concluded Flux could not: dtquery_dt_dist_host_dt_node_id carries no
host tag, despite its name. The live capture shows the conclusion does not
follow -- it identifies the node under dt_node_id, holding the data_ip the
inventory already indexes, and the five per-node counts sum to exactly the
cluster total from the same run.

fluxQuery gains hostTag for it. The node inventory now indexes private_ip and
data2_ip too, so a cluster that names its DT nodes differently joins instead of
counting itself unmapped.

collectDT keeps ecs_node_dt_total wherever it runs -- it serves unready and
unknown per node as well -- so this only fills the gap on the segmented
topology where port 9101 is unreachable and collectDT cannot work at all.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Request latency as a histogram

**Files:**
- Modify: `internal/ecs/flux.go` (bucket mode, the latency query)
- Modify: `internal/ecs/nodes.go:88-98` (give up the gauge name)
- Test: `internal/ecs/flux_test.go`, `internal/ecs/nodes_test.go`,
  `internal/ecs/prometheus_test.go`, `internal/ecs/otlp_test.go`

**Interfaces:**
- Consumes: `flux_latency.json` (Task 2), `samples(rows, mapper, now)` (Task 3),
  `hostTag` (Task 5).
- Produces: `fluxQuery.buckets *fluxBuckets` with
  `type fluxBuckets struct { name string; idLabels map[string]string }`; the
  metrics `ecs_node_transaction_latency_milliseconds_bucket{node,op,le}` and
  `ecs_node_transaction_latency_milliseconds_count{node,op}`, both counters.

- [ ] **Step 1: Write the failing tests**

Append to `internal/ecs/flux_test.go`:

```go
func TestFluxLatencyBuckets(t *testing.T) {
	// The field names are the bucket bounds and the values are cumulative, with
	// +Inf equal to the last finite bound -- a Prometheus histogram in every
	// respect except that the store serves no _sum.
	f := Flux{now: func() time.Time { return captureInstant }}
	samples, err := f.Collect(t.Context(), fluxMock(t, map[string]string{
		"statDataHead_performance_internal_latency": "flux_latency.json",
	}))
	if err != nil {
		t.Fatal(err)
	}
	node := Label{"node", "supr01-r01"}
	read := Label{"op", "read"}

	inf, ok := findSample(samples, "ecs_node_transaction_latency_milliseconds_bucket", node, read, Label{"le", "+Inf"})
	if !ok {
		t.Fatal("no +Inf bucket")
	}
	if inf.Type != Counter {
		t.Error("histogram buckets are cumulative counters")
	}
	count, ok := findSample(samples, "ecs_node_transaction_latency_milliseconds_count", node, read)
	if !ok {
		t.Fatal("no _count series")
	}
	if count.Value != inf.Value {
		t.Errorf("_count = %v, +Inf bucket = %v; they are the same number", count.Value, inf.Value)
	}
	// ttlb_write maps onto the write op, the same dimension the dashboard path
	// uses for this family.
	if _, ok := findSample(samples, "ecs_node_transaction_latency_milliseconds_bucket",
		node, Label{"op", "write"}, Label{"le", "+Inf"}); !ok {
		t.Error("no write-op buckets: ttlb_write did not map onto op=write")
	}
	// No _sum: ECS does not serve one, and inventing it would be a lie.
	if _, ok := findSample(samples, "ecs_node_transaction_latency_milliseconds_sum"); ok {
		t.Error("a _sum was emitted; the store serves none")
	}
}

func TestFluxLatencyBucketLabelKeyOrder(t *testing.T) {
	// One name, one ordered label-key set (ADR-0006).
	f := Flux{now: func() time.Time { return captureInstant }}
	samples, err := f.Collect(t.Context(), fluxMock(t, map[string]string{
		"statDataHead_performance_internal_latency": "flux_latency.json",
	}))
	if err != nil {
		t.Fatal(err)
	}
	s, _ := findSample(samples, "ecs_node_transaction_latency_milliseconds_bucket")
	got := make([]string, len(s.Labels))
	for i, l := range s.Labels {
		got[i] = l.Key
	}
	if !slices.Equal(got, []string{"node", "op", "le"}) {
		t.Errorf("label keys = %v, want [node op le]", got)
	}
}

func TestFluxLatencyIgnoresUnknownIDs(t *testing.T) {
	// An id the mapping does not cover would otherwise land under a short label
	// set and break the name's schema.
	q := fluxQuery{
		bucket: "monitoring_main", measurement: "statDataHead_performance_internal_latency",
		perNode: false,
		buckets: &fluxBuckets{
			name:     "ecs_node_transaction_latency_milliseconds",
			idLabels: map[string]string{"ttfb_read": "read", "ttlb_write": "write"},
		},
	}
	rows := []fluxRow{{cols: map[string]string{
		"_field": "1.0", "_value": "5", "_time": captureInstant.Format(time.RFC3339), "id": "ttlb_read",
	}}}
	out, _, _ := q.samples(rows, nil, captureInstant)
	if len(out) != 0 {
		t.Errorf("an unmapped id produced %d samples, want none", len(out))
	}
}
```

Append to `internal/ecs/nodes_test.go`:

```go
func TestNodesGivesUpLatencyWhenFluxOwnsIt(t *testing.T) {
	// Prometheus reads X_bucket as belonging to a histogram named X, so the
	// dashboard gauge and the Flux histogram cannot both hold this family.
	with, err := Nodes{FluxOwnsPerf: true}.Collect(t.Context(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findSample(with, "ecs_node_transaction_latency_milliseconds"); ok {
		t.Error("Nodes emitted the latency gauge while Flux owns the family")
	}
	// The cluster-level name has no Flux equivalent and is untouched.
	without, err := Nodes{}.Collect(t.Context(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findSample(without, "ecs_node_transaction_latency_milliseconds"); !ok {
		t.Error("Nodes stopped emitting the latency gauge with Flux off")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/ecs/ -run 'TestFluxLatency|TestNodesGivesUpLatency' -v`
Expected: FAIL — `fluxBuckets` undefined.

- [ ] **Step 3: Implement the bucket mode**

In `internal/ecs/flux.go`, add the type and the field:

```go
// fluxBuckets describes a measurement whose field *names* are histogram bucket
// bounds and whose values are cumulative counts.
//
// statDataHead_performance_internal_latency is the only such measurement, and
// it is the source the ECS dashboard reads its own read/write latency from —
// which is what justifies mapping its id tag onto the op dimension the
// dashboard-sourced family already uses. The store serves no _sum, so
// prometheus.MustNewConstHistogram is unusable and the buckets are published as
// ordinary counters carrying an le label, which is what histogram_quantile
// consumes.
type fluxBuckets struct {
	// name is the family name; _bucket and _count are appended to it.
	name string
	// idLabels maps the id tag onto its op label value. A row whose id is absent
	// from this map is dropped rather than published under a short label set.
	idLabels map[string]string
}
```

```go
	// buckets, when set, reads field names as bucket bounds instead of matching
	// them against fields.
	buckets *fluxBuckets
```

In `samples`, after the base labels are built and before the `q.fields` loop,
handle the bucket mode and skip the normal path:

```go
		if q.buckets != nil {
			op, ok := row.value("id")
			if !ok {
				continue
			}
			opLabel, ok := q.buckets.idLabels[op]
			if !ok {
				continue // an id the mapping does not cover
			}
			labels := append(slices.Clone(base), Label{"op", opLabel})
			out = append(out, Sample{
				Name:   q.buckets.name + "_bucket",
				Labels: append(slices.Clone(labels), Label{"le", field}),
				Value:  v,
				Type:   Counter,
			})
			// _count is the +Inf bucket: every observation falls under it.
			if field == "+Inf" {
				out = append(out, Sample{
					Name:   q.buckets.name + "_count",
					Labels: labels,
					Value:  v,
					Type:   Counter,
				})
			}
			continue
		}
```

Add the query to `fluxQueries`:

```go
	{
		bucket: "monitoring_main", measurement: "statDataHead_performance_internal_latency",
		perNode: true,
		buckets: &fluxBuckets{
			name:     "ecs_node_transaction_latency_milliseconds",
			idLabels: map[string]string{"ttfb_read": "read", "ttlb_write": "write"},
		},
	},
```

- [ ] **Step 4: Hand the family over in nodes.go**

In `internal/ecs/nodes.go`, replace the unconditional transaction block
(currently line 98):

```go
		tx := n.transactionFields.samples("ecs_node", node)
		if nc.FluxOwnsPerf {
			// Flux serves this family as a histogram, and Prometheus reads
			// ecs_node_transaction_latency_milliseconds_bucket as belonging to a
			// histogram of that name — so the gauge and the histogram cannot both
			// hold it (ADR-0006). The bandwidth and TPS names have no Flux
			// equivalent and stay here.
			tx = slices.DeleteFunc(tx, func(s Sample) bool {
				return s.Name == "ecs_node_transaction_latency_milliseconds"
			})
		}
		out = append(out, tx...)
```

Add `"slices"` to that file's imports, and update `Nodes`' doc comment: it now
suppresses four names, not three.

- [ ] **Step 5: Assert both export paths**

Append to `internal/ecs/prometheus_test.go` a case that gathers a snapshot
containing a `_bucket` sample and asserts the series appears with its `le` label
and a counter type; append the mirror to `internal/ecs/otlp_test.go` using the
`ManualReader`, asserting the `le` attribute survives. Follow the existing
tests' structure in each file.

- [ ] **Step 6: Run to verify they pass**

Run: `go test ./internal/ecs/ -v`
Expected: PASS, including `TestLabelKeyConsistency`.

- [ ] **Step 7: Commit**

```bash
make sure
git add internal/ecs/
git commit -m "feat(flux): publish read/write latency as histogram buckets

statDataHead_performance_internal_latency names its fields after bucket bounds
and counts cumulatively, with +Inf equal to the last finite bound. Its id tag
takes two values, ttfb_read and ttlb_write, and its rows carry tag=dashboard --
this is the source the ECS dashboard reads its own read/write latency from,
which is what justifies mapping id onto the op dimension.

Published as _bucket{node,op,le} plus _count, both counters. No _sum: the store
serves none, so histogram_quantile works and average latency does not.

Nodes gives up ecs_node_transaction_latency_milliseconds when collectFlux is
on, because Prometheus reads X_bucket as belonging to a histogram named X and
the gauge cannot share the family.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Warn once, then debug

**Files:**
- Modify: `internal/ecs/flux.go` (`Flux` gains the silence set; `Collect` uses it)
- Test: `internal/ecs/flux_test.go`

**Interfaces:**
- Consumes: `Flux` from Tasks 3 and 5.
- Produces: `Flux.silent map[string]bool`, lazily created; no exported surface.

- [ ] **Step 1: Write the failing test**

Append to `internal/ecs/flux_test.go`:

```go
func TestFluxWarnsOncePerSilentMeasurement(t *testing.T) {
	// A measurement the cluster legitimately does not carry would otherwise warn
	// on every cycle forever, about something the operator cannot fix.
	var hook silenceHook
	log.AddHook(&hook)
	defer func() { log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) }()

	f := Flux{now: func() time.Time { return captureInstant }}
	c := fluxMock(t, map[string]string{"cpu": "flux_cpu.json"})
	for range 3 {
		if _, err := f.Collect(t.Context(), c); err != nil {
			t.Fatal(err)
		}
	}
	if hook.warns != 1 {
		t.Errorf("warned %d times for the same silent measurement, want 1", hook.warns)
	}
	if hook.debugs == 0 {
		t.Error("later cycles logged nothing at debug")
	}
}
```

Define `silenceHook` in the test file, counting `Warn` and `Debug` entries whose
message mentions a silent measurement. Note this test requires `Flux` to be
addressable across cycles — call `Collect` on the same value, and hold the set
by pointer inside the struct so a value receiver still mutates it.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/ecs/ -run TestFluxWarnsOnce -v`
Expected: FAIL — three warnings.

- [ ] **Step 3: Implement**

In `internal/ecs/flux.go`, add to `Flux`:

```go
	// silent remembers which measurements have already been reported as
	// returning nothing, so a cluster that legitimately does not carry one is
	// announced once rather than on every cycle. Held by pointer so the value
	// receiver Collect uses still mutates it; rebuilt when Registry rebuilds the
	// collector, which is what makes a config reload re-announce.
	silent *silenceSet
```

```go
// silenceSet tracks measurements already reported silent. A measurement that
// starts answering again is forgotten, so a later disappearance warns afresh.
type silenceSet struct {
	mu   sync.Mutex
	seen map[string]bool
}

// firstTime reports whether this is the first cycle in which the measurement
// came back empty.
func (s *silenceSet) firstTime(key string) bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	if s.seen[key] {
		return false
	}
	s.seen[key] = true
	return true
}

// answered forgets a measurement that produced rows.
func (s *silenceSet) answered(key string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.seen, key)
}
```

In `Collect`, replace the empty-rows branch:

```go
		key := q.bucket + "/" + q.measurement
		if len(rows) == 0 {
			entry := log.WithFields(log.Fields{
				"cluster": c.Name(), "bucket": q.bucket, "measurement": q.measurement,
			})
			if f.silent.firstTime(key) {
				entry.Warn("Flux measurement returned no rows; its samples are absent this cycle")
			} else {
				entry.Debug("Flux measurement still returns no rows")
			}
			continue
		}
		f.silent.answered(key)
```

In `internal/ecs/resource.go`, give the collector its set:

```go
		rcs = append(rcs, Flux{DTOwnedByDT: cl.CollectDT, silent: &silenceSet{}})
```

Add `"sync"` to `flux.go`'s imports.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/ecs/ -run TestFluxWarnsOnce -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
make sure
git add internal/ecs/
git commit -m "fix(flux): warn once about a silent measurement, then log at debug

A measurement the cluster legitimately does not carry warned on every cycle --
a permanent stream about something the operator cannot act on. It is announced
once now and logged at debug thereafter, and a measurement that starts
answering again is forgotten so a later disappearance warns afresh.

The set lives on the collector Registry builds, so a config reload
re-announces, which is the wanted behaviour.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: The mock Flux endpoint

**Files:**
- Create: `cmd/mockecs/flux.go`
- Modify: `cmd/mockecs/main.go` (register the route)
- Test: manual — `make demo`

**Interfaces:**
- Consumes: the mirrored fixtures from Task 2.
- Produces: `func fluxHandler() http.HandlerFunc` in package `main`.

- [ ] **Step 1: Write the handler**

Create `cmd/mockecs/flux.go`:

```go
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// fluxFixtures maps a measurement name to its embedded capture. A measurement
// absent from this map answers with the empty envelope a real cluster returns
// for a measurement it does not carry.
var fluxFixtures = map[string]string{
	"cpu":                                         "fixtures/flux_cpu.json",
	"mem":                                         "fixtures/flux_mem.json",
	"net":                                         "fixtures/flux_net.json",
	"dtquery_dt_status":                           "fixtures/flux_dt_status.json",
	"dtquery_dt_dist_host_dt_node_id":             "fixtures/flux_dt_dist.json",
	"statDataHead_performance_internal_transactions": "fixtures/flux_transactions.json",
	"statDataHead_performance_internal_throughput":   "fixtures/flux_throughput.json",
	"statDataHead_performance_internal_latency":      "fixtures/flux_latency.json",
	"cq_performance_transaction":                  "fixtures/flux_cq_transaction.json",
	"cq_performance_throughput":                   "fixtures/flux_cq_throughput.json",
}

// emptyEnvelope is what a live 4.3 answers for a measurement the store does not
// carry: HTTP 200, one Series with every field null.
const emptyEnvelope = `{"Series":[{"Datatypes":null,"Columns":null,"Values":null}]}`

// fluxHandler serves POST /flux/api/external/v2/query. All queries share one
// path, so the measurement is read out of the request body — the same routing
// the collector's own tests use.
func fluxHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("X-SDS-AUTH-TOKEN") != mockToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		var file string
		for measurement, f := range fluxFixtures {
			if strings.Contains(string(body), `"`+measurement+`"`) {
				file = f
				break
			}
		}
		if file == "" {
			_, _ = io.WriteString(w, emptyEnvelope)
			return
		}
		raw, err := fixtures.ReadFile(file)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		out, err := freshenTimes(raw, time.Now().UTC())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(out)
	}
}

// freshenTimes rewrites every _time cell to now.
//
// The fixtures are real captures taken at a fixed instant in July 2026, and the
// collector drops points older than fluxMaxAge — so replayed literally, every
// demo row would be correctly discarded as stale and make demo would show an
// empty dashboard. Only _time is touched; values, columns and datatypes stay as
// the cluster wrote them.
func freshenTimes(raw []byte, now time.Time) ([]byte, error) {
	var env struct {
		Series []struct {
			Datatypes []string   `json:"Datatypes"`
			Columns   []string   `json:"Columns"`
			Values    [][]string `json:"Values"`
		} `json:"Series"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	stamp := now.Format(time.RFC3339Nano)
	for _, s := range env.Series {
		for i, col := range s.Columns {
			if col != "_time" {
				continue
			}
			for _, row := range s.Values {
				if i < len(row) {
					row[i] = stamp
				}
			}
		}
	}
	return json.Marshal(env)
}
```

Note `mockecs`'s `//go:embed fixtures/*.json` already covers the new files, and
the `_comment` key in the two synthesized fixtures is dropped by this round-trip
— harmless, since it is documentation for a reader of the file, not for the
demo.

- [ ] **Step 2: Register the route**

In `cmd/mockecs/main.go`, after the billing route:

```go
	// The Flux monitoring store (opt-in collectFlux). All queries share one
	// path, so the handler routes on the measurement named in the body.
	mux.HandleFunc("/flux/api/external/v2/query", fluxHandler())
```

Extend the package doc comment to mention the Flux endpoint.

- [ ] **Step 3: Verify the demo serves it**

```bash
go build ./cmd/mockecs
```

Enable the flag for the demo cluster in `config.demo.yaml` by adding
`collectFlux: true` to the cluster entry, then:

```bash
make demo
```

Expected: the exporter logs no `Flux measurement returned no rows` warning for
`cpu`, `mem`, `net`, `dtquery_dt_status`, `dtquery_dt_dist_host_dt_node_id`,
the three `statDataHead_*` measurements, or the two `cq_*` ones. Confirm the
series exist:

```bash
curl -s localhost:9101/metrics | grep -c ecs_node_transaction_latency_milliseconds_bucket
curl -s localhost:9101/metrics | grep ecs_node_dt_total
```

Expected: a non-zero bucket count, and per-node DT rows.

- [ ] **Step 4: Commit**

```bash
make sure
git add cmd/mockecs/ config.demo.yaml
git commit -m "feat(mockecs): serve the Flux query endpoint

make demo has never exercised the Flux collector: mockecs served no
/flux/api/external/v2/query at all, so the one collector built entirely from
documentation was also the one the demo could not show.

All Flux queries share a path, so the handler routes on the measurement named
in the body -- the same routing the collector's tests use -- and answers the
real empty envelope for anything it does not carry.

_time is rewritten to now on the way out. The fixtures are captures from a
fixed instant, and the staleness guard would correctly discard every one of
them.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: Making a Flux trace readable

**Files:**
- Modify: `internal/ecsclient/client.go:97-108` (trace hook)
- Modify: `internal/ecs/flux.go` (per-measurement debug line)
- Test: `internal/ecsclient/client_test.go`, `internal/ecs/flux_test.go`

**Interfaces:**
- Consumes: the `stale` return value from Task 3.
- Produces: nothing new for other tasks.

- [ ] **Step 1: Write the failing test**

Append to `internal/ecs/flux_test.go` a test capturing log entries at debug level
and asserting that collecting `cpu` emits one entry carrying the fields
`bucket`, `measurement`, `rows`, `samples`, `unmapped` and `stale`, with `rows`
matching the fixture's row count.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/ecs/ -run TestFluxDebugLine -v`
Expected: FAIL — no such entry.

- [ ] **Step 3: Add the per-measurement account**

In `internal/ecs/flux.go`, after `samples` returns:

```go
		samples, miss, stale := q.samples(rows, mapper, now)
		log.WithFields(log.Fields{
			"cluster":  c.Name(),
			"bucket":   q.bucket,
			"measurement": q.measurement,
			"rows":     len(rows),
			"samples":  len(samples),
			"unmapped": miss,
			"stale":    stale,
		}).Debug("Flux measurement collected")
```

- [ ] **Step 4: Attribute the trace**

The trace hook logs `method`, `url` and `status`, which is enough for the
dashboard API and useless for Flux: every query is a POST to the same path. Add
the request body's measurement.

In `internal/ecsclient/client.go`, inside the `cfg.Trace` hook, before the log
call:

```go
			fields := log.Fields{
				"cluster": cfg.Name,
				"method":  r.Request.Method,
				"url":     r.Request.URL,
				"status":  r.StatusCode(),
			}
			// Every Flux query is a POST to one path, so the URL identifies
			// nothing. The query itself is the only thing that tells ten
			// otherwise identical trace blocks apart.
			if q, ok := r.Request.Body.(map[string]string); ok {
				if query := q["query"]; query != "" {
					fields["query"] = query
				}
			}
			log.WithFields(fields).Infof("API trace:\n%s", r.Body())
```

- [ ] **Step 5: Verify the trace end to end**

```bash
make demo   # in one terminal
go run . --config config.demo.yaml --once --trace --debug 2>&1 | grep -c 'query='
```

Expected: one line per Flux query, each carrying its measurement.

- [ ] **Step 6: Commit**

```bash
make sure
git add internal/ecsclient/ internal/ecs/
git commit -m "feat(trace): tell ten identical Flux queries apart

--trace logged method, url and status, which identifies a dashboard request
and nothing at all about a Flux one: every query is a POST to the same path,
and the request body was never logged. A Flux trace was ten indistinguishable
blocks -- unusable for the live-cluster validation the flag exists for.

The query now rides along, and each measurement gets one debug line: rows read,
samples emitted, rows dropped unmapped, rows dropped stale.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Task 10: The `flux-capture` subcommand

**Files:**
- Create: `main_fluxcapture.go`
- Modify: `main.go` (register the subcommand)
- Test: manual against `mockecs`

**Interfaces:**
- Consumes: `config.Load`, `ecsclient.NewClusterClient`, `ecs.FluxScripts`.
- Produces: `func fluxCaptureCmd() *cobra.Command`; and in package `ecs`,
  `func FluxScripts() map[string]string` returning `bucket/measurement → script`,
  plus `func FluxScriptFor(bucket, measurement string) string` for the free-form
  mode. Exported because `main` is a different package.

- [ ] **Step 1: Export the script table**

Add to `internal/ecs/flux.go`:

```go
// FluxScripts returns every query this collector issues, keyed
// "bucket/measurement". Exported for the flux-capture subcommand, which replays
// the real table rather than a hand-written approximation — a capture of
// queries we do not issue proves nothing about the ones we do.
func FluxScripts() map[string]string {
	out := make(map[string]string, len(fluxQueries))
	for _, q := range fluxQueries {
		out[q.bucket+"/"+q.measurement] = q.script()
	}
	return out
}

// FluxScriptFor renders an ad-hoc query in the same shape, for probing a
// measurement the table does not carry.
func FluxScriptFor(bucket, measurement string) string {
	return fluxQuery{bucket: bucket, measurement: measurement}.script()
}

// FluxPath is the query endpoint, exported for the same reason.
const FluxPath = fluxPath
```

- [ ] **Step 2: Write the subcommand**

Create `main_fluxcapture.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fjacquet/obs_exporter/internal/config"
	"github.com/fjacquet/obs_exporter/internal/ecs"
	"github.com/fjacquet/obs_exporter/internal/ecsclient"
	"github.com/spf13/cobra"
)

// fluxCaptureCmd replays the collector's own query table once and writes each
// response to its own file.
//
// It exists because the live-cluster reporter is reachable by email on a
// months-long round trip, and assembling this by hand is what made the last
// capture a campaign. The output is deliberately raw: sanitizing is the
// reporter's call, on their data policy, and half-done automatic redaction is
// worse than none.
func fluxCaptureCmd() *cobra.Command {
	var cfgPath, cluster, outDir, bucket, measurement string
	cmd := &cobra.Command{
		Use:   "flux-capture",
		Short: "Query the Flux store once and write each measurement's raw response to a file",
		Long: "Runs the exporter's own Flux query table against one configured cluster and " +
			"writes one JSON file per measurement, plus a summary. Use --bucket and " +
			"--measurement together to probe a measurement the table does not carry. " +
			"Responses are written verbatim: sanitize before sharing them.",
		RunE: func(_ *cobra.Command, _ []string) error {
			config.LoadDotEnv(cfgPath)
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			var target *config.Cluster
			for i := range cfg.Clusters {
				if cfg.Clusters[i].Name == cluster || cluster == "" {
					target = &cfg.Clusters[i]
					break
				}
			}
			if target == nil {
				return fmt.Errorf("no cluster named %q in %s", cluster, cfgPath)
			}
			if err := os.MkdirAll(outDir, 0o750); err != nil {
				return err
			}

			c := ecsclient.NewClusterClient(ecsclient.Config{
				Name:               target.Name,
				BaseURL:            target.BaseURL(),
				Username:           target.Username,
				Password:           target.Password,
				InsecureSkipVerify: target.InsecureSkipVerify,
			})
			defer func() { _ = c.Close() }()

			scripts := ecs.FluxScripts()
			if bucket != "" && measurement != "" {
				scripts = map[string]string{
					bucket + "/" + measurement: ecs.FluxScriptFor(bucket, measurement),
				}
			}

			type result struct {
				Key   string `json:"measurement"`
				Query string `json:"query"`
				Rows  int    `json:"rows"`
				Err   string `json:"error,omitempty"`
			}
			var summary []result
			for key, script := range scripts {
				var raw json.RawMessage
				r := result{Key: key, Query: script}
				if err := c.Post(cmd.Context(), ecs.FluxPath,
					map[string]string{"query": script}, &raw); err != nil {
					r.Err = err.Error()
					summary = append(summary, r)
					fmt.Fprintf(os.Stderr, "%s: %v\n", key, err)
					continue
				}
				name := strings.ReplaceAll(key, "/", "-") + ".json"
				if err := os.WriteFile(filepath.Join(outDir, name), raw, 0o600); err != nil {
					return err
				}
				r.Rows = countRows(raw)
				summary = append(summary, r)
				fmt.Printf("%s: %d rows -> %s\n", key, r.Rows, name)
			}
			b, err := json.MarshalIndent(summary, "", "  ")
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(outDir, "summary.json"), b, 0o600)
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "config.yaml", "path to config file")
	cmd.Flags().StringVar(&cluster, "cluster", "", "cluster name (default: the first configured)")
	cmd.Flags().StringVar(&outDir, "out", "flux-capture", "directory to write responses into")
	cmd.Flags().StringVar(&bucket, "bucket", "", "probe one measurement: its bucket")
	cmd.Flags().StringVar(&measurement, "measurement", "", "probe one measurement: its name")
	return cmd
}

// countRows reports how many rows a raw envelope carried, for the summary.
func countRows(raw []byte) int {
	var env struct {
		Series []struct {
			Values [][]json.RawMessage `json:"Values"`
		} `json:"Series"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return 0
	}
	n := 0
	for _, s := range env.Series {
		n += len(s.Values)
	}
	return n
}
```

Check `config.Cluster`'s accessor for the base URL before writing this; if the
field is named differently, use the existing name rather than inventing
`BaseURL()`. `buildTargets` in `main.go` already builds a client from a cluster —
reuse whatever it does.

- [ ] **Step 3: Register it**

In `main.go`, before `root.Execute()`:

```go
	root.AddCommand(fluxCaptureCmd())
```

- [ ] **Step 4: Verify against mockecs**

```bash
make demo   # in one terminal
go run . flux-capture --config config.demo.yaml --out /tmp/flux-capture
ls /tmp/flux-capture
go run . flux-capture --config config.demo.yaml --out /tmp/probe \
  --bucket monitoring_vdc --measurement cq_performance_transaction
```

Expected: one file per measurement plus `summary.json`; the probe writes a single
file.

- [ ] **Step 5: Commit**

```bash
make sure
git add main.go main_fluxcapture.go internal/ecs/flux.go
git commit -m "feat(cli): add flux-capture for live-cluster validation

The live-cluster reporter is reachable by email on a months-long round trip,
and assembling the last capture by hand is what made it a campaign. This
replays the collector's own query table -- not a hand-written approximation,
because a capture of queries we do not issue proves nothing about the ones we
do -- and writes one file per measurement plus a summary.

--bucket/--measurement probes a measurement the table does not carry, which is
how an open question gets answered without hand-written curl.

Output is verbatim. Sanitizing is the reporter's call on their data policy.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Task 11: Documentation

**Files:**
- Modify: `docs/metrics/flux.md`, `docs/metrics/index.md`
- Modify: `docs/adr/0011-flux-collector-for-unreachable-metrics.md`
- Modify: `docs/adr/0004-token-auth-retry-policy.md`
- Create: `docs/operate/flux-validation.md`
- Modify: `mkdocs.yml`
- Test: `uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict`

- [ ] **Step 1: Rewrite `docs/metrics/flux.md`**

Changes, in place:

1. Add the two new rows to the `monitoring_op` cluster-scoped and per-node
   tables: `dtquery_dt_dist_host_dt_node_id` / `count_i` →
   `ecs_node_dt_total{node}`, gauge, joined on `dt_node_id` = `data_ip`.
2. Add a `monitoring_main` histogram block for
   `ecs_node_transaction_latency_milliseconds_bucket{node,op,le}` and `_count`.
3. Replace the "Cluster-wide, and not a replacement for `collectDT`" note: Flux
   now serves the per-node count where `collectDT` is off, and `collectDT` keeps
   the name wherever it runs.
4. Add a note that no `_sum` is served — `histogram_quantile` works, average
   latency does not — and that
   `ecs_node_transaction_latency_milliseconds` (the gauge) is suppressed when
   `collectFlux` is on.
5. Add a staleness note: a point older than ten minutes is dropped, so a node
   that stops emitting goes absent within two cadence periods; alert with
   `absent()`, not on a zero.
6. Add the six-operation whitelist as the reason there is one request per
   measurement and no server-side aggregation.
7. Replace the "documentation-derived" hedging: every measurement, field and tag
   in the tables is confirmed against a 4.3.0.0.142978 capture dated 2026-07-31,
   except `cq_performance_transaction` and `cq_performance_throughput`, which are
   confirmed in prose with no payload attached.

- [ ] **Step 2: Update `docs/metrics/index.md`**

Add the new metric names to the index, keeping its existing grouping and the
`_total`-names-that-are-gauges count accurate — the two new counters and the
histogram change it.

- [ ] **Step 3: Update ADR-0011**

- Status: add that the live capture of 2026-07-31 closes the deferred
  verification the Consequences section recorded.
- Context: add the operation whitelist as a constraint.
- The open-questions table: questions 2, 4 and 5 gain live-cluster confirmation
  in their Source column; question 5's answer is narrowed — Flux *does* report
  per-node DT via `dt_node_id`, and `collectDT` keeps the name where it runs.
- Correct the Context correction: `dtquery_dt_dist_host_dt_node_id` has no
  `host` tag, which is true, but it identifies the node under `dt_node_id`, so
  the conclusion drawn from it was wrong.
- Consequences: the deferred-verification paragraph is replaced by what the
  capture confirmed and what remains open (the two `cq_*` payloads, the latency
  unit, the `tag` column).
- Add the staleness rule as a constraint that follows from `last()`.

- [ ] **Step 4: Update ADR-0004**

Add that the retry policy is no longer expressed in status classes alone: a 5xx
whose body carries `retryable:false` or code 6401 is permanent and is not
retried, and a body that does not decode makes no claim and retries as before.

- [ ] **Step 5: Write `docs/operate/flux-validation.md`**

A short page for the reporter: what to run
(`obs_exporter flux-capture --config … --out …`, and `--once --trace --debug`
for a full cycle), what to send back, and the specific open questions — the two
`cq_*` payloads, the unit of the latency bucket bounds, the meaning of the `tag`
column, and whether `*_Process_status` and `diskio` are absent on that build or
everywhere. State that responses are verbatim and must be sanitized before
sharing.

- [ ] **Step 6: Add the nav entry and build**

Add the page to `mkdocs.yml` under the operate section.

Run: `uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict`
Expected: builds clean, no dangling anchors.

- [ ] **Step 7: Commit**

```bash
make sure
git add docs/ mkdocs.yml
git commit -m "docs(flux): say what the live capture confirmed, and what it did not

The collector's docs hedged everywhere, because everything in them came from
the 4.3 admin guide. Every measurement, field and tag in the tables is now
confirmed against a 4.3.0.0.142978 capture -- except cq_performance_transaction
and cq_performance_throughput, which are confirmed in prose with no payload,
and say so.

Adds the two new families, the six-operation whitelist that makes one request
per measurement the only possible shape, the staleness rule, and a validation
page so the September round trip is a command rather than a campaign.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Task 12: Dashboards, changelog, and the full gate

**Files:**
- Modify: `grafana/dashboards/obs-overview.json`
- Modify: `CHANGELOG.md`
- Test: `make ci`, `make demo`

- [ ] **Step 1: Add the panels**

In `grafana/dashboards/obs-overview.json`, following the existing panels'
structure and datasource variable:

- A latency panel:
  `histogram_quantile(0.95, sum by (le, node) (rate(ecs_node_transaction_latency_milliseconds_bucket{cluster="$cluster",op="read"}[5m])))`,
  and the same for `op="write"`, unit milliseconds.
- A per-node DT panel: `ecs_node_dt_total{cluster="$cluster"}`.

Confirm the existing panels' `cluster` template variable name before writing the
queries — copy it, do not assume it.

- [ ] **Step 2: Verify against the demo**

```bash
make demo
```

Open Grafana on `:3000` and confirm both panels draw. The `_time` rewriting from
Task 8 is what makes this possible; an empty panel here means the fixtures are
being dropped as stale.

- [ ] **Step 3: Write the changelog entry**

Under `## [Unreleased]`, an entry covering: the body-aware retry and `APIError`;
the staleness guard; per-node DT and its arbitration; the latency histogram and
the gauge it displaces; per-query failure tolerance; warn-once; the mockecs Flux
endpoint; `flux-capture`; and the fixture replacement. Note the two behaviour
changes an operator would notice — `ecs_node_transaction_latency_milliseconds`
disappears when `collectFlux` is on, and Flux-sourced series now go absent within
ten minutes of a node falling silent instead of holding a stale value.

- [ ] **Step 4: Run the full gate**

Run: `make ci`
Expected: PASS — fmt-check, vet, golangci-lint, `go test -race`, govulncheck,
build.

- [ ] **Step 5: Commit**

```bash
git add grafana/ CHANGELOG.md
git commit -m "feat(dashboards): chart read/write latency quantiles and per-node DT

Both families are new in this release and neither had a panel. The latency
quantiles come from the Flux histogram buckets; per-node DT draws on clusters
where collectDT cannot reach port 9101.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage.** Spec §1 staleness → Task 3. §2 error envelope → Task 1. §3
partial failure → Task 4. §4 per-node DT → Task 5. §5 latency histogram → Task 6.
§6 log noise → Task 7. §7 tracing → Tasks 9 and 10, checklist in Task 11. §8
fixtures and mockecs → Tasks 2 and 8. §9 documentation → Tasks 11 and 12. The
spec's `internal/ecs/testdata/flux/` subdirectory is deliberately **not**
followed: `TestFixturesMatchMockecs` enumerates only top-level `.json` files, so
a subdirectory would silently escape the mirror guarantee. Fixtures stay flat,
as `flux_cpu.json` already is.

**Type consistency.** `samples` takes `(rows, mapper, now)` and returns
`(samples, unmapped, stale)` from Task 3 onward; Tasks 5, 6 and 9 use that
signature. `Flux` accretes fields in order — `now` (Task 3), `DTOwnedByDT`
(Task 5), `silent` (Task 7) — and `Registry` is updated in Tasks 5 and 7.
`fluxQuery` accretes `maxAge` (Task 3), `hostTag` and `dtPerNode` (Task 5),
`buckets` (Task 6). `Collect`'s receiver becomes `f Flux` in Task 3 and stays.
`APIError`'s fields and `Permanent()` are defined in Task 1 and consumed by
`fluxFatal` in Task 4 and the test helper in Task 4.

**Known soft spots**, flagged rather than papered over: Task 2's step 5 and
Task 6's step 5 say "update the assertions to match" and "follow the existing
tests' structure" without quoting the final code, because the exact values and
the surrounding test scaffolding are only knowable once the fixtures are on disk.
Task 10's client construction must be read off `buildTargets` in `main.go` rather
than assumed. These are the three places where the implementer must look at the
repo rather than transcribe.
