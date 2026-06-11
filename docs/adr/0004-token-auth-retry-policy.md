# Token auth & retry policy

## Status

Accepted (2026-06-10)

## Context

ECS management auth (unchanged through OBS 4.1.0.0) is a basic-auth `GET /login`
returning a session token in the `X-SDS-AUTH-TOKEN` response header; `GET /logout`
releases it. ECS **caps concurrent tokens per user** (~100): a client that leaks
tokens eventually locks the monitoring account out. Tokens also expire (idle +
absolute lifetime), so long-running clients must re-authenticate.

## Decision

- **Lazy login**: the first API call authenticates; the token is cached on the
  client (mutex-guarded).
- **Re-login once on 401**: a 401 means the session expired — clear the token,
  log in again, retry the call once. No loops.
- **Transport retry excludes 4xx** (family rule, ppdd ADR-0004): resty retries
  twice on transport errors and 5xx only. Bad credentials fail immediately instead
  of hammering `/login` (which counts against the token cap and can trip lockout
  policies).
- **Logout on shutdown and on config-reload swaps**: every client `Close()` hits
  `/logout` best-effort, so tokens are returned even across hot reloads.
- TLS minimum 1.2; `insecureSkipVerify` is a per-cluster operator opt-in for
  self-signed certificates.

## Consequences

- The exporter holds exactly one token per cluster at steady state.
- An expired token costs one extra round-trip on the first call after expiry.
