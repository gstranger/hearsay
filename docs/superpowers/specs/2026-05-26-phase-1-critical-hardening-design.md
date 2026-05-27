# Design: Phase 1 Critical Hardening

**Date:** 2026-05-26
**Scope:** Fix the 5 most critical production readiness gaps before any network exposure.

## Motivation

The PRODUCTION_READINESS.md assessment identified critical gaps that make `hearsay` unsafe for any network exposure. This design addresses the top 5: REST API auth, expired claim cleanup, graceful shutdown, input validation, and request logging.

## Changes

### 1. Auth on REST API (`serve`)

**Problem:** REST server on `:8080` has zero authentication.

**Solution:**
- Add `--auth-token` flag to `hearsay serve`
- When set, require `Authorization: Bearer <token>` or `X-Api-Key: <token>` on all mutating endpoints
- Read-only endpoints (`GET /health`, `GET /claims`, `GET /check`, `GET /mailbox`) remain open
- Extract auth check from `internal/a2a/auth.go` into shared middleware

**Files:**
- `cmd/hearsay/main.go` — add `--auth-token` flag, wire middleware
- `internal/server/server.go` — apply auth middleware to mutating handlers
- `internal/a2a/auth.go` — extract shared auth check function

### 2. Implement `ReleaseExpired`

**Problem:** SQLite and Postgres `ReleaseExpired` are no-ops. DB bloats forever.

**Solution:**
- SQLite: DELETE expired claim messages + their release messages
- Postgres: Same with `$N` placeholders
- Add background sweeper goroutine in `cmdServe` (every 60s)

**Files:**
- `internal/sqlite/sqlite.go` — implement `ReleaseExpired`
- `internal/postgres/postgres.go` — implement `ReleaseExpired`
- `cmd/hearsay/main.go` — add sweeper goroutine

### 3. Graceful Shutdown

**Problem:** `SIGTERM` orphans in-flight claims.

**Solution:**
- Wrap REST and A2A servers in `http.Server`
- `signal.Notify` for `SIGINT`, `SIGTERM`
- `context.WithTimeout(10s)` for shutdown

**Files:**
- `cmd/hearsay/main.go` — replace `http.ListenAndServe` with `http.Server` + graceful shutdown

### 4. Input Validation

**Problem:** Arbitrary string lengths accepted.

**Solution:**
- Max lengths: `agent_id` 128, `resource_uri` 2048, `intent` 512, `namespace` 64
- Validate at server handler entry points
- Return `400 Bad Request` with specific field error

**Files:**
- `pkg/hearsay/validation.go` — new validation helpers
- `internal/server/server.go` — validate inputs in handlers

### 5. Request Logging Middleware

**Problem:** No visibility into requests.

**Solution:**
- HTTP middleware: method, path, agent_id, latency, status code
- `--log-format=json` flag for structured JSON output
- Default: plain text

**Files:**
- `internal/server/middleware.go` — new logging middleware
- `cmd/hearsay/main.go` — add `--log-format` flag

## Out of Scope

- TLS/HTTPS (use reverse proxy)
- Rate limiting
- Audit log
- Process death detection
- Metrics / Prometheus
- Database migration framework

## Verification

- `go test ./...` passes
- `go build ./cmd/hearsay` succeeds
- Manual test: `hearsay serve --auth-token secret`, verify unauthorized requests rejected
- Manual test: create expired claim, verify sweeper removes it
