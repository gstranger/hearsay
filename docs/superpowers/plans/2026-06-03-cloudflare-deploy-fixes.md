# Cloudflare Deploy Fixes + Managed-Provider WASM Support — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a clean `git clone` of hearsay deployable to Cloudflare Workers in three documented commands, while keeping the native Go binary path unchanged and adding the existing managed HTTP provider as a second storage option for the WASM target.

**Architecture:** Restore `[build]` in `wrangler.toml` (WASM compile + D1 migration apply), replace the account-specific `database_id` with a placeholder, drop the defensive `!wasm` build tag on `internal/managed`, and add a `pickProviderKind` selector in `cmd/hearsay` so the Worker can use either D1 or a remote managed endpoint via env vars.

**Tech Stack:** Go 1.24+ (native and `GOOS=js GOARCH=wasm`), Cloudflare Workers (wrangler v3+), D1, Cloudflare D1 migrations.

**Spec:** `docs/superpowers/specs/2026-06-03-cloudflare-deploy-fixes-design.md`

---

## File Structure

**New files:**
- `cmd/hearsay/provider_kind.go` — `ProviderKind` enum and `pickProviderKind` selector. No build tag — testable on native.
- `cmd/hearsay/provider_kind_test.go` — four-arm selection test.

**Modified files:**
- `wrangler.toml` — restore `[build]` block, placeholder `database_id`, add `migrations_dir`, drop stale `[env.production]` and `[build.upload]`.
- `internal/managed/managed.go` — drop `//go:build !wasm` (provider becomes available to WASM target).
- `cmd/hearsay/main_wasm.go` — wire `pickProviderKind` into `handleRequest`, accept `args[2]`/`args[3]` for managed URL/token, add `safeString` helper, document arg-order contract.
- `worker.js` — pass `env.HEARSAY_MANAGED_URL` and `env.HEARSAY_MANAGED_TOKEN` as `args[2]`/`args[3]`.
- `README.md` — rewrite Architecture (rows 307–340) and Deploy-to-Cloudflare (rows 344–377) sections; add runtime feature matrix; correct file table (drop DO row).
- `docs/cloudflare-deploy.md` — full rewrite per spec section 4.

**Out of scope (do not touch):** SQLite/Postgres providers, A2A server, native `cmd_serve.go`, sweeper. None of these are exercised by the WASM build today.

---

## Task 1: Add `pickProviderKind` selector with TDD

**Files:**
- Create: `cmd/hearsay/provider_kind.go`
- Create: `cmd/hearsay/provider_kind_test.go`

The whole point of extracting this enum-returning function is so it can be unit-tested on native (no `syscall/js`). The WASM entrypoint translates the enum into real providers later in Task 3.

- [ ] **Step 1: Write the failing test file**

Create `cmd/hearsay/provider_kind_test.go`:

```go
package main

import "testing"

func TestPickProviderKind_ManagedWinsWhenSet(t *testing.T) {
	if got := pickProviderKind("https://hearsay.example.com", false); got != ProviderManaged {
		t.Fatalf("expected ProviderManaged, got %v", got)
	}
}

func TestPickProviderKind_FallsBackToD1(t *testing.T) {
	if got := pickProviderKind("", true); got != ProviderD1 {
		t.Fatalf("expected ProviderD1, got %v", got)
	}
}

func TestPickProviderKind_ReturnsNoneWhenNeitherConfigured(t *testing.T) {
	if got := pickProviderKind("", false); got != ProviderNone {
		t.Fatalf("expected ProviderNone, got %v", got)
	}
}

func TestPickProviderKind_ManagedWinsEvenWhenD1AlsoSet(t *testing.T) {
	if got := pickProviderKind("https://hearsay.example.com", true); got != ProviderManaged {
		t.Fatalf("expected ProviderManaged (managed wins), got %v", got)
	}
}
```

- [ ] **Step 2: Run test to confirm it fails**

