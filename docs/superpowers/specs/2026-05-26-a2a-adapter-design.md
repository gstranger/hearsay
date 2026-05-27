# A2A Adapter for hearsay — Design Document

> Date: 2026-05-26  
> Status: Approved  
> Scope: Initial A2A compliance (Phase 1) — sync tasks only, no streaming

---

## 1. Purpose

Make hearsay discoverable and usable by any A2A-compatible agent. External agents (LangChain, CrewAI, Google ADK, custom agents) can discover hearsay via its Agent Card, delegate resource coordination tasks to it, and receive results through standard A2A JSON-RPC 2.0 over HTTP.

This is **not** a rewrite of hearsay. The existing REST API, CLI, Cursor integration, TypeScript SDK, and filesystem watcher remain untouched. The A2A layer is additive.

---

## 2. Architecture

### 2.1 High-Level

```
┌─────────────────────────────────────────────────────────────┐
│                    hearsay serve                          │
│                                                              │
│  ┌─────────────────┐      ┌─────────────────────────────┐  │
│  │  REST Server    │      │  A2A Server                 │  │
│  │  :8080 (default)│      │  :8081 (or --a2a-addr)      │  │
│  │                 │      │                             │  │
│  │  POST /claim    │      │  GET /.well-known/agent.json│  │
│  │  GET /claims    │      │  POST / (JSON-RPC 2.0)      │  │
│  │  POST /message  │      │    tasks/send               │  │
│  │  ...            │      │    tasks/get                │  │
│  │                 │      │    tasks/cancel             │  │
│  └────────┬────────┘      └─────────────┬───────────────┘  │
│           │                             │                  │
│           └──────────────┬──────────────┘                  │
│                          │                                  │
│                   ┌──────▼──────┐                          │
│                   │  Client     │                          │
│                   │  (claims)   │                          │
│                   └──────┬──────┘                          │
│                          │                                  │
│                   ┌──────▼──────┐                          │
│                   │  Provider   │                          │
│                   │  SQLite/PG  │                          │
│                   └─────────────┘                          │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 Design Rationale

The A2A server runs **alongside** the existing REST server, not replacing it. Both share the same `Provider` and `Client` instances. This preserves:
- Cursor IDE integration (uses REST API)
- TypeScript SDK (uses REST API)
- CLI tools (`claim`, `release`, `query`, etc.)
- Filesystem watcher

The A2A server is **opt-in**. It only starts if `--a2a-addr` is explicitly provided. If omitted, only the REST server runs (backward compatible with existing deployments).

---

## 3. A2A Server Package Structure

```
internal/a2a/
├── server.go       # HTTP server setup, ListenAndServe
├── rpc.go          # JSON-RPC 2.0 envelope: Request, Response, Error
├── router.go       # Method dispatch: tasks/send, tasks/get, tasks/cancel
├── task.go         # Task data model, state machine, transitions
├── task_store.go   # Task persistence via Provider interface additions
├── agentcard.go    # Agent Card generation (static skills, dynamic capabilities)
├── auth.go         # Auth middleware: API key + Bearer with pluggable validator
├── mapper.go       # Maps A2A task params ↔ hearsay ClaimRequest/Release/etc
├── handler_tasks.go # Task method handlers
└── handler_skills.go # Skill execution logic (claim, release, check, mailbox)
```

---

## 4. Agent Card

### 4.1 Endpoint

`GET /.well-known/agent.json` — served by the A2A server.

### 4.2 Generation

The Agent Card is **dynamically generated** on each request (computed from runtime config), but **skills are statically declared**. Dynamic fields:
- `url` — from `--a2a-addr`
- `capabilities` — from config flags
- `authentication.schemes` — from which auth methods are enabled
- `version` — from a compile-time build variable or `go.mod` module version

### 4.3 Capabilities (Phase 1)

```json
{
  "streaming": false,
  "pushNotifications": false,
  "stateTransitionHistory": false
}
```

Streaming, push notifications, and history are deferred to Phase 2.

### 4.4 Authentication Advertisement

```json
{
  "authentication": {
    "schemes": ["api-key", "Bearer"]
  }
}
```

Both schemes are advertised if configured. If only API key is configured, only `"api-key"` is advertised.

### 4.5 Skills

Five skills are declared:

| Skill ID | Purpose | Maps To |
|---|---|---|
| `claim_resource` | Lock a resource | `client.Claim()` |
| `release_resource` | Unlock a claim | `client.Release()` |
| `check_conflict` | Dry-run conflict check | `client.CheckConflict()` |
| `query_mailbox` | Read mailbox messages | `provider.GetMailbox()` |
| `send_mailbox` | Send a message to another agent | `provider.SendMessage()` |

Each skill includes a JSON Schema `parameters` object describing its inputs.

---

## 5. JSON-RPC 2.0 Protocol Layer

### 5.1 Request Format

All A2A requests are `POST /` with:
```json
{
  "jsonrpc": "2.0",
  "id": <number|string>,
  "method": "tasks/send" | "tasks/get" | "tasks/cancel",
  "params": { ... }
}
```

### 5.2 Response Format

Success:
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": { /* Task object */ }
}
```

