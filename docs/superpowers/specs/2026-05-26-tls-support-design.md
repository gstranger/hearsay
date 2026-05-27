# Design: TLS/HTTPS Support

**Date:** 2026-05-26
**Scope:** Add TLS/HTTPS to both REST and A2A servers with auto-generated self-signed certs and user-provided cert support.

## Motivation

The PRODUCTION_READINESS.md identified "No TLS / HTTPS configuration flags" as a critical gap. Credentials and resource URIs currently fly in plaintext. This design adds TLS with zero-config auto-generation for local use and user-provided certs for production.

## Changes

### 1. CLI Flags

Add to `hearsay serve`:

| Flag | Default | Description |
|---|---|---|
| `--tls-cert` | `""` | Path to TLS certificate file |
| `--tls-key` | `""` | Path to TLS private key file |
| `--tls-auto` | `false` | Auto-generate self-signed TLS certificate |

### 2. Auto-Generated Self-Signed Certs

When `--tls-auto` is set (or when `--tls-cert`/`--tls-key` are not provided but TLS is implied):

1. Check for existing `.hearsay.crt` and `.hearsay.key` in working directory
2. If not found, generate a new 2048-bit RSA self-signed cert valid for 365 days
3. Save to `.hearsay.crt` and `.hearsay.key`
4. Log a warning that the cert is self-signed

**Cert details:**
- CN: `hearsay-local`
- SANs: `localhost`, `127.0.0.1`, `::1`
- Valid: 365 days
- Key: RSA 2048-bit

### 3. TLS Application

- Both REST server (`:8080`) and A2A server (`:8081`) use TLS when configured
- `http.Server` with `ListenAndServeTLS` instead of `ListenAndServe`
- Graceful shutdown already handles TLS (from Phase 1)

### 4. New File

- `internal/server/tls.go` — Self-signed cert generation helper
- `internal/server/tls_test.go` — Tests for cert generation

## Out of Scope

- HTTP→HTTPS redirect (clients should use HTTPS directly)
- Let's Encrypt / ACME auto-renewal
- mTLS (client certificates)
- HSTS headers

## Verification

- `go test ./...` passes
- `go build ./cmd/hearsay` succeeds
- `./hearsay serve --tls-auto` generates `.hearsay.crt` and `.hearsay.key`
- `curl -k https://localhost:8080/health` returns `{"status":"ok"}`
