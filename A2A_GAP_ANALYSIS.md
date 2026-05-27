# A2A Gap Analysis: agentstate vs. Google's Agent-to-Agent Protocol

> Date: 2026-05-26  
> A2A Protocol Version: 1.0 (released April 2025)  
> agentstate Version: v0.1 (May 2026)

---

## Executive Summary

**agentstate is not an A2A implementation, nor is it close to being one.**

The gap is not a matter of adding a few endpoints or renaming fields. A2A and agentstate solve fundamentally different problems at different layers of the agent stack:

| | **A2A** | **agentstate** |
|---|---|---|
| **Problem** | "How do I ask another agent to do work?" | "How do I prevent agents from colliding on the same file?" |
| **Primitive** | Task delegation with rich messages | Resource claims with conflict detection |
| **Protocol** | JSON-RPC 2.0 over HTTP | Plain REST JSON over HTTP |
| **State model** | Task lifecycle (submitted → working → completed/failed) | Claim lifecycle (claim → heartbeat → release) |
| **Message model** | Multi-part messages (text, file, data) | Single string `content` field |
| **Discovery** | Agent Cards at `/.well-known/agent.json` | None — hardcoded endpoint |
| **Streaming** | Native SSE (`tasks/sendSubscribe`) | Polling only (`GET /mailbox`, `GET /query`) |
| **Outputs** | Artifacts (named, multi-part, streamable) | None |

**To make agentstate an A2A implementation would require a near-total rewrite of the API surface, data model, and protocol layer.** The two tools are complementary, not overlapping.

---

## Side-by-Side: What A2A Requires vs. What agentstate Has

### 1. Discovery & Agent Card

| A2A Requirement | agentstate Status | Gap |
|---|---|---|
| `GET /.well-known/agent.json` endpoint | ❌ **Missing entirely** | Must implement Agent Card schema with `name`, `description`, `url`, `version`, `capabilities`, `skills`, `authentication` |
| `capabilities.streaming` boolean | ❌ Missing | Must advertise whether SSE streaming is supported |
| `capabilities.pushNotifications` boolean | ❌ Missing | Must advertise webhook push support |
| `capabilities.stateTransitionHistory` boolean | ❌ Missing | Must advertise whether full task history is retained |
| `skills[]` array with `id`, `name`, `description`, `tags`, `examples`, `inputModes`, `outputModes`, `parameters` | ❌ Missing | Must describe what the agent can do. agentstate has no "skills" concept |
| `authentication.schemes[]` | ⚠️ Partial concept | agentstate has a `token` field for managed provider, but no standardized auth advertisement |

**Effort estimate:** Medium — new endpoint, new schema, but straightforward.

---

### 2. Protocol Layer

| A2A Requirement | agentstate Status | Gap |
|---|---|---|
| **JSON-RPC 2.0** envelope (`jsonrpc`, `id`, `method`, `params` / `result` / `error`) | ❌ **Wrong protocol** | agentstate uses plain REST JSON. Every request and response would need to be wrapped in JSON-RPC 2.0 |
| `id` correlation (client-provided request ID echoed in response) | ❌ Missing | agentstate REST responses don't correlate to request IDs |
| Standardized error codes (`-32602` Invalid params, `-32000` Task not found, etc.) | ❌ Missing | agentstate uses ad-hoc HTTP status codes + plain text or JSON bodies |
| Single POST endpoint (`POST /`) dispatching by `method` field | ❌ Wrong routing | agentstate uses path-based routing (`POST /claim`, `GET /claims`, etc.) |

**Effort estimate:** Large — requires replacing the entire HTTP routing and request/response serialization layer.

---

### 3. Task Lifecycle

| A2A Requirement | agentstate Status | Gap |
|---|---|---|
| Task object with `id`, `sessionId`, `status`, `history`, `artifacts`, `metadata` | ❌ **Missing entirely** | agentstate has no "Task" concept. It has Claims, Messages, and MailboxMessages |
| Task states: `submitted`, `working`, `input-required`, `completed`, `failed`, `canceled`, `unknown` | ❌ Missing | agentstate has no state machine for tasks |
| `TaskStatus` object with `state`, `message`, `timestamp` | ❌ Missing | No status tracking beyond claim active/inactive |
| State transitions with validation | ❌ Missing | No transition rules |
| `history[]` — full message history within a task | ❌ Missing | agentstate has an event log, but it's not scoped to a task and doesn't include message parts |
| `metadata` — arbitrary task-level key-value data | ❌ Missing | No task-level metadata |

**Effort estimate:** Very Large — requires new data model, new state machine, new storage schema.

---

### 4. Messages & Parts

