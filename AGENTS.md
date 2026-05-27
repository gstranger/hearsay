# AGENTS.md

> Guidance for AI coding agents working on the `hearsay` codebase.

---

## Project Overview

**hearsay** is a lightweight, embeddable coordination layer for AI agents. It prevents file conflicts when multiple agents work on the same codebase by tracking **resource claims** — who owns what, for how long, and why. Agents communicate via an inter-agent **mailbox** and can coordinate through the **Google A2A protocol**.

It ships as a Go CLI binary, an embeddable Go library, a TypeScript SDK, and IDE extensions (Cursor, pi).

---

## Architecture

### Layered Design

```
┌─────────────────────────────────────────────────┐
│  IDE Extensions  │  TypeScript SDK  │  A2A      │
│  (Cursor, pi)    │  (REST client)   │  Agents   │
├──────────────────┼──────────────────┼───────────┤
│  CLI (cmd/hearsay)                    │ REST API │
│  - init/claim/release/query/serve/  │ :8080    │
│    watch/cursor/status              │ A2A :8081│
├─────────────────────────────────────┴───────────┤
│               pkg/hearsay (Public API)           │
│  Client  │  Claims  │  Mailbox  │  Config       │
│  Provider interface  │  Validation            │
├─────────────────────────────────────────────────┤
│               internal/ (Private)                │
│  a2a/  │  server/  │  mailbox/  │  watcher/     │
│  sqlite/ │ postgres/ │ managed/ │  memory/      │
└─────────────────────────────────────────────────┘
```

### The Provider Pattern

The central abstraction is the `hearsay.Provider` interface defined in `pkg/hearsay/provider.go`. It defines an append-only event log with methods for:

| Method Group | Purpose |
|---|---|
| `CreateNamespace` / `DeleteNamespace` | Multi-tenant isolation |
| `Append` / `Query` / `Subscribe` | Event sourcing — claims, releases, heartbeats are immutable messages |
| `ActiveClaims` / `AgentState` / `ReleaseExpired` | Derived state from the event log |
| `SendMessage` / `GetMailbox` / `MarkRead` / `ArchiveMessage` | Inter-agent mailbox |
| `CreateTask` / `GetTask` / `UpdateTask` | A2A task state storage |

Three backend implementations implement this interface:

| Backend | Package | When to use |
|---|---|---|
| **SQLite** | `internal/sqlite/` | Local dev, single machine. Pure Go (modernc.org/sqlite), no CGO |
| **PostgreSQL** | `internal/postgres/` | Team/shared state, production |
| **Managed** | `internal/managed/` | Thin HTTP proxy to a remote `hearsay serve` instance |
| **Memory** | `internal/memory/` | Tests only — in-memory, no persistence |

All providers pass a shared **contract test suite** (`internal/contract.go`) that validates the `Provider` interface contract.

### Event Sourcing Model

Claims are **not** mutable records. Instead:

1. A `MsgClaim` message is appended to the log
2. `MsgHeartbeat` messages keep the claim alive (extend TTL)
3. A `MsgRelease` (or `MsgTransfer`) message terminates the claim

`ActiveClaims()` derives current state by replaying messages and computing which claims are still active (not released + not expired + heartbeat-extended TTL). This is an O(M) single-pass algorithm in `FilterActiveClaims()` (`pkg/hearsay/activeclaims.go`).

### Conflict Detection

In `pkg/hearsay/claims.go`, `CheckConflict()` computes conflicts using a matrix:

| Operation | Conflicts With |
|---|---|
| `read` | `delete`, `rename` |
| `write` | `write`, `delete`, `rename`, `refactor` |
| `delete` | `read`, `write`, `delete`, `rename`, `refactor` |
| `rename` | everything |
| `refactor` | everything |

Resource matching supports exact, glob (`*`), and prefix (`/**`) patterns.

### Dual API Surface

The server (`internal/server/`) runs on **two ports**:

1. **REST API** (`:8080`) — Used by CLI, TypeScript SDK, IDE extensions. Read-only endpoints are unauthenticated; mutating endpoints can require `Authorization: Bearer <token>` or `X-Api-Key`.

2. **A2A JSON-RPC** (`:8081`) — Google A2A protocol compliance. Exposes an Agent Card at `/.well-known/agent.json` and three RPC methods (`tasks/send`, `tasks/get`, `tasks/cancel`) mapped to 5 skills (`claim_resource`, `release_resource`, `check_conflict`, `query_mailbox`, `send_mailbox`).

---

## Repository Layout

