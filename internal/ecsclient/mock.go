package ecsclient

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
)

// Mock is a canned-response Client for tests and the demo. GET responses are
// keyed by path; POST responses by path as well (the body is ignored).
//
// Collectors may issue requests concurrently, so the call log is guarded; the
// response maps are read-only once built and are not.
type Mock struct {
	ClusterName string
	// Responses maps an API path (including any query string used by the caller)
	// to its raw JSON response.
	Responses map[string]string
	// Errs forces an error for a path, taking precedence over Responses.
	Errs   map[string]error
	Closed bool

	mu    sync.Mutex
	calls []string
}

// Name returns the mock's cluster name.
func (m *Mock) Name() string { return m.ClusterName }

// Get decodes the canned JSON for path into out.
func (m *Mock) Get(_ context.Context, path string, out any) error {
	return m.respond(path, out)
}

// Post decodes the canned JSON for path into out, ignoring the request body.
func (m *Mock) Post(_ context.Context, path string, _, out any) error {
	return m.respond(path, out)
}

// Close records that the session was released.
func (m *Mock) Close() error {
	m.Closed = true
	return nil
}

// Calls returns a copy of the paths requested so far. Order is meaningless for
// concurrent collectors; use it to assert that a request was or was not made.
func (m *Mock) Calls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.calls)
}

func (m *Mock) respond(path string, out any) error {
	m.mu.Lock()
	m.calls = append(m.calls, path)
	m.mu.Unlock()

	if err, ok := m.Errs[path]; ok {
		return err
	}
	body, ok := m.Responses[path]
	if !ok {
		return fmt.Errorf("mock: no response for %s", path)
	}
	return json.Unmarshal([]byte(body), out)
}
