# SSE Streaming for `tasks/sendSubscribe` — Design Document

> Date: 2026-05-26  
> Status: Draft  
> Scope: Phase 2 — SSE streaming for A2A protocol compliance

---

## 1. Purpose

Implement `tasks/sendSubscribe` so hearsay advertises `capabilities.streaming: true` and external A2A clients (Google ADK, LangChain, CrewAI, etc.) can use the canonical task submission method. The implementation lays the architecture for future long-running skills (conflict watch, mailbox stream) without over-engineering for skills that don't exist yet.

---

## 2. Architecture

### 2.1 High-Level Flow

```
Client                          A2A Server
  |                                  |
  |-- POST /                        |
  |   { method: tasks/sendSubscribe, |
  |     params: { id, message } }   |
  |                                  |-- create Task (state: submitted)
  |                                  |-- store Task in Provider
  |                                  |-- launch goroutine: TaskExecutor
  |                                  |
  |<-- HTTP 200, SSE headers -------|
  |                                  |
  |<-- event: task-status-update ---|  (state: submitted)
  |<-- event: task-status-update ---|  (state: working)
  |                                  |  executeSkill() runs...
  |<-- event: task-artifact-update -|  (result artifact)
  |<-- event: task-status-update ---|  (state: completed)
  |<-- event: close ----------------|  (terminal)
```

### 2.2 TaskExecutor Channel Model

A new `TaskExecutor` type bridges the synchronous `executeSkill` call and the streaming SSE handler:

```go
type TaskExecutor struct {
    task   *Task
    events chan TaskEvent
    ctx    context.Context
}

type TaskEvent struct {
    Type      string         // "status", "artifact", "done", "error"
    Task      *Task          // task at point of event (for status/artifact/done)
    Artifacts []Artifact     // for "artifact" events
    Error     *JSONRPCError  // for "error" events
}
```

`StartTaskExecution` launches a goroutine:

1. Emits `status: submitted`
2. Emits `status: working`
3. Calls `executeSkill(ctx, task, msg)` — blocking, synchronous
4. On success: emits `artifact: result.Artifacts`, emits `status: completed`, emits `done`
5. On error: emits `error` with the JSON-RPC error object

The goroutine doesn't know about SSE. It writes typed events to a channel. The handler is the only SSE-aware component.

---

## 3. SSE Protocol Layer

### 3.1 `SSEWriter` Utility

A small type in a new `internal/a2a/sse.go`:

```go
type SSEWriter struct {
    w         http.ResponseWriter
    flusher   http.Flusher
    requestID any  // JSON-RPC request id — echoed in every response
}

func (s *SSEWriter) WriteStatusUpdate(taskJSON map[string]any) error
func (s *SSEWriter) WriteArtifactUpdate(taskJSON map[string]any) error
func (s *SSEWriter) WriteError(rpcError *JSONRPCError) error
func (s *SSEWriter) WriteClose(taskJSON map[string]any) error
```

Each method:
1. Wraps `taskJSON` (or the error) in a `JSONRPCResponse{JSONRPC: "2.0", ID: requestID, ...}`
2. Serializes to JSON
3. Writes `event: <type>\ndata: <json>\n\n`
4. Calls `Flush()`

### 3.2 SSE Event Types

| Event | Payload | When |
|---|---|---|
| `task-status-update` | Full `JSONRPCResponse` with `result: Task` | State transitions (submitted, working, completed, failed, canceled) |
| `task-artifact-update` | Full `JSONRPCResponse` with `result: Task` containing new artifacts | After skill produces output |
| `close` | Full `JSONRPCResponse` with `result: Task` in terminal state | Final event — stream ends here |
| (inline error) | `JSONRPCResponse` with `error` | Validation failure or runtime error |

Errors are delivered as raw SSE data frames (no `event:` prefix), per A2A spec convention for streaming errors.

### 3.3 HTTP Headers

```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
```

HTTP status is always 200. Errors flow through the SSE stream, not HTTP status codes.

---

## 4. Handler: `handleTasksSendSubscribe`

