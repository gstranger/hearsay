# Production Readiness Assessment

> Last updated: 2026-05-26  
> Version assessed: Post-Phase 1 hardening

Honest assessment of what is solid enough for production and what gaps remain.

---

## TL;DR

| Use Case | Ready? |
|---|---|
| Single developer, local SQLite, localhost | ✅ Yes |
| Small team, shared Postgres, trusted network/VPN | ✅ Yes |
| CI/CD pipeline claiming files (behind VPN) | ✅ Yes |
| Public / internet-facing service | ⚠️ Yes, with TLS + auth enabled |
| Multi-tenant SaaS | ❌ No |
| Large team (10+ agents, multiple hosts) | ❌ No |
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

### Storage Backends (3 providers)

- **SQLite** — Local file, default. Pure Go, no CGO.
- **PostgreSQL** — Shared database for teams.
- **Managed** — HTTP client proxying to a remote `hearsay serve`.

### HTTP Server

- REST API on `:8080` with optional TLS, auth, rate limiting, audit logging
- A2A JSON-RPC server on configurable port with JWT/API key auth
- 5 A2A skills: claim_resource, release_resource, check_conflict, query_mailbox, send_mailbox

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
| CI/CD | ✅ | GitHub Actions: lint, test, build |

---

## What's Still Missing ⚠️ ❌

### Distribution & Scale

| Gap | Risk | Priority |
|---|---|---|
| **No clustering** | Multiple `hearsay serve` instances = split brain | 🔴 Critical |
| **Broadcast only works single-node** | No cross-instance propagation | 🔴 Critical |
| **No WebSocket / push notifications** | Mailbox is polling-only | 🟡 Medium |

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
| **No tenant / namespace isolation with authz** | One agent can query another namespace's claims | 🔴 Critical |
| **No load testing / benchmarks** | Unknown performance limits | 🟡 Medium |
| **No federation** | Cannot forward claims between servers | 🟢 Low |

---

## Remaining Hardening Checklist

### Phase 2 — "Ready for Team CI/CD" (Weeks)

- [ ] Add Prometheus metrics endpoint (`/metrics`)
- [ ] Add `hearsay audit` subcommand for querying audit log
- [ ] Add schema versioning / migration table
- [ ] Document backup/restore for SQLite and Postgres

### Phase 3 — "Ready for Multi-Tenant SaaS" (Months)

- [ ] Add proper tenant / namespace isolation with authz
- [ ] Add WebSocket support for real-time mailbox push
- [ ] Add clustering / shared state backend (Redis or event log replication)
- [ ] Add federation: one `hearsay serve` can forward claims to another
- [ ] Load testing and performance benchmarks

---

## Specific Risks to Highlight

### The Split-Brain Problem

If two developers run separate `hearsay serve` instances (different SQLite files or different Postgres instances), coordination is broken — agents can't see each other's claims.

**Mitigation:** Use a single shared Postgres instance. **Fix:** Add clustering (Redis pub/sub, or inter-server forwarding).

### The Silent Failure Problem

IDE integrations are designed to never block — if hearsay is down, they silently `allow` with a warning. Team may think they're coordinated when they're not.

**Mitigation:** Use `hearsay status` to verify connectivity.

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

11 packages, all passing. TypeScript SDK has full Vitest coverage. GitHub Actions CI runs on every push/PR.

---

## Bottom Line

**hearsay is solid for single-instance team use.** With TLS + auth enabled, it's safe to expose on a shared network. The core coordination logic, A2A interop, security hardening, and observability are in place. Remaining gaps are clustering (split-brain), push notifications, and multi-tenant isolation — blockers for SaaS but not for team CI/CD or single-server deployments.