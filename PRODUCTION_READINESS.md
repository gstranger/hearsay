# Production Readiness Assessment

> Last updated: 2026-05-26  
> Version assessed: Post dual-runtime architecture (SSE, WASM/Worker, D1)

Honest assessment of what is solid enough for production and what gaps remain.

---

## TL;DR

| Use Case | Ready? |
|---|---|
| Single developer, local SQLite, localhost | ✅ Yes |
| Small team, shared Postgres, trusted network/VPN | ✅ Yes |
| CI/CD pipeline claiming files (behind VPN) | ✅ Yes |
| Public / internet-facing service | ⚠️ Yes, with TLS + auth enabled |
| Single-instance SaaS (one shared Postgres, Cloudflare edge) | ⚠️ Yes, with SSE push + Workers/WASM deployment |
| Multi-tenant SaaS (isolated namespaces, cross-region) | ❌ No |
| Large team (10+ agents, multiple instances or regions) | ❌ No |
| A2A interop or cross-vendor delegation | ✅ Yes (A2A adapter ships) |

---

## What Comprises the Tool Today

### Core CLI (`hearsay` binary)

| Command | Purpose |
|---|---|
| `init` | Creates `.hearsay.toml` config |
| `claim` | Lock a resource with operation + intent |
| `release` | Unlock a claim with outcome |
| `heartbeat` | Keep a claim alive (extends TTL) |
| `query` | List active claims matching pattern |
| `check` | Dry-run conflict check before claiming |
| `namespace` | Config-only namespace bookkeeping |
| `serve` | Start HTTP + optional A2A API server |
| `watch` | Filesystem watcher — auto-claims files on change |
| `cursor` | Cursor IDE integration via `hooks.json` |
| `status` | Show runtime state (active claims, mailbox, agents) |

### Storage Backends (5 providers)

- **SQLite** — Local file, default. Pure Go, no CGO.
- **PostgreSQL** — Shared database for teams.
- **Managed** — HTTP client proxying to a remote `hearsay serve`.
- **D1** — Cloudflare D1 (distributed SQLite) for Worker/WASM deployment.
- **Memory** — In-memory provider for tests.

### HTTP Server

- REST API on `:8080` with optional TLS, auth, rate limiting, audit logging
- SSE event streaming (`GET /events?namespace=X&since=N`) with monotonic sequence numbers for reconnect-without-miss
- A2A JSON-RPC server on configurable port with JWT/API key auth
- A2A SSE streaming (`tasks/sendSubscribe`) with Agent Card advertising `capabilities.streaming: true`
- 5 A2A skills: claim_resource, release_resource, check_conflict, query_mailbox, send_mailbox
- Dual-runtime: compiles to native Go binary (`go build`) AND Cloudflare Worker WASM (`GOOS=js GOARCH=wasm`) from one source tree

### Mailbox System

Agent-to-agent messaging with yield requests, escalations, notes, pings, and broadcast support.

### Conflict Detection

5 operations with well-tested conflict matrix, glob pattern matching, and optional locking mode.

### Integrations

- **Cursor IDE** — Hooks for sessionStart, preToolUse, postToolUse, sessionEnd
- **OpenCode** — Plugin for OpenCode CLI agent
- **Pi** — Extension for Pi Coding Agent
- **TypeScript SDK** — `HearsayClient` with auto-heartbeat, auto-release
- **Coordination Etiquette** — Agent skill for cooperative multi-agent work
- **File watcher** — `fsnotify`-based auto-claim

---

## What's Solid ✅