```
hearsay/
├── cmd/hearsay/           # CLI entrypoint (main.go + cursor.go)
│   ├── main.go            # All CLI subcommands: init, claim, release, heartbeat,
│   │                      #   query, check, namespace, serve, watch, cursor, status
│   └── cursor.go          # Cursor IDE hook integration
│
├── pkg/hearsay/           # PUBLIC API — safe to import from external Go projects
│   ├── provider.go        # Provider interface (the main abstraction)
│   ├── client.go          # Client: claim, release, heartbeat, transfer, query, conflict check
│   ├── claims.go          # Claim types, Conflict detection, Operation conflict matrix
│   ├── activeclaims.go    # FilterActiveClaims() — derives active state from event log
│   ├── mailbox.go         # Mailbox message types (yield_request, escalation, ping, etc.)
│   ├── config.go          # TOML config loader (Config, LoadConfig, Save)
│   └── validation.go      # Input validation (agent IDs, resource URIs, intents)
│
├── internal/              # PRIVATE — implementation details, not for external import
│   ├── contract.go        # Provider contract test suite (shared across backends)
│   ├── provider_test.go   # Runs contract tests against all backends
│   │
│   ├── a2a/               # Google A2A protocol implementation
│   │   ├── server.go      # HTTP handler routing: Agent Card + JSON-RPC dispatch
│   │   ├── task.go        # Task model, state machine (submitted→working→completed/failed)
│   │   ├── agentcard.go   # Agent Card generator with 5 skills
│   │   ├── auth.go        # Auth middleware: API key, JWT/JWKS, external Bearer validator
│   │   ├── handler_tasks.go  # tasks/send, tasks/get, tasks/cancel RPC handlers
│   │   ├── handler_skills.go # Skill execution: claim_resource, release_resource, etc.
│   │   ├── rpc.go         # JSON-RPC 2.0 request/response types
│   │   └── mapper.go      # Maps A2A skill params ↔ hearsay domain types
│   │
│   ├── server/            # REST HTTP server + middleware
│   │   ├── server.go      # Route registration, handlers for /claim, /release, /mailbox, etc.
│   │   ├── mailbox_handlers.go  # Mailbox HTTP handlers
│   │   ├── middleware.go   # AuthMiddleware (Bearer/API key), LoggingMiddleware (text/JSON)
│   │   ├── ratelimit.go    # Per-agent token bucket rate limiter
│   │   └── tls.go          # Self-signed TLS certificate generation for --tls-auto
│   │
│   ├── mailbox/           # Mailbox business logic
│   │   └── mailbox.go     # Mailbox orchestrator
│   │
│   ├── watcher/           # Filesystem watcher
│   │   └── watcher.go     # fsnotify-based file change detection → auto-claims
│   │
│   ├── sqlite/            # SQLite provider
│   │   └── sqlite.go      # Schema (6 tables), all Provider interface methods
│   │
│   ├── postgres/          # PostgreSQL provider
│   │   └── postgres.go    # Same Provider contract, PostgreSQL dialect
│   │
│   ├── managed/           # Remote managed provider (HTTP proxy)
│   │   └── managed.go     # Forwards Provider calls to a remote hearsay server
│   │
│   └── memory/            # In-memory provider (tests only)
│       └── memory.go      # Maps + slices, no persistence
│
├── sdk/                   # Client SDKs for non-Go consumers
│   ├── typescript/        # TypeScript SDK (npm package)
│   │   ├── src/client.ts  # HearsayClient: claim, release, heartbeat, mailbox, query
│   │   ├── src/hooks.ts   # Auto-heartbeat + auto-release hooks for agent loops
│   │   ├── src/types.ts   # TypeScript type definitions
│   │   ├── src/uri.ts     # Resource URI helpers
│   │   └── src/index.ts   # Package exports
│   │
│   ├── pi-extension/      # pi coding agent extension
│   │   └── hearsay.ts     # Auto-starts hearsay serve, claims on tool calls, releases on results
│   │
│   ├── cursor/            # Cursor IDE integration
│   │   └── hooks.json     # Hook definitions: sessionStart, preToolUse, postToolUse, sessionEnd
│   │
│   └── opencode-extension/ # OpenCode IDE extension (similar to pi extension)
│
├── tests/
│   └── integration/       # End-to-end integration tests
│       └── a2a_test.go    # Full-stack A2A tests
│
├── docs/                  # Documentation
│   └── superpowers/       # Superpowers plans and specs
│
├── skills/                # AI agent skills for coordination
│   └── coordination-etiquette/
│
├── .hearsay.toml          # Local config (gitignored)
├── .hearsay.db            # Local SQLite database (gitignored)
├── go.mod / go.sum        # Go module definition
├── README.md              # User-facing documentation
├── AGENTS.md              # This file — guidance for AI coding agents
├── PRODUCTION_READINESS.md # Honest assessment of production gaps
└── A2A_GAP_ANALYSIS.md    # Detailed A2A protocol compliance analysis
```

---

## How the Application Works

### Claim Lifecycle

```
Agent calls hearsay.Client.Claim(resource, op, intent)
  │
  ├─ 1. Query ActiveClaims for the resource
  ├─ 2. Build new Claim struct, run CheckConflict()
  ├─ 3. If conflict: return ConflictError + ConflictReport (HTTP 409 or 423)
  │     • Locking mode: HTTP 423 with error details
  │     • Non-locking mode: HTTP 409, agent decides to proceed or back off
  └─ 4. If no conflict: append MsgClaim message to event log → return claim_id

Agent keeps claim alive with Heartbeat(claimID)
  → Appends MsgHeartbeat, extending effective TTL

Agent finishes work: Release(claimID, outcome)
  → Appends MsgRelease, marking claim as terminated

Background sweeper (every 60s):
  → ReleaseExpired() deletes claims whose TTL+heartbeat has expired
```

