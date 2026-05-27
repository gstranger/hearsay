# Production Readiness Assessment

> Last updated: 2026-05-26  
> Version assessed: v0.1 (commit range around initial build, May 2026)

This document is an honest assessment of what is solid enough for production use today and what gaps remain before the tool can be trusted in public, multi-tenant, or high-availability scenarios.

---

## TL;DR

| Use Case | Ready? |
|---|---|
| Single developer, local SQLite, localhost | ✅ Yes |
| Small team, shared Postgres, trusted network/VPN | ⚠️ Mostly — add basic auth |
| CI/CD pipeline claiming files (behind VPN) | ⚠️ Yes, with caveats |
| Public / internet-facing service | ❌ No |
| Multi-tenant SaaS | ❌ No |
| Large team (10+ agents, multiple hosts) | ❌ No |
| A2A interop or cross-vendor delegation | ❌ Not yet scoped |

---

## What Comprises the Tool Today

### Core CLI (`hearsay` binary)

| Command | Purpose |
|---|---|
| `init` | Creates `.hearsay.toml` config |
| `claim` | Lock a resource with operation + intent |
| `release` | Unlock a claim with outcome (`succeeded`/`abandoned`/`conflicted`) |
| `heartbeat` | Keep a claim alive (extends TTL) |
| `query` | List active claims matching pattern |
| `check` | Dry-run conflict check before claiming |
| `namespace` | Config-only namespace bookkeeping |
| `serve` | Start HTTP API server (`localhost:8080`) |
| `watch` | Filesystem watcher — auto-claims files on change |
| `cursor` | Cursor IDE integration via `hooks.json` |

### Storage Backends (3 providers)

- **SQLite** (`internal/sqlite/`) — Local file, default. Pure Go (`modernc.org/sqlite`), no CGO.
- **PostgreSQL** (`internal/postgres/`) — Shared database for teams.
- **Managed** (`internal/managed/`) — HTTP client proxying to a remote `hearsay serve`.

### HTTP Server (`internal/server/`)

Client-facing endpoints:
```
POST /claim              POST /release
POST /heartbeat          GET  /claims
GET  /check              POST /intent
POST /message            GET  /mailbox
POST /message/read       POST /message/archive
```

Provider-proxy endpoints:
```
POST /namespaces/create  POST /namespaces/delete
POST /append             GET  /query
GET  /agents             POST /expire
GET  /health
```

### Mailbox System

Agent-to-agent messaging types:
- `yield_request` / `yield_ack` — Ask another agent to release a claim
- `escalation` / `all_clear` — Problem escalation
- `note` / `ping` — General communication
- Broadcast support (`to: "broadcast"`)

### Conflict Detection

- 5 operations: `read`, `write`, `delete`, `rename`, `refactor`
- Operation conflict matrix (e.g., `write` conflicts with `write`, `delete`, `rename`, `refactor`)
- Resource pattern matching: exact, glob (`*`), prefix (`/**`)
- Optional locking mode: reject claims outright instead of just reporting conflicts

### Integrations

- **Cursor IDE** (`cmd/hearsay/cursor.go`, `sdk/cursor/`) — Hooks into `sessionStart`, `preToolUse`, `postToolUse`, `sessionEnd`. Gracefully degrades (warns, never blocks) when coordination is unavailable.
- **TypeScript SDK** (`sdk/typescript/`) — `HearsayClient` and `createHooks()` with auto-heartbeat, auto-release, and full test coverage.
- **File watcher** (`internal/watcher/`) — `fsnotify`-based auto-claim on file changes with include/exclude patterns.

---

## What's Solid ✅

All tests pass (`go test ./...`). Core logic is well-structured, covered, and handles edge cases.

| Area | Status | Notes |
|---|---|---|
| Conflict detection matrix | ✅ Good | Well-tested, handles glob patterns and recursive prefixes |
| SQLite provider | ✅ Good | Pure Go, migrations via `CREATE TABLE IF NOT EXISTS`, no CGO |
| PostgreSQL provider | ✅ Good | Standard SQL, tested with contract suite |
| Mailbox messaging | ✅ Good | Clean API, proper DB indexes, TTL expiry on query |
| HTTP server handlers | ✅ Good | Clean separation of concerns, handlers tested |
| TypeScript SDK | ✅ Good | Full Vitest coverage for client, hooks, mailbox, URI |
| Cursor integration | ✅ Good | Never blocks, warns on conflict, auto-releases on tool end |
| State machine / transitions | ✅ Good | Deterministic, tested, treats "ended" as terminal |
| Managed provider proxy | ✅ Good | Standard HTTP client, Bearer auth, timeout handling |

---

## What's Missing ⚠️ ❌

### Security & Authentication

| Gap | Risk | Priority |
|---|---|---|
| **No authentication on `serve`** | Anyone on the network can create, claim, release | 🔴 Critical |
| **No TLS / HTTPS configuration flags** | Credentials and resource URIs fly in plaintext | 🔴 Critical |
| **No API key or token enforcement** | Can't safely expose outside `localhost` or a VPN | 🔴 Critical |
| **No rate limiting** | Easy to accidentally DOS or spam the event log | 🟡 Medium |
| **No input validation / sanitization** on claims | `intent`, `agent_id`, `resource_uri` accept arbitrary strings with no length limits | 🟡 Medium |
| **No audit log** | Can't answer "who claimed what when?" after the fact | 🟡 Medium |
| **Cursor state in `/tmp`** | Session state lost on reboot, not synced across machines | 🟡 Medium |

### Reliability & Operations