### 4.1 Method Dispatch

`handleJSONRPC` in `server.go` handles the streaming method before the sync response path:

```go
func (s *Server) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
    var req JSONRPCRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        s.writeError(w, nil, -32600, "Invalid Request")
        return
    }

    // Streaming methods handle their own response lifecycle
    if req.Method == "tasks/sendSubscribe" {
        s.handleTasksSendSubscribe(w, r, req)
        return
    }

    // Sync methods follow the standard JSON-RPC response path
    var resp *JSONRPCResponse
    var handlerErr error
    switch req.Method {
    case "tasks/send":
        resp, handlerErr = s.handleTasksSend(r.Context(), req.Params)
    case "tasks/get":
        resp, handlerErr = s.handleTasksGet(r.Context(), req.Params)
    case "tasks/cancel":
        resp, handlerErr = s.handleTasksCancel(r.Context(), req.Params)
    default:
        s.writeError(w, req.ID, -32601, "Method not found")
        return
    }

    if handlerErr != nil { /* ... error handling unchanged ... */ }
    resp.ID = req.ID
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(resp)
}
```

`handleTasksSendSubscribe` writes its own headers, SSE frames, and closes the connection — it does not return through the standard `JSONRPCResponse` path.

### 4.2 Handler Logic

```go
func (s *Server) handleTasksSendSubscribe(w http.ResponseWriter, r *http.Request, req JSONRPCRequest) {
    // 1. Parse and validate params (same validation as tasks/send)
    params, err := parseTaskParams(req.Params)
    if err != nil {
        writeJSONRPCError(w, req.ID, -32602, "Invalid params: "+err.Error())
        return
    }

    // 2. Create task, store it
    task := NewTask(params.ID, params.SessionID, s.namespace)
    if err := s.provider.CreateTask(r.Context(), s.namespace, taskToStorage(task)); err != nil {
        writeJSONRPCError(w, req.ID, -32003, "Failed to create task: "+err.Error())
        return
    }

    // 3. Set up SSE
    flusher, ok := w.(http.Flusher)
    if !ok {
        writeJSONRPCError(w, req.ID, -32003, "Streaming not supported")
        return
    }
    sse := &SSEWriter{w: w, flusher: flusher, requestID: req.ID}
    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.WriteHeader(http.StatusOK)

    // 4. Start execution goroutine
    executor := StartTaskExecution(r.Context(), task, params.Message, s)

    // 5. Stream events until done or error
    for event := range executor.Events() {
        switch event.Type {
        case "status":
            sse.WriteStatusUpdate(taskToJSON(event.Task))
        case "artifact":
            sse.WriteArtifactUpdate(taskToJSON(event.Task))
        case "error":
            sse.WriteError(event.Error)
            return
        case "done":
            s.provider.UpdateTask(r.Context(), s.namespace, taskToStorage(event.Task))
            sse.WriteClose(taskToJSON(event.Task))
            return
        }
    }
}
```

### 4.3 Disconnect Handling

- `r.Context()` is cancelled when the client disconnects
- The goroutine checks `ctx.Err()` before sending to the channel
- On disconnect, it releases any held claim and marks the task `failed`
- The channel is closed, ending the `for range` loop in the handler

---

## 5. `executeSkill` Refactoring

### 5.1 Current Signature

```go
func (s *Server) executeSkill(ctx context.Context, task *Task, msg Message) (*Task, error)
```

Returns the mutated task with artifacts populated.

### 5.2 New Signature

Introduce a `SkillResult` struct so the caller (sync handler or goroutine) has the pieces it needs:

```go
type SkillResult struct {
    Task      *Task   // mutated task (state, claim_id, etc.)
    Artifacts []Artifact
}

func (s *Server) executeSkill(ctx context.Context, task *Task, msg Message) (*SkillResult, error)
```

### 5.3 Caller Differences

**`tasks/send` (sync):**
```go
result, err := s.executeSkill(ctx, task, msg)
// ... handle error, store artifacts, mark completed, return response
```