### A2A Task Flow

```
External A2A agent sends tasks/send with skill_id="claim_resource"
  │
  ├─ Auth check (API key, JWT/JWKS, or external Bearer validator)
  ├─ Create Task (state=submitted)
  ├─ Transition to working
  ├─ Execute skill (claim_resource → calls client.Claim())
  ├─ If success: transition to completed, attach claim_result artifact
  ├─ If conflict: return as completed with conflict-report artifact
  └─ If error: transition to failed
```

### Server Startup (`hearsay serve`)

```
1. Load .hearsay.toml config
2. Initialize provider (SQLite/Postgres/Managed)
3. Auto-create namespace if needed
4. Start REST server on --addr (default :8080)
5. Start A2A server on --a2a-addr if configured (default disabled)
6. Start background sweeper (60s interval)
7. Handle graceful shutdown on SIGINT/SIGTERM
```

---

## Key Design Decisions

### Claims Are Immutable Events, Not Mutable State

The system uses an **append-only event log** (`messages` table). Claims are never modified in place. Instead, heartbeat and release messages extend or terminate claims. This gives us:
- Full audit trail (every claim, heartbeat, release is a message)
- Easy time-travel queries
- Eventual consistency between providers

### Dual API Design (REST + A2A)

The REST API is simple and pragmatic for internal agents and SDKs. The A2A JSON-RPC API provides interoperability with the broader agent ecosystem. They share the same provider/client layer underneath.

### Providers Are Swappable at Runtime

Everything above `Provider` depends only on the interface, not concrete implementations. Changing from SQLite to Postgres requires only a config change — no code changes. The contract test suite ensures all providers behave identically.

### IDE Extensions Are Non-Blocking

Cursor and pi extensions are designed to **never block** agent execution. On conflict, they either allow with a warning or return an advisory message. This is intentional UX — better to warn than to deadlock an agent session.

---

## Development

### Prerequisites

- Go 1.25+
- Node.js 18+ (for SDK tests)
- PostgreSQL (optional, for Postgres provider tests)

### Common Commands

```bash
# Run all Go tests
go test ./...

# Run with race detection
go test -race ./...

# Build the binary
go build ./cmd/hearsay

# Run locally with both servers
./hearsay serve --a2a-addr localhost:8081

# Run with auth and JSON logging
./hearsay serve --auth-token dev-secret --log-format json

# Watch a directory and auto-claim changes
./hearsay watch --path ./src

# TypeScript SDK tests
cd sdk/typescript && npm test
```

### Testing Strategy

| Layer | How tested |
|---|---|
| Provider interface contract | `internal/contract.go` — shared test suite run against all backends |
| Business logic (claims, conflicts, active claims) | Unit tests in `pkg/hearsay/` |
| HTTP handlers | Tests in `internal/server/server_test.go` |
| A2A protocol | Tests in `internal/a2a/`, integration test in `tests/integration/` |
| TypeScript SDK | Vitest tests in `sdk/typescript/test/` |
| CLI commands | Tests in `cmd/hearsay/cursor_test.go` |

### Adding a New Provider

1. Create a new package in `internal/` (e.g., `internal/d1/`)
2. Implement the `hearsay.Provider` interface
3. Add a test file that calls `internal.RunProviderContractTests()`
4. Register the provider in `cmd/hearsay/main.go`'s provider loading functions
5. Add config support in `pkg/hearsay/config.go`

### Adding a New A2A Skill

1. Add a skill definition in `internal/a2a/agentcard.go` (add to `Skills` array in `GenerateAgentCard`)
2. Add a new `skillXxx()` method in `internal/a2a/handler_skills.go`
3. Add the `case` in `executeSkill()`
4. Add parameter mapping in `internal/a2a/mapper.go`

---

## Code Conventions

- **Go package naming**: `pkg/hearsay` for public API, `internal/` for implementation details
- **Error types**: Custom errors defined in `pkg/hearsay/claims.go` (`ConflictError`, `ExpiredError`, `NotFoundError`, `ProviderError`)
- **Message types**: Enum constants in `pkg/hearsay/provider.go` (`MsgClaim`, `MsgHeartbeat`, `MsgRelease`, etc.)
- **Operation types**: Enum constants in `pkg/hearsay/claims.go` (`OpRead`, `OpWrite`, `OpDelete`, `OpRename`, `OpRefactor`)
- **SQL schema**: Declared inline in `internal/sqlite/sqlite.go` using `CREATE TABLE IF NOT EXISTS`
- **TOML config**: Uses `github.com/BurntSushi/toml` for both reading and writing