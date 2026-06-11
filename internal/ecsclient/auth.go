package ecsclient

import (
	"context"
	"fmt"
)

// ECS management auth endpoints (unchanged through OBS 4.1.0.0): a basic-auth GET
// to /login returns the session token in the X-SDS-AUTH-TOKEN response header, and
// GET /logout releases it.
const (
	loginPath  = "/login"
	logoutPath = "/logout"
)

// ensureToken logs in if no token is cached, capturing X-SDS-AUTH-TOKEN.
func (c *ClusterClient) ensureToken(ctx context.Context) error {
	if c.currentToken() != "" {
		return nil
	}
	resp, err := c.rc.R().SetContext(ctx).
		SetBasicAuth(c.cfg.Username, c.cfg.Password).
		Get(loginPath)
	if err != nil {
		return fmt.Errorf("login GET: %w", err)
	}
	if resp.StatusCode() >= 300 {
		return fmt.Errorf("login GET: status %d", resp.StatusCode())
	}
	tok := resp.Header().Get("X-SDS-AUTH-TOKEN")
	if tok == "" {
		return fmt.Errorf("login GET: no X-SDS-AUTH-TOKEN in response")
	}
	c.mu.Lock()
	c.token = tok
	c.mu.Unlock()
	return nil
}

// Close logs out (best effort) and is safe to call with no active session. ECS
// limits the number of concurrent session tokens per user, so every client must
// release its token on shutdown.
func (c *ClusterClient) Close() error {
	if c.currentToken() == "" {
		return nil
	}
	_, _ = c.rc.R().SetHeader("X-SDS-AUTH-TOKEN", c.currentToken()).Get(logoutPath)
	c.clearToken()
	return nil
}