**`tasks/sendSubscribe` goroutine:**
```go
emitStatus(task, TaskWorking)
result, err := s.executeSkill(ctx, task, msg)
if err != nil {
    emitError(err)
    return
}
task.Artifacts = result.Artifacts
emitArtifact(task)
task.Transition(TaskCompleted)
emitStatus(task)
emitDone(task)
```

The skill implementations (`skillClaimResource`, `skillReleaseResource`, etc.) are unchanged — they already set `task.Artifacts` on the task. Only the return site in `executeSkill` changes from returning `task` to returning `&SkillResult{Task: task, Artifacts: task.Artifacts}`.

---

## 6. What Does Not Change

- **Provider interface** — no new storage methods. `CreateTask`, `UpdateTask`, and `CreateArtifact` already exist.
- **Database schema** — no migrations. Tasks and artifacts are stored identically for sync and streaming paths.
- **Skills** — no changes to `skillClaimResource`, `skillReleaseResource`, `skillCheckConflict`, `skillQueryMailbox`, `skillSendMailbox`.
- **`tasks/send`** — continues working as before, just uses `SkillResult` instead of `*Task` return.
- **`tasks/get`** — unchanged.
- **`tasks/cancel`** — unchanged. If called while an SSE stream is in-flight for the same task, the stream goroutine receives ctx cancellation and cleans up.

---

## 7. Agent Card Update

```go
func GenerateAgentCard(cfg *hearsay.A2AConfig, version string) *AgentCard {
    return &AgentCard{
        // ...
        Capabilities: Capabilities{
            Streaming:              true,   // was false
            PushNotifications:      false,
            StateTransitionHistory: false,
        },
        // ...
    }
}
```

---

## 8. Files Changed

| File | Change |
|---|---|
| `internal/a2a/sse.go` | **New** — `SSEWriter` type |
| `internal/a2a/server.go` | Add `tasks/sendSubscribe` case to dispatch, handle streaming vs sync response patterns |
| `internal/a2a/handler_tasks.go` | Add `handleTasksSendSubscribe`, add `SkillResult` type, refactor `executeSkill` return to `SkillResult`, add `StartTaskExecution` |
| `internal/a2a/task.go` | Add `TaskExecutor` type with `TaskEvent` |
| `internal/a2a/agentcard.go` | Flip `Streaming: true` |
| `internal/a2a/handler_test.go` | Add SSE handler tests |
| `internal/a2a/server_test.go` | Integration test for sendSubscribe flow |
| `internal/a2a/sse_test.go` | **New** — SSE framing unit tests |

---

## 9. Testing Strategy

### 9.1 Unit Tests

- `sse_test.go` — Verify `SSEWriter` produces correctly framed `event:` / `data:` lines, flushes between events, handles nil/small/large payloads
- `task_test.go` — `TaskExecutor` goroutine lifecycle: emits correct event sequence (submitted → working → artifact → completed → done), handles skill errors, handles context cancellation

### 9.2 Handler Tests

- Submit a valid `tasks/sendSubscribe` request, read SSE stream, assert event sequence matches expected
- Submit with bad params → error in SSE stream (not HTTP 400)
- Client disconnects mid-stream → goroutine cleans up, task ends in failed state
- Submit `tasks/send` and `tasks/sendSubscribe` with same params → both produce the same final task state

### 9.3 Integration Test

Extend `tests/integration/a2a_test.go` with:
- Full SSE stream read for a claim_resource task
- Verify all expected event types appear (status, artifact, close)
- Verify `capabilities.streaming: true` in Agent Card

---

## 10. Future: Long-Running Skills

This design establishes the architecture future long-running skills will use. When implementing, for example, a conflict watch skill:

1. `executeSkill` would be a long-running loop rather than a single `client.Claim()`
2. It calls `client.Claim()` initially, then enters a polling or subscription loop
3. It sends incremental artifacts via the channel (not in this design, but the channel is the natural extension point)
4. On ctx cancellation (client disconnect), it releases the claim and exits

No architectural changes needed — just a skill that doesn't return immediately.

---

*End of design document*