Error:
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32602,
    "message": "Invalid params",
    "data": { "details": "..." }
  }
}
```

### 5.3 Error Codes

| Code | Meaning | When |
|---|---|---|
| `-32600` | Invalid Request | Malformed JSON-RPC envelope |
| `-32601` | Method not found | Unknown `method` value |
| `-32602` | Invalid params | Missing required param, wrong type, bad skill ID |
| `-32000` | Task not found | `tasks/get` or `tasks/cancel` with unknown task ID |
| `-32001` | Task already in terminal state | `tasks/cancel` on completed/failed/canceled task |
| `-32002` | Conflict detected | `claim_resource` found active conflicting claims |
| `-32003` | Internal error | Unexpected server error |

---

## 6. Task Data Model & State Machine

### 6.1 Task Struct

```go
type Task struct {
    ID        string
    SessionID string
    Status    TaskStatus
    History   []Message
    Artifacts []Artifact
    Metadata  map[string]any

    // Internal: maps to hearsay
    ClaimID   string
    Namespace string
}

type TaskStatus struct {
    State     TaskState
    Message   *Message
    Timestamp time.Time
}

type TaskState string
const (
    TaskSubmitted     TaskState = "submitted"
    TaskWorking       TaskState = "working"
    TaskInputRequired TaskState = "input-required"
    TaskCompleted     TaskState = "completed"
    TaskFailed        TaskState = "failed"
    TaskCanceled      TaskState = "canceled"
    TaskUnknown       TaskState = "unknown"
)
```

### 6.2 State Transitions

```
submitted → working → completed
         ↘ input-required ↗
         ↘ failed
         ↘ canceled
```

Valid transitions:
- `submitted` → `working`, `input-required`, `failed`, `canceled`
- `working` → `input-required`, `completed`, `failed`, `canceled`
- `input-required` → `working`, `canceled`

### 6.3 Storage

Tasks are stored in new tables added to the existing SQLite/Postgres databases:

```sql
CREATE TABLE a2a_tasks (
    id              TEXT PRIMARY KEY,
    session_id      TEXT,
    state           TEXT NOT NULL,
    status_message  TEXT,
    status_time     DATETIME NOT NULL,
    claim_id        TEXT,
    namespace       TEXT NOT NULL,
    metadata        TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE a2a_task_history (
    task_id     TEXT NOT NULL,
    seq         INTEGER NOT NULL,
    role        TEXT NOT NULL,
    parts       TEXT NOT NULL,
    metadata    TEXT,
    timestamp   DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, seq)
);