| A2A Requirement | agentstate Status | Gap |
|---|---|---|
| `Message` object with `role` (`"user"` / `"agent"`), `parts[]`, `metadata` | ❌ **Wrong model** | agentstate `MailboxMessage` has `from`, `to`, `type`, `content` (single string). No `role`, no `parts`, no `metadata` |
| `TextPart` — `{ type: "text", text: "..." }` | ❌ Missing | agentstate `content` is a plain string, not a typed part |
| `FilePart` — `{ type: "file", file: { name, mimeType, bytes | uri } }` | ❌ Missing | No file upload/download in messages |
| `DataPart` — `{ type: "data", data: { ... } }` | ❌ Missing | No structured data parts |
| Multi-part messages (text + file + data in one message) | ❌ Missing | agentstate messages are single-string only |

**Effort estimate:** Large — requires redesigning the MailboxMessage schema and all handlers.

---

### 5. Artifacts

| A2A Requirement | agentstate Status | Gap |
|---|---|---|
| `Artifact` object with `name`, `description`, `parts[]`, `index`, `append`, `lastChunk`, `metadata` | ❌ **Missing entirely** | agentstate has no concept of task outputs or deliverables |
| Named artifacts (multiple per task) | ❌ Missing | No artifact storage |
| Streaming artifacts (`append: true`, `lastChunk: false/true`) | ❌ Missing | No incremental output delivery |
| Artifact parts using same Part types as Messages (text, file, data) | ❌ Missing | Would need Part system first |

**Effort estimate:** Large — new storage, new API, new streaming logic.

---

### 6. Required HTTP Endpoints

| A2A Endpoint | agentstate Equivalent | Gap |
|---|---|---|
| `tasks/send` — submit task, block until terminal | ❌ No equivalent | agentstate has no task submission. Closest is `POST /claim` which is completely different |
| `tasks/sendSubscribe` — submit task, stream SSE | ❌ No equivalent | agentstate has no SSE streaming. `Subscribe()` in provider does polling (500ms sleep loop) |
| `tasks/get` — retrieve task state + history | ❌ No equivalent | Closest is `GET /claims` or `GET /mailbox`, neither returns task state |
| `tasks/cancel` — cancel a non-terminal task | ❌ No equivalent | Closest is `POST /release` on a claim, but claims are not tasks |

**Effort estimate:** Very Large — four entirely new endpoints with new semantics.

---

### 7. Streaming (SSE)

| A2A Requirement | agentstate Status | Gap |
|---|---|---|
| `Content-Type: text/event-stream` | ❌ Missing | agentstate returns `application/json` |
| SSE event types: `task-status-update`, `task-artifact-update`, `close` | ❌ Missing | No SSE infrastructure |
| Each SSE `data:` line contains a complete JSON-RPC response | ❌ Missing | Would need SSE encoder + JSON-RPC wrapper |
| `capabilities.streaming` must be `true` to advertise this | ❌ Missing | See Discovery gap |

**Effort estimate:** Medium — Go has good SSE libraries, but needs integration with task lifecycle.

---

### 8. Push Notifications

| A2A Requirement | agentstate Status | Gap |
|---|---|---|
| Accept `pushNotification` config in task requests (`url`, `token`) | ❌ Missing | No webhook support |
| POST to client webhook on status changes | ❌ Missing | No outbound HTTP callbacks |
| Include verification `token` in push payloads | ❌ Missing | No auth on callbacks |
| `capabilities.pushNotifications` advertisement | ❌ Missing | See Discovery gap |

**Effort estimate:** Medium — new background worker, HTTP client, retry logic.

---

### 9. Authentication

| A2A Requirement | agentstate Status | Gap |
|---|---|---|
| OAuth 2.0 Bearer token (`Authorization: Bearer <token>`) | ⚠️ Partial | Managed provider sends `Bearer` header, but server doesn't validate incoming auth |
| API key scheme with configurable header name (`credentials` field) | ❌ Missing | No API key support on server |
| Auth advertised in Agent Card | ❌ Missing | See Discovery gap |
| HTTPS enforcement | ❌ Missing | `serve` uses plain HTTP |

**Effort estimate:** Small to Medium — auth middleware is well-understood.

---

### 10. Capabilities & Skills

| A2A Requirement | agentstate Status | Gap |
|---|---|---|
| `skills[]` with `id`, `name`, `description`, `tags`, `examples` | ❌ Missing | agentstate has no skill registry |
| `inputModes` / `outputModes` per skill (MIME types) | ❌ Missing | No MIME type handling |
| `parameters` — JSON Schema for skill inputs | ❌ Missing | No schema validation |
| `defaultInputModes` / `defaultOutputModes` at agent level | ❌ Missing | No mode negotiation |

**Effort estimate:** Medium — requires designing what "skills" mean for a coordination tool.

---

## What agentstate Has That A2A Doesn't

This is important — the gap goes both ways. A2A doesn't solve agentstate's problem:

