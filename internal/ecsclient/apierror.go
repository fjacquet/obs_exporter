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