| Area | Status | Notes |
|---|---|---|
| Conflict detection matrix | ✅ | Well-tested, glob patterns, recursive prefixes |
| SQLite provider | ✅ | Pure Go, migrations via `CREATE TABLE IF NOT EXISTS` |
| PostgreSQL provider | ✅ | Standard SQL, contract suite tested |
| Mailbox messaging | ✅ | Clean API, proper indexes, TTL expiry |
| HTTP server handlers | ✅ | Clean separation of concerns, tested |
| TypeScript SDK | ✅ | Full Vitest coverage |
| Cursor integration | ✅ | Never blocks, auto-releases, graceful degradation |
| Managed provider proxy | ✅ | HTTP client, Bearer auth, timeout handling |
| A2A protocol support | ✅ | Agent Card, JSON-RPC 2.0, 5 skills, JWT/JWKS + API key auth |
| REST API auth | ✅ | `--auth-token` with Bearer + X-Api-Key |
| TLS/HTTPS | ✅ | `--tls-auto`, `--tls-cert`, `--tls-key` |
| Input validation | ✅ | Max length limits on agent_id, resource_uri, intent, namespace |
| Rate limiting | ✅ | Per-agent sliding window, `--rate-limit`, `--rate-burst` |
| Audit log | ✅ | Configurable levels: off/coordination/security/full |
| Graceful shutdown | ✅ | SIGTERM with 10s timeout |
| Request logging | ✅ | Text/JSON format, method, path, agent_id, latency, status |
| Background sweeper | ✅ | Cleans expired claims every 60s |
| Process death detection | ✅ | `--agent-timeout` auto-releases dead agents' claims |
| Status subcommand | ✅ | `hearsay status --verbose` |
| Cursor state location | ✅ | Moved from `/tmp` to `~/.config/hearsay/cursor/` |
| SSE event streaming | ✅ | `GET /events` with `since` replay, monotonic seq numbers, reconnect-safe |
| Dual-runtime compilation | ✅ | Native binary + Cloudflare Worker WASM from one source tree |
| D1 provider (WASM) | ✅ | Full Provider interface for Cloudflare D1 via `syscall/js` |
| Durable Object (WASM) | ✅ | Alarm-based sweeper per namespace for Worker runtime |
| CI/CD | ✅ | GitHub Actions: lint, test, build |

---

## What's Still Missing ⚠️ ❌

### Distribution & Scale

| Gap | Risk | Priority |
|---|---|---|
| **No clustering** | Multiple `hearsay serve` instances = split brain. Two agents on different instances can both be granted the same claim — coordination breaks entirely across instance boundaries. Fix: shared Postgres (simple but slow cross-region) or Redis pub/sub gossip (fast but complex). | 🔴 Critical |
| **Broadcast cross-instance** | `to: "broadcast"` mailbox messages only fan out within one instance. An escalation sent on us-east never reaches agents on eu-west. Same root cause as clustering — once instances share state, broadcast propagates naturally. | 🔴 Critical |

### Operations & Observability

| Gap | Risk | Priority |
|---|---|---|
| **No Prometheus metrics** | No `/metrics` endpoint | 🟡 Medium |
| **No database migration framework** | Schema changes = manual `ALTER TABLE` or wipe | 🟡 Medium |
| **No `hearsay audit` subcommand** | Audit events only queryable via raw SQL | 🟡 Medium |
| **No backup/restore documentation** | SQLite is a single file — fine, but undocumented | 🟢 Low |

### Multi-Tenant

| Gap | Risk | Priority |
|---|---|---|
| **No namespace isolation with authz** | One API key grants access to all namespaces. Customer A's agents can read customer B's claims and mailbox. Fix requires per-key namespace scoping: when the SaaS control plane issues an API key, it binds it to specific namespaces. Server middleware checks scope before every request. ~200 lines of middleware + key-to-namespace mapping. | 🔴 Critical |
| **No load testing / benchmarks** | Unknown performance limits under realistic agent workloads | 🟡 Medium |
| **No federation** | Cannot forward claims between independent servers | 🟢 Low |

---

## Remaining Hardening Checklist

### Phase 2 — "Ready for Team CI/CD" (Weeks)

- [ ] Add Prometheus metrics endpoint (`/metrics`)
- [ ] Add `hearsay audit` subcommand for querying audit log
- [ ] Add schema versioning / migration table
- [ ] Document backup/restore for SQLite and Postgres

### Phase 3 — "Ready for Multi-Tenant SaaS" (Months)

