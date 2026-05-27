# Design: Configurable Audit Log

**Date:** 2026-05-26
**Scope:** Append-only audit log with configurable verbosity levels.

## Motivation

Currently there's no way to answer "who claimed what when?" after the fact. An audit log provides compliance, debugging, and operational visibility.

## Design

### Audit Levels

| Level | Captures |
|---|---|
| `off` | Nothing (default, backward compatible) |
| `coordination` | Claims, releases, claim expirations, death detections |
| `security` | All above + auth failures, rate limit hits |
| `full` | All above + heartbeats, mailbox sends, namespace create/delete, A2A task lifecycle |

### Storage

New `audit_events` table in SQLite/Postgres:

```sql
CREATE TABLE IF NOT EXISTS audit_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace TEXT NOT NULL,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    level TEXT NOT NULL,        -- 'coordination','security','full'
    event_type TEXT NOT NULL,   -- 'claim','release','auth_failure','rate_limit',...
    agent_id TEXT,
    resource_uri TEXT,
    outcome TEXT,
    metadata TEXT               -- JSON blob for event-specific data
);
```

### How It Works

1. **`internal/server/audit.go`** — `AuditLogger` struct with `Log(event)` method. Filters by level (logs only if event level <= configured level).

2. **Wired into existing handlers** — each handler calls `audit.Log(...)` after processing. Mutating POST handlers (claim, release, heartbeat, message) produce coordination events. Auth/rate-limit middleware produces security events.

3. **Background persistence** — audit events are appended to the provider via a buffered channel + goroutine (non-blocking for request handlers).

### CLI

| Flag | Default | Description |
|---|---|---|
| `--audit-level` | `off` | Audit verbosity: `off`, `coordination`, `security`, `full` |

### Querying

```bash
hearsay audit --resource file://src/api.go --since 2026-05-26
```

(New `hearsay audit` subcommand, Phase 2 — for now, query via SQL directly)

### Files

| File | Purpose |
|---|---|
| `internal/server/audit.go` | **NEW** — AuditLogger struct, event types, level filtering |
| `internal/server/audit_test.go` | **NEW** — Tests |
| `internal/server/server.go` | Inject AuditLogger into handlers |
| `internal/server/middleware.go` | Auth/rate-limit middleware produces audit events |
| `internal/sqlite/sqlite.go` | Add `audit_events` table migration, `AppendAudit` method |
| `internal/postgres/postgres.go` | Same for Postgres |
| `pkg/hearsay/provider.go` | Add `AppendAudit` to Provider interface |
| `cmd/hearsay/main.go` | Add `--audit-level` flag |
| `README.md` | Document |

### Out of Scope
- `hearsay audit` subcommand (Phase 2)
- Audit log retention/rotation
- Tamper-proof audit chain (PGP/HMAC)

## Verification
- `--audit-level off` → no events written
- `--audit-level coordination` → claim/release events written
- `--audit-level security` → claim/release + auth failures written
- `--audit-level full` → all events written