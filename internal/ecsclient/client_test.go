package ecsclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
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