CREATE TABLE a2a_artifacts (
    task_id     TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT,
    parts       TEXT NOT NULL,
    index_num   INTEGER,
    append      BOOLEAN DEFAULT FALSE,
    last_chunk  BOOLEAN DEFAULT FALSE,
    metadata    TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, name, index_num)
);
```

---

## 7. Skill Implementations

### 7.1 `claim_resource`

**Input params (from `message.parts.data`):**
```json
{
  "resource_uri": "file://src/api.go",
  "operation": "write",
  "intent": "refactoring error handling",
  "agent_id": "claude-session-abc",
  "ttl_seconds": 300
}
```

**Execution:**
1. Validate required fields: `resource_uri`, `operation`, `agent_id`
2. `client.Claim(ctx, ClaimRequest{...})`
3. On success → Task `completed`, artifact: `{ claim_id, status: "granted" }`
4. On conflict → Task `failed`, artifact: full `ConflictReport`, error code `-32002`
5. Store `claim_id` in Task metadata for cancel/release

**State transitions:** `submitted` → `working` → `completed` or `failed`

### 7.2 `release_resource`

**Input params:**
```json
{
  "claim_id": "claim_12345",
  "outcome": "succeeded"
}
```

**Execution:**
1. `client.Release(ctx, claimID, outcome)`
2. Task `completed`

### 7.3 `check_conflict`

**Input params:**
```json
{
  "resource_uri": "file://src/api.go",
  "operation": "write"
}
```

**Execution:**
1. `client.CheckConflict(ctx, resourceURI, operation)`
2. Task `completed`, artifact: `ConflictReport`

### 7.4 `query_mailbox`

**Input params:**
```json
{
  "agent_id": "claude-session-abc",
  "unread_only": true
}
```

**Execution:**
1. `provider.GetMailbox(ctx, namespace, agentID, opts)`
2. Task `completed`, artifact: `[]MailboxMessage`

### 7.5 `send_mailbox`

**Input params:**
```json
{
  "to": "cursor-session-def",
  "type": "yield_request",
  "content": "I need src/api.go, can you release your claim?",
  "related_claim_id": "claim_12345"
}
```

**Execution:**
1. Validate `type` against `ValidMailboxTypes`
2. `provider.SendMessage(ctx, namespace, MailboxMessage{...})`
3. Task `completed`, artifact: `{ message_id }`

### 7.6 Cancel Flow

When `tasks/cancel` is called on a `working` task with an active `claim_id`:
1. `client.Release(ctx, claimID, OutcomeAbandoned)`
2. Task status → `canceled`

If task is already terminal → error `-32001`.

---

## 8. Authentication

### 8.1 Configuration

```toml
[a2a]
addr = "localhost:8081"

# Enable API key scheme
api_key = "ak_live_..."

# Enable Bearer scheme with external validator
bearer_validator_url = "https://auth.example.com/verify"

# OR enable Bearer with built-in JWT validation
bearer_jwks_url = "https://auth.example.com/.well-known/jwks.json"
```

### 8.2 API Key

- Header: `X-Api-Key: <key>`
- Server checks against configured `--a2a-api-key`
- Stateless, single-tenant

### 8.3 Bearer Token

- Header: `Authorization: Bearer <token>`
- Pluggable validation:
  - **External URL:** POST token to validator URL. Expect `200` with `{ "valid": true, "sub": "agent-id" }`.
  - **JWT (built-in):** Validate signature against JWKS, extract `sub` claim as `agent_id`.
Only one Bearer validator can be active. If both `bearer_validator_url` and `bearer_jwks_url` are configured, `bearer_jwks_url` (built-in JWT) takes precedence.

### 8.4 Middleware Order

1. Parse `X-Api-Key` (if configured)
2. Parse `Authorization: Bearer` (if configured)
3. If neither matches → JSON-RPC error response with code `-32600`

---

## 9. Data Flow

### 9.1 `tasks/send` — Claim Resource

```
A2A Client                     A2A Server                    hearsay Core
    |                              |                              |
    |-- POST / (tasks/send) ------> |                              |
    |   { skill: claim_resource }  |                              |
    |                              |-- auth check ---------------->|
    |                              |                              |
    |                              |-- client.Claim() ----------->|
    |                              |                              |
    |<-- JSON-RPC response --------|                              |
    |   { result: Task(completed) }|                              |
