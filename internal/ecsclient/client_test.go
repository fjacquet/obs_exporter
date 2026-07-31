package ecsclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	log "github.com/sirupsen/logrus"
)

// newTestServer builds a TLS httptest server simulating the ECS management API
// auth flow and a /dashboard/zones/localzone endpoint.
func newTestServer(t *testing.T, hooks *serverHooks) (*httptest.Server, *ClusterClient) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hooks.logins, 1)
		user, pass, ok := r.BasicAuth()
		if !ok || user != "monitor" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-SDS-AUTH-TOKEN", hooks.tokenToIssue())
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hooks.logouts, 1)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/dashboard/zones/localzone", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-SDS-AUTH-TOKEN") != hooks.validToken() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "vdc1"})
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	c := NewClusterClient(Config{
		Name:       "test",
		BaseURL:    srv.URL,
		Username:   "monitor",
		Password:   "secret",
		HTTPClient: srv.Client(),
	})
	return srv, c
}

type serverHooks struct {
	logins, logouts int64
	issued          atomic.Int64 // bumping invalidates previously issued tokens
}

func (h *serverHooks) tokenToIssue() string { return h.validToken() }
func (h *serverHooks) validToken() string {
	if h.issued.Load() == 0 {
		h.issued.Store(1)
	}
	return "tok-" + string(rune('0'+h.issued.Load()))
}

func TestLoginAndGet(t *testing.T) {
	hooks := &serverHooks{}
	_, c := newTestServer(t, hooks)
	var out struct {
		Name string `json:"name"`
	}
	if err := c.Get(context.Background(), "/dashboard/zones/localzone", &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "vdc1" {
		t.Errorf("name = %q", out.Name)
	}
	if got := atomic.LoadInt64(&hooks.logins); got != 1 {
		t.Errorf("logins = %d, want 1", got)
	}
	// Second call reuses the cached token — no new login.
	if err := c.Get(context.Background(), "/dashboard/zones/localzone", &out); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&hooks.logins); got != 1 {
		t.Errorf("logins after 2nd call = %d, want 1", got)
	}
}

func TestReloginOn401(t *testing.T) {
	hooks := &serverHooks{}
	_, c := newTestServer(t, hooks)
	var out map[string]string
	if err := c.Get(context.Background(), "/dashboard/zones/localzone", &out); err != nil {
		t.Fatal(err)
	}
	// Invalidate the session server-side: the next Get must re-login once and succeed.
	hooks.issued.Add(1)
	if err := c.Get(context.Background(), "/dashboard/zones/localzone", &out); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&hooks.logins); got != 2 {
		t.Errorf("logins = %d, want 2", got)
	}
}

func TestBadCredentialsNoRetry(t *testing.T) {
	hooks := &serverHooks{}
	srv, _ := newTestServer(t, hooks)
	c := NewClusterClient(Config{
		Name: "test", BaseURL: srv.URL,
		Username: "monitor", Password: "wrong",
		HTTPClient: srv.Client(),
	})
	var out map[string]string
	if err := c.Get(context.Background(), "/dashboard/zones/localzone", &out); err == nil {
		t.Fatal("expected auth error")
	}
	// 401 is a 4xx: resty's retry condition must not have retried it.
	if got := atomic.LoadInt64(&hooks.logins); got != 1 {
		t.Errorf("logins = %d, want 1 (no retry on 4xx)", got)
	}
}

func TestTraceDoesNotBreakCalls(t *testing.T) {
	hooks := &serverHooks{}
	srv, _ := newTestServer(t, hooks)
	c := NewClusterClient(Config{
		Name: "test", BaseURL: srv.URL,
		Username: "monitor", Password: "secret",
		HTTPClient: srv.Client(),
		Trace:      true,
	})
	var out struct {
		Name string `json:"name"`
	}
	// Exercises the OnAfterResponse trace hook on both the login (skipped) and
	// the data call (logged); the decoded result must be unaffected.
	if err := c.Get(context.Background(), "/dashboard/zones/localzone", &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "vdc1" {
		t.Errorf("name = %q", out.Name)
	}
}

func TestCloseLogsOut(t *testing.T) {
	hooks := &serverHooks{}
	_, c := newTestServer(t, hooks)
	var out map[string]string
	if err := c.Get(context.Background(), "/dashboard/zones/localzone", &out); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&hooks.logouts); got != 1 {
		t.Errorf("logouts = %d, want 1", got)
	}
	// Close with no session is a no-op.
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&hooks.logouts); got != 1 {
		t.Errorf("logouts after idempotent close = %d, want 1", got)
	}
}

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