- [ ] Add namespace isolation with per-key authz (blocker #3 — see below)
- [ ] Add clustering / shared state backend (blocker #1 — Redis pub/sub or Postgres LISTEN/NOTIFY)
- [ ] Add broadcast cross-instance propagation (blocker #2 — free with clustering)
- [ ] Add federation: one `hearsay serve` can forward claims to another
- [ ] Load testing and performance benchmarks

---

## The Three SaaS Blockers — Explained

### Blocker 1: Clustering (Split-Brain)

**What happens today:** Two `hearsay serve` instances running on separate machines can't see each other's claims. If Agent A on instance us-east claims `file://src/api.go`, and Agent B on instance eu-west tries to claim the same file, neither knows about the other — both claims are granted. Coordination is broken across instances.

**Why the single-instance workaround works:** Point both instances at the same shared Postgres database and the problem disappears — all claims land in the same `messages` table. But this ties you to one DB across regions (latency), or one region (no failover).

**The fix:** A pub/sub layer between instances. When a claim is appended on any instance, it publishes the event. All other instances consume it and update their local state. Natural candidates: Redis pub/sub (simple, well-understood), Postgres LISTEN/NOTIFY (no new infrastructure), or a dedicated gossip protocol between hearsay instances (no external dependency).

### Blocker 2: Broadcast Cross-Instance

**What happens today:** The mailbox supports `to: "broadcast"` — a message sent to every connected agent. But "every" means every agent connected to *this instance*. An escalation sent from an agent on us-east never reaches agents on eu-west.

**Why it's the same root cause:** Broadcast is just fan-out of a single message to multiple recipients. If instances share state (Blocker 1), broadcast naturally propagates — the message lands in the shared mailbox table, and all instances deliver it to their connected agents.

**The fix:** Comes free with clustering. Solve Blocker 1 and broadcast works across instances automatically.

### Blocker 3: Namespace Isolation

**What happens today:** One `--auth-token` protects the entire instance. Anyone with the token can access any namespace — read claims, send mailbox messages, query state. For multi-tenant SaaS, Customer A's agents must not be able to see Customer B's claims.

**Why it's separate from clustering:** Even with a single shared Postgres (no split-brain), namespace isolation is still missing. The database stores all namespaces in the same tables. The server has no concept of "this API key is only authorized for namespace X."

**The fix:** Per-key namespace scoping. The SaaS control plane (a separate service) issues API keys bound to specific namespaces. hearsay adds middleware that checks: on every request, does the authenticated key's scope include the requested namespace? This is ~200 lines of middleware + a key-to-namespace mapping table. The coordination engine itself stays unchanged — it just gains an authorization gate.

---

## Test Coverage Status

```
ok  github.com/gstranger/hearsay/cmd/hearsay
ok  github.com/gstranger/hearsay/internal
ok  github.com/gstranger/hearsay/internal/a2a
ok  github.com/gstranger/hearsay/internal/mailbox
ok  github.com/gstranger/hearsay/internal/managed
ok  github.com/gstranger/hearsay/internal/postgres
ok  github.com/gstranger/hearsay/internal/server
ok  github.com/gstranger/hearsay/internal/sqlite
ok  github.com/gstranger/hearsay/internal/watcher
ok  github.com/gstranger/hearsay/pkg/hearsay
ok  github.com/gstranger/hearsay/tests/integration
```

12 packages, all passing. TypeScript SDK has full Vitest coverage. GitHub Actions CI runs on every push/PR. WASM targets compile but are tested at build time only (D1/DO require a Worker runtime).

---

## Bottom Line

**hearsay is solid for single-instance team use and single-region SaaS.** With TLS + auth enabled, SSE push streaming, A2A interop, and dual-runtime compilation (native + Cloudflare Worker), it's safe to expose on a shared network. The three remaining SaaS blockers — clustering (no split-brain across instances), broadcast propagation, and namespace isolation — share a tight dependency chain: solving clustering solves broadcast, and namespace isolation is a standalone ~200-line middleware change. For team CI/CD, self-hosted deployments, or single-server SaaS behind Cloudflare, hearsay is production-ready today.