| agentstate Feature | A2A Equivalent | Notes |
|---|---|---|
| **Resource claims** with operations (`read`, `write`, `delete`, `rename`, `refactor`) | ❌ None | A2A has no concept of locking or claiming resources |
| **Conflict detection matrix** (write vs write, delete vs read, etc.) | ❌ None | A2A delegates tasks but doesn't prevent collisions |
| **Resource pattern matching** (exact, glob `*`, prefix `/**`) | ❌ None | No resource URI semantics in A2A |
| **Heartbeat / TTL** for claims | ❌ None | A2A tasks don't have a "keepalive" mechanism |
| **Event log** (append-only, offset-ordered) | ⚠️ Partial | A2A `history` is similar but scoped to a task, not global |
| **Filesystem watcher** (`agentstate watch`) | ❌ None | A2A is protocol-only, no OS integration |
| **Cursor IDE hooks** (`preToolUse` / `postToolUse`) | ❌ None | A2A has no IDE integration concept |
| **Locking mode** (reject claims on conflict) | ❌ None | A2A doesn't enforce mutual exclusion |

---

## The Architectural Mismatch

### A2A's Model: "I ask you to do something"

```
Client Agent          Remote Agent
     |                      |
     |-- tasks/send -------->|
     |   "Refactor auth.ts" |
     |                      |
     |<-- Task (working) ---|
     |                      |
     |<-- SSE updates ------|
     |                      |
     |<-- Task (completed) -|
     |   + Artifacts        |
```

### agentstate's Model: "I'm working on this, don't touch it"

```
Agent A               agentstate server              Agent B
   |                         |                         |
   |-- claim auth.ts ------->|                         |
   |   "write: refactoring"  |                         |
   |<-- OK                   |                         |
   |                         |<-- claim auth.ts -------|
   |                         |   "write: fixing bug"   |
   |                         |--> CONFLICT ------------|
   |                         |   "Agent A has it"      |
   |                         |                         |
   |-- release auth.ts ----->|                         |
   |                         |                         |
```

**These are orthogonal concerns.** An A2A agent might use agentstate internally to manage its files while processing an A2A task.

---

## What Would It Take to Make agentstate A2A-Compatible?

### Option A: Add A2A as a New Layer (Recommended)

Keep agentstate's core coordination logic intact and add an **A2A adapter** that sits on top:

```
┌─────────────────────────────────────────┐
│  A2A Adapter (new)                      │
│  • Agent Card endpoint                  │
│  • JSON-RPC 2.0 router                  │
│  • Task lifecycle state machine         │
│  • Message/Part serialization           │
│  • SSE streaming                        │
│  • Artifact collection                  │
└──────────────┬──────────────────────────┘
               │ maps tasks → claims
┌──────────────▼──────────────────────────┐
│  agentstate core (existing)             │
│  • Claims, conflicts, heartbeats        │
│  • Mailbox                              │
│  • Event log                            │
│  • SQLite/Postgres providers            │
└─────────────────────────────────────────┘
```

**Work required:**
1. New `A2AServer` struct with JSON-RPC 2.0 dispatch
2. New `Task` data model with state machine
3. New `Message`/`Part` data model (or adapter to MailboxMessage)
4. New `Artifact` storage and streaming
5. `/.well-known/agent.json` endpoint
6. SSE endpoint for `tasks/sendSubscribe`
7. Push notification worker
8. Auth middleware (Bearer / API key)

**Estimated effort:** 4–6 weeks for a single engineer.

### Option B: Fork / Rewrite

Treat A2A as the primary protocol and rebuild agentstate's coordination as an A2A skill:

```json
{
  "id": "coordinate_resources",
  "name": "Resource Coordination",
  "description": "Lock files and detect conflicts between agents",
  "tags": ["coordination", "locking"]
}
```

This loses the standalone CLI and filesystem watcher, but gains full A2A interoperability.

**Estimated effort:** 8–12 weeks (effectively a new project).

---

## Recommended Path Forward

Given that agentstate solves a real problem A2A doesn't (resource locking), the best approach is **Option A**: keep agentstate as a coordination service and add an A2A adapter so A2A-compatible agents can discover and use it.

### Immediate Next Steps (if pursuing A2A interop)

1. **Read the canonical schemas** from `github.com/google/a2a` — verify all field names, types, and optionality against live source
2. **Design the Task → Claim mapping** — what does an A2A task look like when translated to agentstate claims?
3. **Implement JSON-RPC 2.0 router** — a thin wrapper over existing HTTP handlers
4. **Implement Agent Card** — static JSON at `/.well-known/agent.json`
5. **Implement `tasks/send` and `tasks/get`** — synchronous task delegation mapped to claim operations
6. **Defer SSE and push notifications** — mark `capabilities.streaming: false` and `capabilities.pushNotifications: false` initially

---

## Bottom Line

| Question | Answer |
|---|---|
| Is agentstate an A2A implementation? | **No.** |
| Could it become one? | **Yes, with significant work (4–12 weeks).** |
| Should it become one? | **As an adapter layer, yes.** As a rewrite, probably not — the resource-locking problem is real and A2A doesn't solve it. |
| What's the fastest win? | **Expose agentstate as an A2A agent** with a `coordinate_resources` skill so other A2A agents can delegate locking tasks to it. |
