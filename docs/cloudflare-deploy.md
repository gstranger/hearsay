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

Neither is great. If this becomes a sharp pain point, split migrations out into a `make deploy` wrapper in a follow-up.