```

### 9.2 `tasks/cancel` — Active Claim

```
A2A Client                     A2A Server                    hearsay Core
    |                              |                              |
    |-- POST / (tasks/cancel) ---> |                              |
    |                              |-- lookup Task by ID -------->|
    |                              |                              |
    |                              |-- client.Release() --------->|
    |                              |   (OutcomeAbandoned)         |
    |                              |                              |
    |<-- JSON-RPC response --------|                              |
    |   { result: Task(canceled) } |                              |
```

---

## 10. Out of Scope (Phase 2+)

| Feature | Deferred To |
|---|---|
| `tasks/sendSubscribe` (SSE streaming) | Phase 2 |
| `tasks/resubscribe` | Phase 2 |
| Push notifications (webhooks) | Phase 2 |
| `capabilities.stateTransitionHistory` | Phase 2 |
| Multi-part messages with `FilePart` | Phase 2 |
| Streaming artifacts (`append`, `lastChunk`) | Phase 2 |
| `input-required` interactive state | Phase 2 |
| OAuth 2.0 authorization server | Phase 2 |
| Agent registry / directory | Phase 2 |
| Thunder session A2A interop | Separate project |

---

## 11. Testing Strategy

### 11.1 Unit Tests

- `internal/a2a/rpc_test.go` — JSON-RPC envelope encode/decode
- `internal/a2a/task_test.go` — State machine transitions
- `internal/a2a/auth_test.go` — Auth middleware (API key, Bearer, missing)
- `internal/a2a/handler_test.go` — Each skill handler with mocked Client/Provider

### 11.2 Integration Tests

- Spin up `hearsay serve --a2a-addr :8081` with SQLite
- Send JSON-RPC requests via HTTP client
- Verify Agent Card, task lifecycle, claim→conflict→release flow
- Test auth rejection with bad API key / Bearer token

### 11.3 Contract Tests

- Add A2A task operations to existing `internal/contract.go` provider contract suite

---

## 12. CLI Integration

### 12.1 New Flags for `serve`

```
hearsay serve \
  --addr localhost:8080 \
  --a2a-addr localhost:8081 \
  --a2a-api-key ak_live_xxx \
  --a2a-bearer-jwks-url https://auth.example.com/.well-known/jwks.json
```

### 12.2 Config File Additions

```toml
[a2a]
  addr = "localhost:8081"
  api_key = "ak_live_xxx"
  bearer_jwks_url = "https://auth.example.com/.well-known/jwks.json"
```

---

## 13. Risks & Mitigations

| Risk | Mitigation |
|---|---|
| A2A spec evolves, our implementation drifts | Keep A2A package isolated; spec changes only affect `internal/a2a/` |
| REST API accidentally broken | A2A server is separate binary entry point or separate port; no shared router |
| Auth misconfiguration leaves server open | Require explicit `--a2a-api-key` or `--a2a-bearer-*` to enable A2A server; fail closed |
| Task table grows unbounded | Same as existing claim expiry problem — sweeper needed for both (see PRODUCTION_READINESS.md) |
| Performance: JSON-RPC overhead | Negligible for coordination tasks (not high-throughput); monitor with integration tests |

---

## 14. Success Criteria

- [ ] `GET /.well-known/agent.json` returns valid Agent Card with 5 skills
- [ ] `tasks/send` with `claim_resource` creates a claim, returns completed Task
- [ ] `tasks/send` with `release_resource` releases a claim
- [ ] `tasks/send` with `check_conflict` returns ConflictReport without side effects
- [ ] `tasks/send` with `query_mailbox` returns mailbox messages
- [ ] `tasks/send` with `send_mailbox` delivers a message
- [ ] `tasks/get` retrieves any task by ID with correct state
- [ ] `tasks/cancel` on a working task releases the claim and marks canceled
- [ ] Auth rejects unauthenticated requests
- [ ] All existing REST API tests still pass (no regression)
- [ ] New A2A integration tests pass

---

*End of design document*
