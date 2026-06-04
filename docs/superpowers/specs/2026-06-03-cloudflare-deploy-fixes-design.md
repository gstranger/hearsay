# Cloudflare deploy fixes + managed-provider WASM support

**Status:** Draft
**Date:** 2026-06-03
**Author:** brainstorm with Claude

## Problem

A clean `git clone` of this repo cannot be deployed to Cloudflare Workers today. Three blockers:

1. **No build step.** `wrangler.toml` has no `[build]` block, and `hearsay.wasm` + `wasm_exec.js` are `.gitignore`d. `wrangler deploy` fails because the main module isn't there.
2. **Account-specific D1 ID.** `database_id` in `wrangler.toml` is a real UUID from a specific Cloudflare account. Nobody else's deploy will target the right database.
3. **Migrations aren't applied.** `migrations/0001_init.sql` exists but nothing in the current config runs it. First request after deploy fails because tables don't exist.

The README claims the Deploy-to-Cloudflare button works and references a Durable Object binding that's been removed from `wrangler.toml` — doc drift on top of the three blockers.

Separately: the WASM target only supports D1. The existing `internal/managed` HTTP provider has `//go:build !wasm` at the top, so even though it would work fine in WASM (uses only stdlib `net/http`), it's excluded.

## Goals

- A clean clone can be deployed to Cloudflare Workers in three documented commands.
- Native build path (`go build ./cmd/hearsay`) is untouched. Self-hosters keep using SQLite, Postgres, or the managed provider against a native binary on any host.
- The WASM target supports a second storage option: managed-remote, for users who already run hearsay natively and want a Cloudflare edge proxying to it.
- Docs reflect reality. No more Durable Object claims; no more "Deploy button just works" overpromises.

## Non-goals

