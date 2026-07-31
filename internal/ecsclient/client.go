// Package ecsclient is the per-cluster Dell ECS / ObjectScale management REST API client.
package ecsclient

import (
	"context"
	"crypto/tls"
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

// MaxConcurrentRequests is the widest fan-out any collector may issue against a
// single cluster. It sizes this client's idle-connection pool, so a collector
// that exceeds it silently trades pooled connections for TLS handshakes — keep
// the two in step by deriving the collector's limit from this constant.
const MaxConcurrentRequests = 8

// ClusterClient is the live per-cluster ECS management REST client.
type ClusterClient struct {
	cfg Config
	rc  *resty.Client
	// mu guards token only, and is never held across a network call.
	mu    sync.Mutex
	token string
	// loginMu serialises the login round-trip itself. See ensureToken.
	loginMu sync.Mutex
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
	// resty's default transport keeps GOMAXPROCS+1 idle connections per host —
	// three on a two-CPU container. Collectors fan out up to MaxConcurrentRequests
	// GETs at one cluster, and every connection past the idle limit is closed
	// instead of pooled, re-paying a TLS handshake on the next cycle. Size the
	// pool to the widest fan-out we issue. Skipped when the caller supplied its
	// own client: that transport is theirs to tune.
	if cfg.HTTPClient == nil {
		if tr, ok := rc.GetClient().Transport.(*http.Transport); ok {
			tr.MaxIdleConnsPerHost = MaxConcurrentRequests
		}
	}
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
	if cfg.Trace {
		// Deliberately not resty's SetDebug: that dumps request headers including
		// X-SDS-AUTH-TOKEN. This logs only method/path/status and the body.
		rc.OnAfterResponse(func(_ *resty.Client, r *resty.Response) error {
			if r.Request.URL == cfg.BaseURL+loginPath {
				return nil // login body is uninteresting; the token lives in a header
			}
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
		return parseAPIError(method, path, resp.StatusCode(), resp.Body())
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
