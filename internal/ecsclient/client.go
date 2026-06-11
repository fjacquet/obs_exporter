// Package ecsclient is the per-cluster Dell ECS / ObjectScale management REST API client.
package ecsclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"

	"github.com/go-resty/resty/v2"
	log "github.com/sirupsen/logrus"
)

// Client is the per-cluster ECS management API abstraction. It is satisfied by the
// live ClusterClient and by Mock (tests). Calls authenticate lazily and decode JSON.
type Client interface {
	// Name returns the configured cluster name (used as the `cluster` label).
	Name() string
	// Get fetches an absolute management API path (e.g. "/dashboard/zones/localzone")
	// and JSON-decodes the body into out. It (re-)authenticates as needed.
	Get(ctx context.Context, path string, out any) error
	// Post sends body as JSON to an absolute management API path and decodes the
	// response into out. It (re-)authenticates as needed.
	Post(ctx context.Context, path string, body, out any) error
	// Close logs out (GET /logout) so the session token is released — ECS caps
	// concurrent tokens per user, so leaking them eventually locks the account out.
	Close() error
}

// Config configures a ClusterClient. HTTPClient is optional (tests inject the
// httptest TLS client); when nil a client honoring InsecureSkipVerify is built.
type Config struct {
	Name               string
	BaseURL            string // e.g. https://ecs01.example.com:4443
	Username           string
	Password           string
	InsecureSkipVerify bool
	HTTPClient         *http.Client
	// Trace logs every management API response body (method, path, status, body)
	// for validating payload shapes against a live cluster. Headers are never
	// logged, so the session token cannot leak. Verbose — debugging only.
	Trace bool
}

// ClusterClient is the live per-cluster ECS management REST client.
type ClusterClient struct {
	cfg   Config
	rc    *resty.Client
	mu    sync.Mutex
	token string
}

// NewClusterClient builds a client. Auth is lazy (on first call).
func NewClusterClient(cfg Config) *ClusterClient {
	rc := resty.New().SetBaseURL(cfg.BaseURL).SetHeader("Accept", "application/json")
	if cfg.HTTPClient != nil {
		rc.SetTransport(cfg.HTTPClient.Transport)
	} else if cfg.InsecureSkipVerify {
		rc.SetTLSClientConfig(&tls.Config{
			InsecureSkipVerify: cfg.InsecureSkipVerify, // operator opt-in for self-signed ECS certs
			MinVersion:         tls.VersionTLS12,
		})
	}
	// Retry on transport errors and 5xx, but never on 4xx (do not retry
	// auth/permission failures). resty passes r == nil on transport/TLS errors,
	// so guard the dereference to avoid a panic.
	rc.SetRetryCount(2).AddRetryCondition(func(r *resty.Response, err error) bool {
		if err != nil {
			return true
		}
		return r != nil && r.StatusCode() >= 500
	})
	if cfg.Trace {
		// Deliberately not resty's SetDebug: that dumps request headers including
		// X-SDS-AUTH-TOKEN. This logs only method/path/status and the body.
		rc.OnAfterResponse(func(_ *resty.Client, r *resty.Response) error {
			if r.Request.URL == cfg.BaseURL+loginPath {
				return nil // login body is uninteresting; the token lives in a header
			}
			log.WithFields(log.Fields{
				"cluster": cfg.Name,
				"method":  r.Request.Method,
				"url":     r.Request.URL,
				"status":  r.StatusCode(),
			}).Infof("API trace:\n%s", r.Body())
			return nil
		})
	}
	return &ClusterClient{cfg: cfg, rc: rc}
}

// Name returns the configured cluster name.
func (c *ClusterClient) Name() string { return c.cfg.Name }

// Get fetches path, authenticating first if needed and re-authenticating once on 401.
func (c *ClusterClient) Get(ctx context.Context, path string, out any) error {
	return c.call(ctx, http.MethodGet, path, nil, out)
}

// Post sends body to path, authenticating first if needed and re-authenticating once on 401.
func (c *ClusterClient) Post(ctx context.Context, path string, body, out any) error {
	return c.call(ctx, http.MethodPost, path, body, out)
}

func (c *ClusterClient) call(ctx context.Context, method, path string, body, out any) error {
	if err := c.ensureToken(ctx); err != nil {
		return err
	}
	resp, err := c.do(ctx, method, path, body, out)
	if err != nil {
		return err
	}
	if resp.StatusCode() == http.StatusUnauthorized {
		// Session token expired (ECS tokens have an idle + absolute lifetime):
		// drop it, log in again, and retry the call once.
		c.clearToken()
		if err := c.ensureToken(ctx); err != nil {
			return err
		}
		resp, err = c.do(ctx, method, path, body, out)
		if err != nil {
			return err
		}
	}
	if resp.StatusCode() >= 300 {
		return fmt.Errorf("%s %s: status %d", method, path, resp.StatusCode())
	}
	return nil
}

func (c *ClusterClient) do(ctx context.Context, method, path string, body, out any) (*resty.Response, error) {
	// ForceContentType: decode the body as JSON even when the appliance reports a
	// generic content type (some ECS endpoints answer text/plain for JSON bodies).
	r := c.rc.R().SetContext(ctx).
		SetHeader("X-SDS-AUTH-TOKEN", c.currentToken()).
		SetResult(out).
		ForceContentType("application/json")
	if body != nil {
		r = r.SetBody(body)
	}
	return r.Execute(method, path)
}

func (c *ClusterClient) currentToken() string { c.mu.Lock(); defer c.mu.Unlock(); return c.token }
func (c *ClusterClient) clearToken()          { c.mu.Lock(); c.token = ""; c.mu.Unlock() }