// entryCaptureHook records the fully formatted text of every log entry fired
// while installed -- not just its structured fields -- so a test can assert
// on what would actually reach the log stream. e.String() runs the entry
// through the logger's own formatter, the same step a real log write takes.
type entryCaptureHook struct {
	lines []string
	// fields mirrors each entry's Data, for tests that want to assert on one
	// field's value directly rather than substring-search formatted text.
	fields []log.Fields
}

func (h *entryCaptureHook) Levels() []log.Level { return log.AllLevels }

func (h *entryCaptureHook) Fire(e *log.Entry) error {
	line, err := e.String()
	if err != nil {
		return err
	}
	h.lines = append(h.lines, line)
	data := make(log.Fields, len(e.Data))
	for k, v := range e.Data {
		data[k] = v
	}
	h.fields = append(h.fields, data)
	return nil
}

// installLogHook installs h on the shared standard logger for the duration of
// the test and restores the prior level and hooks afterward. logrus never
// fires a hook for a level the logger isn't currently emitting (default
// Info), so the level is raised for the test; ReplaceHooks both installs and
// returns the displaced value, which is how the prior hooks -- not just this
// one -- are restored exactly rather than wiped.
func installLogHook(t *testing.T, h log.Hook) {
	t.Helper()
	prevLevel := log.GetLevel()
	log.SetLevel(log.DebugLevel)
	t.Cleanup(func() { log.SetLevel(prevLevel) })

	prevHooks := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	log.AddHook(h)
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(prevHooks) })
}

// TestTraceCarriesFluxQuery proves the trace hook attributes a Flux-shaped
// POST to its query. Without this, --trace logs method/url/status for every
// Flux call, and every Flux call is a POST to the same path: ten
// indistinguishable trace blocks, unusable for validating payload shapes
// against a live cluster.
func TestTraceCarriesFluxQuery(t *testing.T) {
	hook := &entryCaptureHook{}
	installLogHook(t, hook)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.Header().Set("X-SDS-AUTH-TOKEN", "tok-flux-test")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Series": []any{}})
	}))
	defer srv.Close()

	c := NewClusterClient(Config{
		Name: "test", BaseURL: srv.URL,
		Username: "monitor", Password: "secret",
		HTTPClient: srv.Client(),
		Trace:      true,
	})

	// Shaped exactly as Collect calls Post (internal/ecs/flux.go): a
	// map[string]string body carrying the Flux script under "query" -- the
	// only shape the trace hook's type assertion recognizes.
	const query = `from(bucket:"monitoring_op") |> range(start: -15m) |> filter(fn: (r) => r._measurement == "cpu")`
	var out map[string]any
	if err := c.Post(t.Context(), "/flux/api/external/v2/query", map[string]string{"query": query}, &out); err != nil {
		t.Fatal(err)
	}

	for _, fields := range hook.fields {
		if fields["query"] == query {
			return
		}
	}
	t.Fatalf("no traced entry carried the Flux query %q; captured fields: %+v", query, hook.fields)
}

// TestTraceDoesNotLeakToken is the one protection worth a permanent test
// rather than a code comment: the trace hook deliberately does not use
// resty's SetDebug, which would dump request headers including
// X-SDS-AUTH-TOKEN. This proves it end to end, against the entry's actual
// formatted log output rather than just its known fields, so a change that
// slipped the token in through the message string (e.g. Infof("...%v",
// r.Request)) would also be caught.
func TestTraceDoesNotLeakToken(t *testing.T) {
	hook := &entryCaptureHook{}
	installLogHook(t, hook)

	const token = "tok-must-never-appear-in-any-log-line"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.Header().Set("X-SDS-AUTH-TOKEN", token)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "vdc1"})
	}))
	defer srv.Close()

	c := NewClusterClient(Config{
		Name: "test", BaseURL: srv.URL,
		Username: "monitor", Password: "secret",
		HTTPClient: srv.Client(),
		Trace:      true,
	})

	// One call does both: logs in (skipped by the trace hook, per the "login
	// body is uninteresting" branch) and issues a traced request that carries
	// the token in its X-SDS-AUTH-TOKEN header.
	var out struct {
		Name string `json:"name"`
	}
	if err := c.Get(t.Context(), "/dashboard/zones/localzone", &out); err != nil {
		t.Fatal(err)
	}
	if len(hook.lines) == 0 {
		t.Fatal("no traced entries were captured; the token-leak assertion below would pass vacuously")
	}
	for _, line := range hook.lines {
		if strings.Contains(line, token) {
			t.Errorf("traced log output leaked the auth token:\n%s", line)
		}
	}
}
