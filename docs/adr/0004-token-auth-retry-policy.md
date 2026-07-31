# Token auth & retry policy

## Status

Accepted (2026-06-10). **Amended (2026-07-31), live-confirmed:** the retry
condition now reads the response body, not the status class alone — see
Decision, below, and the [2026-07-31 validation
design](../superpowers/specs/2026-07-31-flux-live-validation-design.md) for
the analysis.

## Context

ECS management auth (unchanged through OBS 4.1.0.0) is a basic-auth `GET /login`
returning a session token in the `X-SDS-AUTH-TOKEN` response header; `GET /logout`
releases it. ECS **caps concurrent tokens per user** (~100): a client that leaks
tokens eventually locks the monitoring account out. Tokens also expire (idle +
absolute lifetime), so long-running clients must re-authenticate.

ECS also overloads HTTP 500: a permission refusal for an account whose roles
do not cover an endpoint carries the same status as a transient failure,
distinguished only by the response body. Confirmed live 2026-07-31 against a
Flux query issued with insufficient roles:

```json
{"code":6401,"description":"Insufficient permissions","retryable":false}
```

The status code alone cannot tell that refusal from a genuinely transient 5xx
or a malformed request the server rejects with its own 500 — only the body
can.

## Decision

- **Lazy login**: the first API call authenticates; the token is cached on the
  client (mutex-guarded).
- **Re-login once on 401**: a 401 means the session expired — clear the token,
  log in again, retry the call once. No loops.
- **Transport retry excludes 4xx** (family rule, ppdd ADR-0004): resty retries
  twice on transport errors and 5xx only. Bad credentials fail immediately instead
  of hammering `/login` (which counts against the token cap and can trip lockout
  policies).
- **A 5xx is not automatically retryable** (added 2026-07-31, live-confirmed):
  the retry condition decodes the body before deciding. A 5xx whose envelope
  carries `retryable:false`, or whose `code` is `6401`
  (`CodeInsufficientPermissions` — ECS's own permission-refusal code), is
  permanent: no amount of retrying changes an account's roles, so the request
  fails on the first attempt instead of costing two wasted round-trips. A body
  that does not decode as that envelope — including one that is not JSON at
  all — makes no claim either way and retries exactly as before this rule
  existed: on status class alone.
- **Logout on shutdown and on config-reload swaps**: every client `Close()` hits
  `/logout` best-effort, so tokens are returned even across hot reloads.
- TLS minimum 1.2; `insecureSkipVerify` is a per-cluster operator opt-in for
  self-signed certificates.

## Consequences

- The exporter holds exactly one token per cluster at steady state.
- An expired token costs one extra round-trip on the first call after expiry.
- A permission refusal now costs exactly one request per query, not three: an
  account missing `SYSTEM_MONITOR`/`SYSTEM_ADMIN` against the Flux endpoint
  used to retry twice against an outcome that could never change, then log a
  bare `status 500` with no cause. It now fails immediately and names the
  refusal (`code 6401`, `Insufficient permissions`).
