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