Run: `go test ./cmd/hearsay -run TestPickProviderKind -v`

Expected: build error — `undefined: pickProviderKind`, `undefined: ProviderManaged`, etc.

- [ ] **Step 3: Implement `pickProviderKind`**

Create `cmd/hearsay/provider_kind.go`:

```go
package main

// ProviderKind names which storage backend the WASM runtime should use.
// Defined without a build tag so the selection logic is testable on native.
type ProviderKind int

const (
	ProviderNone ProviderKind = iota
	ProviderManaged
	ProviderD1
)

// pickProviderKind picks the backend given which env inputs are populated.
// HEARSAY_MANAGED_URL wins when set; otherwise the D1 binding is used;
// otherwise ProviderNone (the caller should respond with HTTP 500).
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

- [ ] **Step 4: Run test to confirm it passes**

Run: `go test ./cmd/hearsay -run TestPickProviderKind -v`

Expected: `PASS` for all four arms.

- [ ] **Step 5: Confirm the rest of the suite is unaffected**

Run: `go test ./...`

Expected: All packages pass.

- [ ] **Step 6: Confirm WASM cross-compile still works**

Run: `GOOS=js GOARCH=wasm go build -o /tmp/hearsay.wasm ./cmd/hearsay`

Expected: No output, exit 0. The new file has no build tag, so it's included in the WASM build too — this catches any unexpected coupling.

- [ ] **Step 7: Commit**

```bash
git add cmd/hearsay/provider_kind.go cmd/hearsay/provider_kind_test.go
git commit -m "$(cat <<'EOF'
feat: add pickProviderKind selector for WASM provider choice

Pure enum-returning selector with no build tag so it is testable on
native. The WASM entrypoint will translate the returned kind into a
real provider (D1 or managed) in the next change.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Make the managed provider WASM-compatible

**Files:**
- Modify: `internal/managed/managed.go:1` (remove `//go:build !wasm`)

The managed provider uses only stdlib `net/http.Client` with a `Timeout` — Go's WASM target maps that to browser `fetch` transparently. The build tag was defensive, not load-bearing.

- [ ] **Step 1: Confirm the file currently excludes itself from WASM**

Run: `head -3 internal/managed/managed.go`

Expected output starts with:
```
//go:build !wasm

package managed
```

- [ ] **Step 2: Confirm WASM build currently *fails* if we try to import managed from WASM code**

Run: `GOOS=js GOARCH=wasm go build ./internal/managed`

Expected: build error mentioning `build constraints exclude all Go files`. This is the bug we're fixing.

- [ ] **Step 3: Drop the build tag**

Edit `internal/managed/managed.go`. Remove these two lines from the top:

```go
//go:build !wasm

```

Leave the rest of the file untouched. The file should now start with `package managed`.

- [ ] **Step 4: Verify managed compiles for WASM**

Run: `GOOS=js GOARCH=wasm go build ./internal/managed`

Expected: No output, exit 0.

- [ ] **Step 5: Verify managed still compiles + tests on native**

Run: `go build ./internal/managed && go test ./internal/managed`

Expected: build clean, all tests pass.

- [ ] **Step 6: Verify the whole tree cross-compiles**

Run: `GOOS=js GOARCH=wasm go build ./...`

Expected: No output, exit 0. If any other package fails, that's a real WASM-incompatibility surfaced by the change — investigate before proceeding rather than reverting.

- [ ] **Step 7: Commit**