| Gap | Risk | Priority |
|---|---|---|
| **No background expiration sweeper** | Expired claims are filtered at query time but never deleted. SQLite table grows forever | 🔴 Critical |
| **`ReleaseExpired` is a no-op** in SQLite/Postgres | Comment says "TODO". DB bloats with stale messages | 🔴 Critical |
| **No graceful shutdown** | `http.ListenAndServe` doesn't handle `SIGTERM`; in-flight claims may be orphaned | 🟡 Medium |
| **No health checks beyond `/health`** | No readiness / liveness differentiation | 🟡 Medium |
| **No metrics / monitoring** | No Prometheus, OpenTelemetry, or structured logging | 🟡 Medium |
| **No request logging middleware** | Can't debug production issues | 🟡 Medium |
| **No database migration framework** | Schema changes = manual `ALTER TABLE` or wipe | 🟡 Medium |
| **No backup/restore guidance** | SQLite is a single file — fine, but no docs | 🟢 Low |

### Multi-Agent & Distribution

| Gap | Risk | Priority |
|---|---|---|
| **No process death detection** | If an agent crashes, its claim lives until TTL expires (up to 5 min) | 🔴 Critical |
| **No clustering** | Multiple `hearsay serve` instances = split brain on shared state | 🔴 Critical |
| **No WebSocket / push notifications** | Mailbox is polling-only (`GET /mailbox`) | 🟡 Medium |
| **Broadcast only works single-node** | `MailboxToBroadcast` has no cross-instance propagation | 🔴 Critical |
| **No A2A protocol support** | Can't interoperate with Google's A2A, LangChain agents, CrewAI, etc. | 🟢 Low (strategic) |

---

## Hardening Checklist

### Phase 1 — "Safe to Expose on the Team VPN" (Days)

- [ ] Add `--tls-cert` / `--tls-key` flags to `serve`
- [ ] Add `--auth-token` / `--auth-bearer` flag and enforce on all mutating endpoints
- [ ] Add request logging middleware (method, path, agent_id, latency)
- [ ] Add a background goroutine that sweeps expired claims every 60s (implement `ReleaseExpired`)
- [ ] Add `status` subcommand (show active claims, mailbox count, uptime)
- [ ] Move Cursor state from `/tmp` to `~/.config/hearsay/cursor/` (or platform equivalent)

### Phase 2 — "Safe for CI/CD and Small Teams" (Weeks)

- [ ] Add schema versioning / migration table (e.g., `schema_migrations`)
- [ ] Add rate limiting per agent_id / IP
- [ ] Add Prometheus metrics endpoint (`/metrics`) for claims, conflicts, mailbox ops
- [ ] Add graceful shutdown with `context.WithTimeout` for in-flight operations
- [ ] Add structured logging (JSON) with configurable level
- [ ] Add agent death detection: heartbeat timeout should auto-expire claims without waiting for TTL
- [ ] Document backup/restore for SQLite and Postgres

### Phase 3 — "Ready for Multi-Tenant SaaS" (Months)

- [ ] Add proper tenant / namespace isolation with authz
- [ ] Add WebSocket support for real-time mailbox push
- [ ] Add clustering / shared state backend (Redis or event log replication)
- [ ] Add audit log table (append-only, non-deletable)
- [ ] Add A2A protocol adapter so Thunder sessions can expose coordination as an A2A skill
- [ ] Add federation: one `hearsay serve` can forward claims to another
- [ ] Load testing and performance benchmarks

---

## Specific Risks to Highlight

### The Expired Claims Problem

Currently, expired claims are **filtered at query time** (`ActiveClaims` checks `CreatedAt + TTL` against `time.Now()`). But the underlying rows in SQLite/Postgres are **never deleted**. Over weeks of CI/CD runs or busy team usage, the `messages` table grows without bound. This is the most pressing operational issue.

**Mitigation today:** Manually vacuum or rotate the SQLite file. **Fix:** Implement `ReleaseExpired` sweeper.

### The Split-Brain Problem

If two developers each run `hearsay serve` pointing at the same Postgres database, everything works. But if they run separate instances (different SQLite files or different Postgres instances), coordination is completely broken — agents can't see each other's claims. There is no federation or gossip protocol.

**Mitigation today:** Use a single shared Postgres instance. **Fix:** Add clustering (Redis pub/sub, or inter-server forwarding).

### The Silent Failure Problem

The Cursor IDE integration is designed to *never block* — if hearsay is down or misconfigured, it silently `allow`s with a warning message. This is the right UX for an IDE plugin, but it means a team might think they're coordinated when they're not.

**Mitigation today:** Watch for the `"⚠️"` warning in agent responses. **Fix:** Add a `status` command to verify connectivity.

---

## Test Coverage Status

```
ok      github.com/gstranger/hearsay/cmd/hearsay              0.563s
ok      github.com/gstranger/hearsay/internal                    (cached)
ok      github.com/gstranger/hearsay/internal/mailbox            2.788s
ok      github.com/gstranger/hearsay/internal/managed            (cached)
ok      github.com/gstranger/hearsay/internal/postgres           (cached)
ok      github.com/gstranger/hearsay/internal/server             1.300s
ok      github.com/gstranger/hearsay/internal/sqlite             (cached)
ok      github.com/gstranger/hearsay/internal/watcher            (cached)
ok      github.com/gstranger/hearsay/pkg/hearsay              1.039s
```

All Go packages tested. TypeScript SDK also has full Vitest coverage. No integration tests for the full stack (CLI → server → provider → DB) yet.

---

## Bottom Line

**hearsay is a well-built v0 tool with a solid foundation.** The core coordination logic is correct, tested, and handles the file-collision problem it was built to solve. But it's currently a "local power tool" — not a service. Before exposing it to untrusted networks, multiple hosts, or public APIs, it needs Phase 1 and Phase 2 hardening at minimum.
