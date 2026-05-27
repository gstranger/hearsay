# Design: Per-Agent Rate Limiting

**Date:** 2026-05-26
**Scope:** Add per-agent_id rate limiting to mutating REST API endpoints.

## Motivation

Currently there's no protection against a noisy agent flooding the server. Rate limiting prevents one agent from starving others and provides basic abuse protection.

## Design

### CLI Flags

| Flag | Default | Description |
|---|---|---|
| `--rate-limit` | `0` | Max requests per second per agent (0 = unlimited) |
| `--rate-burst` | `10` | Max burst size per agent |

### Algorithm

Sliding window counter. Each agent gets a deque of request timestamps. On each request:
1. Drop timestamps older than the window (1s)
2. If remaining count >= `rate-limit`, return 429
3. Otherwise, append current timestamp and proceed

This is O(1) amortized because we only evict at the front.

### Scope

- Applied to mutating POST endpoints only (read-only GET endpoints are unlimited)
- Agent identified by `X-Agent-ID` header; if missing, uses `"unknown"`
- Applied to REST API only (A2A already has its own auth/throttling concern)

### Response

When rate limited:
```
HTTP 429 Too Many Requests
Retry-After: 1
X-RateLimit-Limit: 100
X-RateLimit-Remaining: 0
X-RateLimit-Reset: <unix timestamp>
```

### Files

| File | Purpose |
|---|---|
| `internal/server/ratelimit.go` | **NEW** — RateLimiter struct and middleware |
| `internal/server/ratelimit_test.go` | **NEW** — Tests |
| `internal/server/server.go` | Wire rate limiter into handler chain |
| `cmd/hearsay/main.go` | Add `--rate-limit`, `--rate-burst` flags |
| `README.md` | Document flags |

### Out of Scope
- IP-based rate limiting
- Distributed rate limiting (across multiple server instances)
- A2A server rate limiting

## Verification
- `go test ./...` passes
- 2 requests within limit → 200
- N+1 request over limit → 429
- After window passes → 200 again