```bash
git add internal/managed/managed.go
git commit -m "$(cat <<'EOF'
feat: make managed provider available to WASM target

Drop the defensive //go:build !wasm tag. The provider only uses
stdlib net/http.Client with a Timeout, which Go's WASM target maps
transparently to the browser fetch API.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Wire `pickProviderKind` into `main_wasm.go`

**Files:**
- Modify: `cmd/hearsay/main_wasm.go:11-47` (imports + handleRequest provider selection)
- Modify: `cmd/hearsay/main_wasm.go` (append `safeString` helper)

- [ ] **Step 1: Add `managed` to the import block**

Open `cmd/hearsay/main_wasm.go`. The imports currently look like:

```go
import (
	"context"
	"net/http"
	"strings"
	"syscall/js"

	"github.com/gstranger/hearsay/internal/a2a"
	"github.com/gstranger/hearsay/internal/d1"
	"github.com/gstranger/hearsay/internal/server"
	"github.com/gstranger/hearsay/pkg/hearsay"
)
```

Add the managed import. The block becomes:

```go
import (
	"context"
	"net/http"
	"strings"
	"syscall/js"

	"github.com/gstranger/hearsay/internal/a2a"
	"github.com/gstranger/hearsay/internal/d1"
	"github.com/gstranger/hearsay/internal/managed"
	"github.com/gstranger/hearsay/internal/server"
	"github.com/gstranger/hearsay/pkg/hearsay"
)
```

- [ ] **Step 2: Update the `handleRequest` arg parsing and provider selection**

Find the current block (around lines 31–47):

```go
func handleRequest(this js.Value, args []js.Value) any {
	request := args[0]
	d1Binding := args[1]
	url := request.Get("url").String()

	if globalProvider == nil {
		if d1Binding.IsUndefined() {
			return newResponse(500, `{"error":"D1 binding not provided"}`)
		}
		globalProvider = d1.New(d1Binding)
		client := hearsay.NewClient(globalProvider, "default")
		globalServer = server.New(client, globalProvider, false, "", server.LogFormatText, 0, 0, hearsay.AuditOff, "default")

		// A2A server — uses the same provider for task storage.
		// Auth is optional on Workers (Cloudflare handles edge auth).
		globalA2A = a2a.NewServer(nil, client, globalProvider, nil, "default")
	}
```

Replace with:

```go
func handleRequest(this js.Value, args []js.Value) any {
	// Argument contract (set by worker.js, load-bearing):
	//   args[0] = Request
	//   args[1] = D1 binding (may be undefined)
	//   args[2] = HEARSAY_MANAGED_URL string (may be undefined)
	//   args[3] = HEARSAY_MANAGED_TOKEN string (may be undefined)
	request := args[0]
	d1Binding := args[1]
	managedURL := safeString(args[2])
	managedToken := safeString(args[3])
	url := request.Get("url").String()

	if globalProvider == nil {
		switch pickProviderKind(managedURL, d1Binding.Truthy()) {
		case ProviderManaged:
			globalProvider = managed.New(managedURL, managedToken)
		case ProviderD1:
			globalProvider = d1.New(d1Binding)
		default:
			return newResponse(500, `{"error":"no provider configured: set HEARSAY_D1 binding or HEARSAY_MANAGED_URL"}`)
		}
		client := hearsay.NewClient(globalProvider, "default")
		globalServer = server.New(client, globalProvider, false, "", server.LogFormatText, 0, 0, hearsay.AuditOff, "default")

		// A2A server — uses the same provider for task storage.
		// Auth is optional on Workers (Cloudflare handles edge auth).
		globalA2A = a2a.NewServer(nil, client, globalProvider, nil, "default")
	}
```

- [ ] **Step 3: Append the `safeString` helper at the bottom of the file**

Add this function after `newResponse` (or anywhere at file scope below `handleRequest`):

```go
// safeString returns v.String() when v is a populated JS string,
// or "" when v is undefined/null. Avoids a panic when an env var
// was not configured on the Worker.
func safeString(v js.Value) string {
	if v.IsUndefined() || v.IsNull() {
		return ""
	}
	return v.String()
}
```

- [ ] **Step 4: Cross-compile the WASM target**

Run: `GOOS=js GOARCH=wasm go build -o /tmp/hearsay.wasm ./cmd/hearsay`

Expected: No output, exit 0.

- [ ] **Step 5: Run native build + full test suite**

Run: `go build ./cmd/hearsay && go test ./...`

Expected: native binary builds cleanly; all tests pass (including the four `TestPickProviderKind` arms).

- [ ] **Step 6: Commit**

```bash
git add cmd/hearsay/main_wasm.go
git commit -m "$(cat <<'EOF'
feat: WASM target can use managed provider via HEARSAY_MANAGED_URL

handleRequest now accepts managed URL/token in args[2]/args[3] and
delegates to pickProviderKind for selection. HEARSAY_MANAGED_URL wins
when set; otherwise the D1 binding is used; otherwise the Worker
responds 500 with an explicit "no provider configured" message.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Pass managed env vars from `worker.js`

**Files:**
- Modify: `worker.js:19`

- [ ] **Step 1: Update the handleRequest call**

Open `worker.js`. The current call (line 19):

```js
    return globalThis.handleRequest(request, env.HEARSAY_D1);
```

Replace with:

```js
    return globalThis.handleRequest(
      request,
      env.HEARSAY_D1,
      env.HEARSAY_MANAGED_URL,
      env.HEARSAY_MANAGED_TOKEN,
    );
```

- [ ] **Step 2: Confirm the file parses (no syntax check possible without wrangler, but we can lint-check via node)**

Run: `node --check worker.js`

Expected: No output, exit 0. (If `node` is not installed, skip — this is a non-blocking sanity check.)

- [ ] **Step 3: Commit**

```bash
git add worker.js
git commit -m "$(cat <<'EOF'
feat: pass HEARSAY_MANAGED_URL/TOKEN env vars to WASM handleRequest

Matches the args[2]/args[3] contract introduced in main_wasm.go. When
both env vars are unset, the WASM handler falls back to the D1 binding
or 500s with an explicit message.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Restore `wrangler.toml` build block + placeholder D1 ID

**Files:**
- Modify: `wrangler.toml` (full replacement)

- [ ] **Step 1: Replace the file contents**

Overwrite `wrangler.toml` with:

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

- [ ] **Step 2: Confirm wrangler validates the config**

Run: `npx wrangler deploy --dry-run --outdir /tmp/hearsay-dry 2>&1 | head -40`

Expected: wrangler parses the file successfully. Two failure modes are acceptable and informative:
- Auth missing (`wrangler login` required) — the file parsed.
- "Couldn't find a D1 DB with the id 'REPLACE_WITH_YOUR_D1_ID'" — the placeholder works as designed.

Failure modes that are *not* acceptable: TOML parse errors, unknown keys, schema errors. Fix them before proceeding.

If wrangler is not installed, run: `npx wrangler@latest --version` once to install it transiently, then retry. If still not available, skip this step and rely on the documented placeholder behavior.

- [ ] **Step 3: Confirm placeholder is recognizable**

Run: `grep -c "REPLACE_WITH_YOUR_D1_ID" wrangler.toml`

Expected: `1`.

- [ ] **Step 4: Commit**

```bash
git add wrangler.toml
git commit -m "$(cat <<'EOF'
fix: restore [build] block and use placeholder D1 id in wrangler.toml

A clean clone now deploys via three steps documented in
docs/cloudflare-deploy.md: wrangler d1 create, paste id, wrangler
deploy. The [build] block runs migrations apply + WASM compile +
wasm_exec.js stage so wrangler deploy produces a working Worker.
Drops the stale [env.production] and legacy [build.upload] blocks.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Rewrite README Architecture + Deploy sections

**Files:**
- Modify: `README.md:307-377` (Architecture and Deploy-to-Cloudflare sections, plus file table)

- [ ] **Step 1: Replace the Architecture section**

Find the heading `## Architecture` (around line 307) and replace everything between it and the next `---` (around line 342) with:

```markdown
## Architecture

hearsay compiles from one source tree into two runtime targets that share the same coordination engine:

```
Native target          WASM (Cloudflare Workers) target
─────────────          ────────────────────────────────
SQLite                 D1
PostgreSQL             Managed (HTTP → remote hearsay)
Managed
(in-memory, tests)
```

- **Native binary** — `go build ./cmd/hearsay` → standalone server backed by SQLite, PostgreSQL, or a managed remote.
- **WASM Worker** — `GOOS=js GOARCH=wasm go build ./cmd/hearsay` → Cloudflare Worker backed by D1 (default) or a managed remote (HEARSAY_MANAGED_URL).

### Runtime feature matrix

| Feature | Native | Workers |
|---|---|---|
| REST API | ✓ | ✓ |
| A2A JSON-RPC (`tasks/send`) | ✓ | ✓ |
| A2A SSE (`tasks/sendSubscribe`) | ✓ | ✗ |
| Background sweeper | ✓ | ✗ (stub) |
| TLS | ✓ | edge (CF) |
```

- [ ] **Step 2: Replace the Deploy-to-Cloudflare section**

Find `## Deploy to Cloudflare Workers` (around line 344) and replace everything between it and the next `---` (around line 378) with:

```markdown
## Deploy to Cloudflare Workers

Three commands from a clean clone (requires a paid Workers plan — the WASM is ~6 MB, over the 3 MB free-tier limit):

```bash
wrangler d1 create hearsay-db          # copy the returned id
# paste id into wrangler.toml database_id
wrangler deploy
```

The `[build]` block in `wrangler.toml` runs `wrangler d1 migrations apply --remote` then `GOOS=js GOARCH=wasm go build` then copies `wasm_exec.js` — no manual steps.

See [`docs/cloudflare-deploy.md`](docs/cloudflare-deploy.md) for prerequisites, the alternative managed-remote storage path, the runtime limitations (no SSE), and Deploy-to-Cloudflare button status.

| File | Purpose |
|---|---|
| `worker.js` | JS entrypoint — loads Go WASM, bridges Cloudflare's `fetch` API |
| `wrangler.toml` | Worker config — D1 binding, build command, env vars |
| `migrations/0001_init.sql` | D1 schema (same tables as SQLite) |
| `cmd/hearsay/main_wasm.go` | Go code compiled to WASM — exports `handleRequest(request, d1, managedURL, managedToken)` |
```

- [ ] **Step 3: Drift grep — confirm nothing stale survives**

Run: `grep -nE "Durable|auto-provision|CoordinatorDO|free plan" README.md`

Expected: zero matches. If anything turns up, fix the survivors in the same commit.

- [ ] **Step 4: Confirm key new strings exist**

Run: `grep -cE "Runtime feature matrix|HEARSAY_MANAGED_URL|REPLACE_WITH_YOUR_D1_ID|paid Workers plan" README.md`

Expected: 4 (one per substring, or more if any repeats).

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "$(cat <<'EOF'
docs: rewrite README architecture + Cloudflare deploy sections

Splits native and WASM targets symmetrically in the architecture
diagram, adds a runtime feature matrix showing what works where
(notably: no SSE on Workers), documents the three-command deploy
flow, and drops the Durable Objects mention that no longer matches
wrangler.toml.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Rewrite `docs/cloudflare-deploy.md`

**Files:**
- Modify: `docs/cloudflare-deploy.md` (full replacement)

- [ ] **Step 1: Overwrite the file**

Replace the entire contents of `docs/cloudflare-deploy.md` with:

```markdown
# Deploy hearsay to Cloudflare Workers

## What you get

- Edge-hosted REST API and A2A JSON-RPC at `https://hearsay.<your-subdomain>.workers.dev`.
- Storage in Cloudflare D1 (default) or in a remote hearsay you already run (managed mode).
- The WASM target does **not** support A2A SSE streaming (`tasks/sendSubscribe`) — see "What doesn't work yet" below.

## Prerequisites

- **Paid Workers plan.** The WASM binary is ~6 MB; the free-tier limit is 3 MB.
- **Go 1.24+** on the machine running `wrangler deploy` (the `[build]` block invokes `go build`).
- **Node 18+** and the **wrangler CLI** (`npx wrangler` is fine).
- A Cloudflare account with `wrangler login` already run, or `CLOUDFLARE_API_TOKEN` set in CI.

## Default path: D1 storage

```bash
wrangler d1 create hearsay-db          # copy the returned id
# paste the id into wrangler.toml under [[d1_databases]] database_id
wrangler deploy
```

The `[build]` block in `wrangler.toml` automatically:

1. Applies pending D1 migrations (`wrangler d1 migrations apply HEARSAY_D1 --remote`).
2. Compiles Go to WASM (`GOOS=js GOARCH=wasm go build -o hearsay.wasm ./cmd/hearsay`).
3. Stages `wasm_exec.js` from the local Go toolchain.

Migrations are idempotent — wrangler tracks applied migrations server-side.

## Alternative: managed-remote storage

If you already run hearsay natively somewhere (a VPS, fly.io, a Kubernetes pod) and want a Cloudflare edge in front of it, point the Worker at it instead of D1:

1. In `wrangler.toml`, comment out the entire `[[d1_databases]]` block.
2. Add `HEARSAY_MANAGED_URL` to `[vars]`:
   ```toml
   [vars]
   HEARSAY_VERSION = "1.0.0"
   HEARSAY_MANAGED_URL = "https://my-hearsay.example.com"
   ```
3. Store the bearer token as a secret:
   ```bash
   wrangler secret put HEARSAY_MANAGED_TOKEN
   ```
4. `wrangler deploy`.

**Priority rule:** if both `HEARSAY_MANAGED_URL` and the D1 binding are configured, managed wins and the D1 binding is silently ignored. The Worker logs no warning — the env var is treated as an explicit opt-in.

## What doesn't work yet

| Limitation | Why | Future path |
|---|---|---|
| A2A SSE streaming | Workers don't expose `http.Flusher` in the Go WASM bridge | Could use Workers' native streaming via a custom JS↔Go bridge — separate spec |
| Postgres backend | `pgx` can't open raw TCP from WASM | Build a new provider using Hyperdrive or Neon HTTP — separate spec |
| Background sweeper | The WASM sweeper is a no-op stub | Durable Objects per namespace — separate spec |

## Verifying the deploy

```bash
curl https://hearsay.<your-subdomain>.workers.dev/ns/test/.well-known/agent.json
```

Should return the agent card with `"streaming": false`.

```bash
curl -X POST https://hearsay.<your-subdomain>.workers.dev/ns/test/claim \
  -H "Content-Type: application/json" \
  -d '{"resource_uri":"file://src/api.go","agent_id":"bot-1","operation":"write","intent":"smoke test"}'
```

Should return a `claim_id`. If you see `{"error":"hearsay is starting up"}` with HTTP 503, the Worker is cold-starting — retry after 2s.

## Deploy-to-Cloudflare button — status

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/gstranger/hearsay)

The button should work because Cloudflare's build environment has Go available and runs the `[build]` block. It has **not** been re-validated since the recent `wrangler.toml` reshuffle — manual `wrangler deploy` is the supported path. If the button errors, fall back to the three-command flow above.

## Files

| File | Purpose |
|---|---|
| `worker.js` | JS entrypoint — loads Go WASM, bridges Cloudflare's `fetch` API |
| `wrangler.toml` | Worker config — D1 binding, build command, env vars |
| `migrations/0001_init.sql` | D1 schema (same tables as SQLite) |
| `cmd/hearsay/main_wasm.go` | Go code compiled to WASM — exports `handleRequest(request, d1, managedURL, managedToken)` |
| `cmd/hearsay/provider_kind.go` | Provider selection logic (`pickProviderKind`) |

## Local dev with `wrangler dev`

`wrangler dev` re-runs the full `[build]` block on every reload, including the remote D1 migrations apply (a network round-trip on every change). `wrangler dev --local` runs the Worker against a local D1 emulator at runtime, but it does **not** skip the build command — `wrangler d1 migrations apply --remote` still fires.

If the dev loop is too slow, two workarounds:

1. **Comment the migration line locally.** Temporarily strip the first line of the `[build]` command in `wrangler.toml` during a dev session. Don't commit the change.
2. **Pre-build, then skip `[build]`.** Run `GOOS=js GOARCH=wasm go build -o hearsay.wasm ./cmd/hearsay && cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" .` once, then pass `--no-bundle` to `wrangler dev` for subsequent reloads. Re-run the build manually when Go code changes.

Neither is great. If this becomes a sharp pain point, it's worth splitting migrations out into a `make deploy` wrapper in a follow-up — see the spec's risk #5.
```

- [ ] **Step 2: Drift grep**

Run: `grep -nE "Durable|CoordinatorDO|free plan|auto-provision" docs/cloudflare-deploy.md`

Expected: zero matches.

- [ ] **Step 3: Confirm key new strings**

Run: `grep -cE "Default path|Alternative: managed|REPLACE_WITH_YOUR_D1_ID|HEARSAY_MANAGED_URL|wrangler dev --local" docs/cloudflare-deploy.md`

Expected: 5 or more.

- [ ] **Step 4: Commit**

```bash
git add docs/cloudflare-deploy.md
git commit -m "$(cat <<'EOF'
docs: rewrite cloudflare-deploy.md for restored build flow

Replaces the stale CoordinatorDO + free-plan content. New sections:
prerequisites (paid plan, Go 1.24+), default D1 path, alternative
managed-remote path, what doesn't work yet, deploy verification curls,
honest Deploy-button status, and a wrangler dev --local note for
interactive dev.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Final verification

No new code — this task confirms the previous seven didn't break anything and surfaces any drift survivors before pushing.

- [ ] **Step 1: Run full Go test suite**

Run: `go test ./...`

Expected: all packages pass.

- [ ] **Step 2: Run with race detector**

Run: `go test -race ./...`

Expected: all packages pass, no race warnings.

- [ ] **Step 3: Native build sanity check**

Run: `go build -o /tmp/hearsay-native ./cmd/hearsay && /tmp/hearsay-native --help 2>&1 | head -5`

Expected: binary builds; `--help` produces usage output (any exit code OK; we only care that the binary runs).

- [ ] **Step 4: WASM cross-compile of the whole tree**

Run: `GOOS=js GOARCH=wasm go build ./...`

Expected: every package compiles, including `./internal/managed`.

- [ ] **Step 5: Size check — confirm we still fit on paid plan**

Run: `GOOS=js GOARCH=wasm go build -o /tmp/hearsay.wasm ./cmd/hearsay && wc -c /tmp/hearsay.wasm`

Expected: under 10,485,760 bytes (10 MB — the paid-tier hard cap). For reference, before this change it was ~6.2 MB. If we grew past 8 MB, investigate which import caused the bloat before proceeding.

- [ ] **Step 6: Drift grep across all touched docs**

Run: `grep -nE "Durable|CoordinatorDO|auto-provision[^d]|free plan" README.md docs/cloudflare-deploy.md`

Expected: zero matches. If any survive, fix them inline and amend the relevant doc commit (or add a follow-up commit).

- [ ] **Step 7: Confirm the `wrangler.toml` placeholder is still in place**

Run: `grep -n "REPLACE_WITH_YOUR_D1_ID" wrangler.toml`

Expected: one match. If a real ID got pasted in by accident during testing, replace it with the placeholder again before pushing.

- [ ] **Step 8: Push**

```bash
git push
```

Expected: clean push to `origin/main` with the 7 task commits.