- **TinyGo migration.** Current WASM is 6.2 MB, paid-plan-only. Shrinking to fit the 3 MB free tier is a separate spec.
- **Postgres-over-HTTP / Hyperdrive provider.** Would need a new provider implementation (pgx doesn't run in WASM). Separate spec.
- **GitHub Action for CI deploys.** Useful, optional, separate scope.
- **Re-introducing Durable Objects.** The WASM sweeper is a no-op stub today, which is the right call until namespace-scoped background work is actually needed on Workers.

## Design

### `wrangler.toml` — restore build, placeholder ID

```toml
#:schema node
name = "hearsay"
main = "worker.js"
compatibility_date = "2026-06-01"
compatibility_flags = ["nodejs_compat"]

# D1 database for coordination state (claims, mailbox, A2A tasks, audit log).
# Run `wrangler d1 create hearsay-db` and paste the returned id below.
[[d1_databases]]
binding = "HEARSAY_D1"
database_name = "hearsay-db"
database_id = "REPLACE_WITH_YOUR_D1_ID"
migrations_dir = "migrations"

# Build: apply D1 migrations, compile Go to WASM, stage wasm_exec.js.
# Requires Go 1.24+ and `wrangler login` (or CLOUDFLARE_API_TOKEN in CI).
[build]
command = """
npx wrangler d1 migrations apply HEARSAY_D1 --remote && \
GOOS=js GOARCH=wasm go build -o hearsay.wasm ./cmd/hearsay && \
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" .
"""

[vars]
HEARSAY_VERSION = "1.0.0"
```

Choices:

- **`migrations_dir = "migrations"`** points wrangler at `migrations/0001_init.sql`. Wrangler tracks applied migrations in D1 — idempotent across deploys.
- **Build runs migrations first.** Fail-fast: a broken migration aborts the deploy before we ship a worker that will fault on every request.
- **Placeholder is screaming-case (`REPLACE_WITH_YOUR_D1_ID`)** so `wrangler deploy` fails with a recognizable string if anyone forgets to paste their ID. Better than a UUID-shaped placeholder that silently targets a nonexistent DB.
- **`nodejs_compat` flag stays** — needed for `wasm_exec.js` shims (`Buffer`, `process`).
- **Removed:** `[build.upload]` (legacy format) and `[env.production]` (was a duplicate of the default).

### Managed provider on WASM

**`internal/managed/managed.go`** — drop the `//go:build !wasm` build tag. The provider uses only stdlib `net/http.Client` with a `Timeout`, which Go's WASM target maps transparently to the browser `fetch` API. The build tag was defensive, not load-bearing.

Verification: `GOOS=js GOARCH=wasm go build ./internal/managed` must succeed before this change is considered done. If something in the file later imports a WASM-incompatible API (e.g., a custom Transport with TCP keepalive), narrow the tag to that function instead of the whole file.

### Provider selection in `cmd/hearsay/main_wasm.go`

Today the WASM entrypoint always uses D1:

```go
globalProvider = d1.New(d1Binding)
```

Replace with a two-layer design so the selection logic is testable on native (where `syscall/js` isn't available).

**`cmd/hearsay/provider_kind.go`** (no build tag — usable from native tests):

```go
package main

// ProviderKind names which storage backend the WASM runtime should use.
type ProviderKind int

const (
    ProviderNone ProviderKind = iota
    ProviderManaged
    ProviderD1
)

// pickProviderKind picks the backend given which env inputs are populated.
// HEARSAY_MANAGED_URL wins when set; otherwise the D1 binding is used;
// otherwise ProviderNone (caller should 500).
func pickProviderKind(managedURL string, hasD1Binding bool) ProviderKind {
    if managedURL != "" {
        return ProviderManaged
    }
    if hasD1Binding {
        return ProviderD1
    }
    return ProviderNone
}
```

**`cmd/hearsay/main_wasm.go`** translates the kind into a real provider:

```go
managedURL   := safeString(args[2]) // env.HEARSAY_MANAGED_URL
managedToken := safeString(args[3]) // env.HEARSAY_MANAGED_TOKEN

switch pickProviderKind(managedURL, d1Binding.Truthy()) {
case ProviderManaged:
    globalProvider = managed.New(managedURL, managedToken)
case ProviderD1:
    globalProvider = d1.New(d1Binding)
default:
    return newResponse(500, `{"error":"no provider configured: set HEARSAY_D1 binding or HEARSAY_MANAGED_URL"}`)
}
```

`safeString` returns `""` if the arg is `Undefined` or `Null`. Avoids panic when the env var isn't set.

**Selection rule (explicit):** if both D1 binding and `HEARSAY_MANAGED_URL` are configured, managed wins. D1 binding is silently ignored. This is the explicit opt-in case — the user added the env var deliberately. Documented in `docs/cloudflare-deploy.md` so behavior matches expectations.

**Edge cases:**
- Neither configured → 500 with the explicit error above. Better than a nil-pointer crash on first request.
- Managed endpoint unreachable → existing managed provider returns the HTTP error; surfaces as a normal hearsay error response.

### `worker.js` — pass the new env vars through

```js
return globalThis.handleRequest(
  request,
  env.HEARSAY_D1,
  env.HEARSAY_MANAGED_URL,
  env.HEARSAY_MANAGED_TOKEN,
);
```

Argument order is now load-bearing. Add a one-line comment in `main_wasm.go` documenting the contract: `args[0]=request, args[1]=d1Binding, args[2]=managedURL, args[3]=managedToken`.

### Tests

- `cmd/hearsay/provider_kind_test.go` (no build tag — runs on native). The wasm entrypoint imports `pickProviderKind` from the same package and translates the result. Test arms:
  - `TestPickProviderKind_ManagedWinsWhenSet`
  - `TestPickProviderKind_FallsBackToD1`
  - `TestPickProviderKind_ReturnsNoneWhenNeitherConfigured`
  - `TestPickProviderKind_ManagedWinsEvenWhenD1AlsoSet`
- `internal/managed` existing tests must still pass with the build tag dropped.
- `GOOS=js GOARCH=wasm go build ./...` must succeed across the whole tree.

### Documentation

**`README.md`** — replace the Architecture + Deploy sections.

Architecture diagram shows two runtime targets sharing one source tree, with different provider sets per target:

```
Native target          WASM (Cloudflare Workers) target
SQLite                 D1
PostgreSQL             Managed (HTTP to remote hearsay)
Managed
```

Add a runtime feature matrix surfacing what works where:

| Feature | Native | Workers |
|---|---|---|
| REST API | ✓ | ✓ |
| A2A JSON-RPC (`tasks/send`) | ✓ | ✓ |
| A2A SSE (`tasks/sendSubscribe`) | ✓ | ✗ |
| Background sweeper | ✓ | ✗ (stub) |
| TLS | ✓ | edge |

Split deploy options into two clearly-labeled subsections:

- **Self-host the native binary** — existing SQLite/Postgres content, retained.
- **Deploy to Cloudflare Workers** — short pointer to `docs/cloudflare-deploy.md` plus the requirements line: paid Workers plan (WASM is ~6 MB), Go 1.24+ locally or in CI, wrangler CLI logged in.

**`docs/cloudflare-deploy.md`** — rewrite with these sections in order:

1. **What you get** — edge endpoint, D1 or managed-remote storage, no SSE.
2. **Prerequisites** — paid Workers plan, Go 1.24+, wrangler logged in (or `CLOUDFLARE_API_TOKEN` set).
3. **Default path: D1 storage** — three commands:
   ```bash
   wrangler d1 create hearsay-db          # copy the returned id
   # paste id into wrangler.toml database_id
   wrangler deploy
   ```
   Note the `[build]` step runs migrations + WASM compile automatically.
4. **Alternative: managed-remote storage** — for users who already run hearsay natively and want a CF edge in front of it:
   ```bash
   # In wrangler.toml: comment out the [[d1_databases]] block,
   # add HEARSAY_MANAGED_URL under [vars].
   wrangler secret put HEARSAY_MANAGED_TOKEN
   wrangler deploy
   ```
   Explicit priority rule: *if both D1 and `HEARSAY_MANAGED_URL` are configured, managed wins.*
5. **What doesn't work yet** — Postgres-via-Hyperdrive (would need a new provider), Durable Object sweeper (WASM sweeper is a no-op stub), SSE streaming on `tasks/sendSubscribe`.
6. **Verifying the deploy** — agent card curl + claim round-trip curl. Note: cold start may return 503 with `Retry-After: 2`.
7. **Deploy-to-Cloudflare button** — honest status: should work since CF's build environment has Go, but not re-validated after this reshuffle. Manual `wrangler deploy` is the supported path.

Also update the "Run your own" file table in the README — drop the DO binding row, keep `worker.js`, `wrangler.toml`, `migrations/`, `main_wasm.go`.

## Verification

| Check | Command | Pass criterion |
|---|---|---|
| Native build | `go build ./cmd/hearsay` | Binary produced |
| Full test suite | `go test ./...` | All pass (incl. existing WASM-noStreaming + sendSubscribe-rejection tests) |
| Race detector | `go test -race ./...` | Clean |
| WASM cross-compile | `GOOS=js GOARCH=wasm go build ./...` | All packages, including `./internal/managed`, compile |
| Provider selection unit test | `go test ./cmd/hearsay -run TestPickProviderKind` | All four arms pass |
| Local Worker smoke (manual) | `wrangler dev` against throwaway D1 | `/.well-known/agent.json` returns card with `streaming: false`; POST `/ns/test/claim` round-trips |

The Worker smoke step requires `wrangler` auth and is documented as a manual check, not wired into CI.

## Risks

1. **`!wasm` tag on managed provider was load-bearing.** Low probability — the file uses only stdlib `net/http` with no custom transport — but if the cross-compile fails, narrow the tag to the offending function rather than reverting wholesale.
2. **`args[2]` / `args[3]` is a contract change** for any direct caller of the WASM `handleRequest`. Only caller in this repo is `worker.js`, updated in the same change. Documented inline.
3. **Worker size creep.** Currently 6.2 MB, paid-plan cap is 10 MB. Adding managed costs a few KB at most. Doc note: `wc -c hearsay.wasm` to monitor.
4. **Documentation drift.** README touches a lot of prose. Mitigation: before commit, grep the doc for `Durable`, `auto-provision`, `free plan` — fix any survivors.
5. **`wrangler dev` slowness.** Every reload re-runs `wrangler d1 migrations apply --remote` (network round-trip) plus `go build`. Documented mitigation: use `wrangler dev --local` for the interactive loop, which skips the remote migration apply.

## Out of scope (restated)

- TinyGo migration for free-tier eligibility.
- Postgres-over-HTTP / Hyperdrive provider.
- GitHub Action for CI deploys.
- Durable Objects for namespace-scoped sweeping.
