# Deploy to Cloudflare

## One-Click Deploy

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/gstranger/hearsay)

The Deploy to Cloudflare button creates:
- A **D1 database** (`hearsay-db`) for coordination state
- A **Durable Object** namespace for the `CoordinatorDO` class
- A Worker that runs the hearsay coordination engine as WebAssembly

## What Gets Deployed

```
┌──────────────────────────────────────────────┐
│  Cloudflare Worker (hearsay.workers.dev)      │
│                                              │
│  worker.js ──▶ hearsay.wasm (Go WASM)        │
│                    │                         │
│                    ▼                         │
│              D1 Database                     │
│              (claims, mailbox,               │
│               A2A tasks, audit)              │
│                                              │
│              CoordinatorDO                   │
│              (per-namespace sweeper)         │
└──────────────────────────────────────────────┘
```

## Manual Setup

```bash
# 1. Build the Go WASM binary
GOOS=js GOARCH=wasm go build -o hearsay.wasm ./cmd/hearsay

# 2. Copy Go's WASM runtime
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" .

# 3. Apply D1 database migrations
npx wrangler d1 migrations apply HEARSAY_D1 --remote

# 4. Deploy
npx wrangler deploy
```

## After Deployment

Your agents talk to `https://hearsay.<your-subdomain>.workers.dev`:

```bash
# Claim a resource
curl https://hearsay.<subdomain>.workers.dev/ns/my-project/claim \
  -H "Content-Type: application/json" \
  -d '{"resource_uri":"file://src/api.go","agent_id":"bot-1","operation":"write","intent":"refactoring"}'

# Stream coordination events via SSE
curl -N https://hearsay.<subdomain>.workers.dev/ns/my-project/events
```

The URL pattern is `/ns/<namespace>/<endpoint>` — the Worker routes to the same REST handlers as the native hearsay binary.

## Files

| File | Purpose |
|---|---|
| `worker.js` | JS entrypoint — loads Go WASM, bridges Cloudflare's `fetch` API |
| `hearsay.wasm` | Go coordination engine compiled to WebAssembly (built locally) |
| `wasm_exec.js` | Go's standard WASM runtime support (copied from Go toolchain) |
| `wrangler.toml` | Worker config — D1 database, Durable Object, build command |
| `migrations/0001_init.sql` | D1 schema migration — creates all hearsay tables |

## Requirements

- [Go 1.25+](https://go.dev/dl/)
- [Node.js 18+](https://nodejs.org/)
- [Wrangler CLI](https://developers.cloudflare.com/workers/wrangler/) (`npm install -g wrangler`)
- A [Cloudflare account](https://dash.cloudflare.com/sign-up/workers)