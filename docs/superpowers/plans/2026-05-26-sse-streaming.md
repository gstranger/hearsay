# SSE Streaming (`tasks/sendSubscribe`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `tasks/sendSubscribe` with SSE streaming so hearsay advertises `capabilities.streaming: true` and external A2A clients can use the canonical task submission method.

**Architecture:** A new `SSEWriter` utility handles `event:`/`data:` framing and flushing. A `TaskExecutor` runs `executeSkill` in a goroutine, sending typed `TaskEvent` values through a channel. The handler reads the channel and streams SSE frames to the client. `executeSkill` is refactored to return `SkillResult` (used by both sync `tasks/send` and streaming `tasks/sendSubscribe`). No schema migrations, no provider interface changes.

**Tech Stack:** Go (no new dependencies), standard library `net/http` with `http.Flusher` for SSE

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/a2a/sse.go` (new) | `SSEWriter` — frames SSE events, calls `Flush()` |
| `internal/a2a/sse_test.go` (new) | Unit tests for SSE framing |
| `internal/a2a/task_executor.go` (new) | `TaskExecutor` + `TaskEvent` — goroutine that runs `executeSkill`, emits events |
| `internal/a2a/task_executor_test.go` (new) | Unit tests for executor lifecycle (normal, error, cancellation) |
| `internal/a2a/handler_tasks.go` (modify) | Add `SkillResult` type, refactor `executeSkill` return, add `handleTasksSendSubscribe` |
| `internal/a2a/server.go` (modify) | Add `tasks/sendSubscribe` dispatch (streaming branch before sync path) |
| `internal/a2a/agentcard.go` (modify) | Flip `Streaming: false` → `true` |
| `internal/a2a/handler_test.go` (modify) | Add SSE handler test |
| `tests/integration/a2a_test.go` (modify) | Add SSE end-to-end test |

---

### Task 1: SSEWriter — SSE Framing Utility

**Files:**
- Create: `internal/a2a/sse.go`
- Create: `internal/a2a/sse_test.go`

**Dependencies:** `internal/a2a/rpc.go` (`JSONRPCResponse`, `JSONRPCError`, `NewResponse`)

- [ ] **Step 1: Write the failing test for SSEWriter**

Create `internal/a2a/sse_test.go`:

```go
package a2a

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeFlusher wraps httptest.ResponseRecorder to implement http.Flusher
type fakeFlusher struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *fakeFlusher) Flush() { f.flushed = true }

func TestSSEWriterStatusUpdate(t *testing.T) {
	rec := httptest.NewRecorder()
	fw := &fakeFlusher{ResponseRecorder: rec}
	sse := &SSEWriter{w: fw, flusher: fw, requestID: 42}

	err := sse.WriteStatusUpdate(map[string]any{"id": "task-1", "status": map[string]any{"state": "working"}})
	if err != nil {
		t.Fatal(err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: task-status-update") {
		t.Fatalf("missing event type line, got: %s", body)
	}
	if !strings.Contains(body, `"jsonrpc":"2.0"`) {
		t.Fatalf("missing jsonrpc field, got: %s", body)
	}
	if !strings.Contains(body, `"id":42`) {
		t.Fatalf("missing request id, got: %s", body)
	}
	if !fw.flushed {
		t.Fatal("expected Flush() to be called")
	}
}

func TestSSEWriterArtifactUpdate(t *testing.T) {
	rec := httptest.NewRecorder()
	fw := &fakeFlusher{ResponseRecorder: rec}
	sse := &SSEWriter{w: fw, flusher: fw, requestID: "req-abc"}

	err := sse.WriteArtifactUpdate(map[string]any{"id": "task-1", "artifacts": []map[string]any{{"name": "result"}}})
	if err != nil {
		t.Fatal(err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: task-artifact-update") {
		t.Fatalf("missing event type line, got: %s", body)
	}
	if !strings.Contains(body, `"id":"req-abc"`) {
		t.Fatalf("missing request id, got: %s", body)
	}
}

func TestSSEWriterClose(t *testing.T) {
	rec := httptest.NewRecorder()
	fw := &fakeFlusher{ResponseRecorder: rec}
	sse := &SSEWriter{w: fw, flusher: fw, requestID: 1}

	err := sse.WriteClose(map[string]any{"id": "task-1", "status": map[string]any{"state": "completed"}})
	if err != nil {
		t.Fatal(err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: close") {
		t.Fatalf("missing close event line, got: %s", body)
	}
	if !strings.Contains(body, `"state":"completed"`) {
		t.Fatalf("missing completed state, got: %s", body)
	}
}

func TestSSEWriterError(t *testing.T) {
	rec := httptest.NewRecorder()
	fw := &fakeFlusher{ResponseRecorder: rec}
	sse := &SSEWriter{w: fw, flusher: fw, requestID: 1}

	err := sse.WriteError(NewError(-32002, "Conflict detected"))
	if err != nil {
		t.Fatal(err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `"code":-32002`) {
		t.Fatalf("missing error code, got: %s", body)
	}
	if !strings.Contains(body, `"message":"Conflict detected"`) {
		t.Fatalf("missing error message, got: %s", body)
	}
}

func TestSSEWriterMultipleEvents(t *testing.T) {
	rec := httptest.NewRecorder()
	fw := &fakeFlusher{ResponseRecorder: rec}
	sse := &SSEWriter{w: fw, flusher: fw, requestID: "x"}

	sse.WriteStatusUpdate(map[string]any{"status": map[string]any{"state": "working"}})
	sse.WriteArtifactUpdate(map[string]any{"artifacts": []map[string]any{{"name": "a"}}})
	sse.WriteClose(map[string]any{"status": map[string]any{"state": "completed"}})

	body := rec.Body.String()
	// Each event should flush independently; verify event lines are present
	lines := strings.Split(body, "\n")
	eventCount := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "event: ") {
			eventCount++
		}
	}
	if eventCount != 3 {
		t.Fatalf("expected 3 event lines, got %d in body:\n%s", eventCount, body)
	}
}

func TestSSEWriterBodyFormat(t *testing.T) {
	// Verify the SSE wire format is correct and parseable
	rec := httptest.NewRecorder()
	fw := &fakeFlusher{ResponseRecorder: rec}
	sse := &SSEWriter{w: fw, flusher: fw, requestID: 1}

	sse.WriteStatusUpdate(map[string]any{"id": "t1"})

	body := rec.Body.String()
	// Data lines must start with "data: "
	scanner := bufio.NewScanner(strings.NewReader(body))
	hasData := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			hasData = true
			jsonStr := strings.TrimPrefix(line, "data: ")
			// Should be valid JSON
			if !strings.HasPrefix(jsonStr, "{") || !strings.HasSuffix(jsonStr, "}") {
				t.Fatalf("data line is not valid JSON object: %s", jsonStr)
			}
		}
	}
	if !hasData {
		t.Fatal("no data: line found in SSE output")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/pj/Documents/agentstate && go test ./internal/a2a/ -run TestSSEWriter -v`
Expected: compilation error — `SSEWriter` not defined

- [ ] **Step 3: Write minimal SSEWriter implementation**

Create `internal/a2a/sse.go`:

```go
package a2a

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// SSEWriter writes A2A SSE (Server-Sent Events) frames to an HTTP response.
// Each method wraps the payload in a JSON-RPC 2.0 response and writes it as
// an SSE event:line followed by a data:line, then flushes.
type SSEWriter struct {
	w         http.ResponseWriter
	flusher   http.Flusher
	requestID any
}

// WriteStatusUpdate sends a task-status-update event.
func (s *SSEWriter) WriteStatusUpdate(taskJSON map[string]any) error {
	return s.writeEvent("task-status-update", taskJSON)
}

// WriteArtifactUpdate sends a task-artifact-update event.
func (s *SSEWriter) WriteArtifactUpdate(taskJSON map[string]any) error {
	return s.writeEvent("task-artifact-update", taskJSON)
}

// WriteClose sends the terminal close event and flushes.
func (s *SSEWriter) WriteClose(taskJSON map[string]any) error {
	return s.writeEvent("close", taskJSON)
}

// WriteError sends an error as a raw SSE data frame (no event: prefix, per A2A convention).
func (s *SSEWriter) WriteError(e *JSONRPCError) error {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      s.requestID,
		Error:   e,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal error response: %w", err)
	}
	return s.writeFrame(data)
}

func (s *SSEWriter) writeEvent(eventType string, result any) error {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      s.requestID,
		Result:  result,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal event response: %w", err)
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\n", eventType); err != nil {
		return err
	}
	return s.writeFrame(data)
}

func (s *SSEWriter) writeFrame(data []byte) error {
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", data); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/pj/Documents/agentstate && go test ./internal/a2a/ -run TestSSEWriter -v`
Expected: all 6 SSEWriter tests PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/pj/Documents/agentstate
git add internal/a2a/sse.go internal/a2a/sse_test.go
git commit -m "feat: add SSEWriter utility for A2A SSE event framing"
```

---

### Task 2: SkillResult type + executeSkill refactoring

**Files:**
- Modify: `internal/a2a/handler_tasks.go:13-93` (add SkillResult, refactor executeSkill call)
- Modify: `internal/a2a/handler_skills.go:15-18` (change return type of executeSkill)

**Dependencies:** None — existing types only

- [ ] **Step 1: Add SkillResult type and refactor handleTasksSend**

In `internal/a2a/handler_tasks.go`, add `SkillResult` after the `taskParams` type (after line 12), and replace the executeSkill block in `handleTasksSend` (lines 54-96):

Add after `taskParams`:

```go
// SkillResult is returned by executeSkill. It carries the mutated task and any
// artifacts produced. Synchronous callers (tasks/send) use both fields; streaming
// callers (tasks/sendSubscribe) use them to emit incremental events.
type SkillResult struct {
	Task      *Task
	Artifacts []Artifact
}
```

Replace `result, err := s.executeSkill(...)` through the artifact storage loop and completion in `handleTasksSend`. The block to replace starts at line 54 (`result, err := s.executeSkill(ctx, task, req.Message)`) and goes through line 96 (`return NewResponse(req.ID, taskToJSON(task)), nil`):

Old block:
```go
	// Execute skill based on message content
	result, err := s.executeSkill(ctx, task, req.Message)
	if err != nil {
		_ = task.Transition(TaskFailed)
		_ = s.provider.UpdateTask(ctx, s.namespace, taskToStorage(task))
		return NewResponse(req.ID, taskToJSON(task)), nil
	}

	// Transition to completed
	if err := task.Transition(TaskCompleted); err != nil {
		return nil, NewError(-32003, "State transition failed: "+err.Error())
	}

	// Store artifacts
	for _, art := range result.Artifacts {
		partsJSON, err := json.Marshal(art.Parts)
		if err != nil {
			return nil, NewError(-32003, "Failed to marshal artifact: "+err.Error())
		}
		if err := s.provider.CreateArtifact(ctx, s.namespace, task.ID, hearsay.A2AArtifact{
			Name: art.Name, Description: art.Description, Parts: partsJSON,
			Index: art.Index, Append: art.Append, LastChunk: art.LastChunk,
		}); err != nil {
			return nil, NewError(-32003, "Failed to store artifact: "+err.Error())
		}
	}

	// Update task in storage
	if err := s.provider.UpdateTask(ctx, s.namespace, taskToStorage(task)); err != nil {
		return nil, NewError(-32003, "Failed to update task: "+err.Error())
	}

	// Populate task with artifacts for response
	task.Artifacts = result.Artifacts

	return NewResponse(req.ID, taskToJSON(task)), nil
```

Replace with:

```go
	// Execute skill based on message content
	result, err := s.executeSkill(ctx, task, req.Message)
	if err != nil {
		_ = task.Transition(TaskFailed)
		_ = s.provider.UpdateTask(ctx, s.namespace, taskToStorage(task))
		return NewResponse(req.ID, taskToJSON(task)), nil
	}

	// Transition to completed
	if err := task.Transition(TaskCompleted); err != nil {
		return nil, NewError(-32003, "State transition failed: "+err.Error())
	}

	// Store artifacts
	for _, art := range result.Artifacts {
		partsJSON, err := json.Marshal(art.Parts)
		if err != nil {
			return nil, NewError(-32003, "Failed to marshal artifact: "+err.Error())
		}
		if err := s.provider.CreateArtifact(ctx, s.namespace, task.ID, hearsay.A2AArtifact{
			Name: art.Name, Description: art.Description, Parts: partsJSON,
			Index: art.Index, Append: art.Append, LastChunk: art.LastChunk,
		}); err != nil {
			return nil, NewError(-32003, "Failed to store artifact: "+err.Error())
		}
	}

	// Update task in storage
	if err := s.provider.UpdateTask(ctx, s.namespace, taskToStorage(task)); err != nil {
		return nil, NewError(-32003, "Failed to update task: "+err.Error())
	}

	// Populate task with artifacts for response
	task.Artifacts = result.Artifacts

	return NewResponse(req.ID, taskToJSON(task)), nil
```

- [ ] **Step 2: Change executeSkill return type in handler_skills.go**

In `internal/a2a/handler_skills.go`, change `executeSkill` signature from `(*Task, error)` to `(*SkillResult, error)` and update the return statement (lines 15-30). The old signature line 15:

```go
func (s *Server) executeSkill(ctx context.Context, task *Task, msg Message) (*Task, error) {
```

Replace with:

```go
func (s *Server) executeSkill(ctx context.Context, task *Task, msg Message) (*SkillResult, error) {
```

Each skill handler returns `(*Task, error)`. The switch statement at lines 24-34 currently does:

```go
	switch params.SkillID {
	case "claim_resource":
		return s.skillClaimResource(ctx, task, params)
	case "release_resource":
		return s.skillReleaseResource(ctx, task, params)
	case "check_conflict":
		return s.skillCheckConflict(ctx, task, params)
	case "query_mailbox":
		return s.skillQueryMailbox(ctx, task, params)
	case "send_mailbox":
		return s.skillSendMailbox(ctx, task, params)
	default:
		return nil, fmt.Errorf("unknown skill: %s", params.SkillID)
	}
```

Replace with:

```go
	switch params.SkillID {
	case "claim_resource":
		return wrapSkillResult(s.skillClaimResource(ctx, task, params))
	case "release_resource":
		return wrapSkillResult(s.skillReleaseResource(ctx, task, params))
	case "check_conflict":
		return wrapSkillResult(s.skillCheckConflict(ctx, task, params))
	case "query_mailbox":
		return wrapSkillResult(s.skillQueryMailbox(ctx, task, params))
	case "send_mailbox":
		return wrapSkillResult(s.skillSendMailbox(ctx, task, params))
	default:
		return nil, fmt.Errorf("unknown skill: %s", params.SkillID)
	}
```

Add the `wrapSkillResult` helper at the bottom of `handler_skills.go` (before `mustJSON`):

```go
func wrapSkillResult(task *Task, err error) (*SkillResult, error) {
	if err != nil {
		return nil, err
	}
	return &SkillResult{Task: task, Artifacts: task.Artifacts}, nil
}
```

- [ ] **Step 3: Run existing tests to verify no regression**

Run: `cd /Users/pj/Documents/agentstate && go test ./internal/a2a/ -v`
Expected: all existing tests PASS (TestHandleTasksSendClaimResource, TestHandleTasksGet, TestAgentCard, TestAuth*, TestRPC*, TestTaskTransitions)

- [ ] **Step 4: Commit**

```bash
cd /Users/pj/Documents/agentstate
git add internal/a2a/handler_tasks.go internal/a2a/handler_skills.go
git commit -m "refactor: extract SkillResult type from executeSkill return"
```

---

### Task 3: TaskExecutor — Goroutine Event Stream

**Files:**
- Create: `internal/a2a/task_executor.go`
- Create: `internal/a2a/task_executor_test.go`

**Dependencies:** `SkillResult` (Task 2), `Task`, `TaskState` constants (`internal/a2a/task.go`), `executeSkill` on `*Server`

- [ ] **Step 1: Write the failing test for TaskExecutor**

Create `internal/a2a/task_executor_test.go`:

```go
package a2a

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gstranger/hearsay/internal/memory"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

func TestTaskExecutorNormalFlow(t *testing.T) {
	p := memory.New()
	_ = p.CreateNamespace(context.Background(), hearsay.Namespace{ID: "test", CreatedAt: time.Now()})
	client := hearsay.NewClient(p, "test")
	cfg := &hearsay.A2AConfig{Addr: "localhost:8081"}
	mw := &AuthMiddleware{}
	srv := NewServer(cfg, client, p, mw, "test")

	ctx := context.Background()
	msg := Message{
		Role: "user",
		Parts: []Part{{Type: "data", Data: json.RawMessage(`{"skill_id":"check_conflict","resource_uri":"file://x.ts","operation":"write"}`)}},
	}
	task := NewTask("task-exec-1", "", "test")

	executor := StartTaskExecution(ctx, task, msg, srv)

	events := collectEvents(executor)
	if len(events) < 4 {
		t.Fatalf("expected at least 4 events (submitted, working, artifact, done), got %d", len(events))
	}

	// First event should be status: submitted
	if events[0].Type != "status" || events[0].Task.Status.State != TaskSubmitted {
		t.Fatalf("event 0: expected status/submitted, got %s/%s", events[0].Type, events[0].Task.Status.State)
	}

	// Second event should be status: working
	if events[1].Type != "status" || events[1].Task.Status.State != TaskWorking {
		t.Fatalf("event 1: expected status/working, got %s/%s", events[1].Type, events[1].Task.Status.State)
	}

	// There should be an artifact event
	hasArtifact := false
	for _, e := range events {
		if e.Type == "artifact" {
			hasArtifact = true
			break
		}
	}
	if !hasArtifact {
		t.Fatal("expected an artifact event")
	}

	// Last event should be done with completed state
	last := events[len(events)-1]
	if last.Type != "done" || last.Task.Status.State != TaskCompleted {
		t.Fatalf("last event: expected done/completed, got %s/%s", last.Type, last.Task.Status.State)
	}
}

func TestTaskExecutorSkillError(t *testing.T) {
	p := memory.New()
	_ = p.CreateNamespace(context.Background(), hearsay.Namespace{ID: "test", CreatedAt: time.Now()})
	client := hearsay.NewClient(p, "test")
	cfg := &hearsay.A2AConfig{Addr: "localhost:8081"}
	mw := &AuthMiddleware{}
	srv := NewServer(cfg, client, p, mw, "test")

	ctx := context.Background()
	// Unknown skill produces an error
	msg := Message{
		Role: "user",
		Parts: []Part{{Type: "data", Data: json.RawMessage(`{"skill_id":"nonexistent_skill","resource_uri":"file://x.ts"}`)}},
	}
	task := NewTask("task-exec-err", "", "test")

	executor := StartTaskExecution(ctx, task, msg, srv)

	events := collectEvents(executor)
	// Should have an error event and no done event
	hasError := false
	for _, e := range events {
		if e.Type == "error" {
			hasError = true
			break
		}
		if e.Type == "done" {
			t.Fatal("should not get done event on skill error")
		}
	}
	if !hasError {
		t.Fatal("expected an error event")
	}
}

func TestTaskExecutorContextCancellation(t *testing.T) {
	p := memory.New()
	_ = p.CreateNamespace(context.Background(), hearsay.Namespace{ID: "test", CreatedAt: time.Now()})
	client := hearsay.NewClient(p, "test")
	cfg := &hearsay.A2AConfig{Addr: "localhost:8081"}
	mw := &AuthMiddleware{}
	srv := NewServer(cfg, client, p, mw, "test")

	// Cancel immediately before the goroutine runs
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	msg := Message{
		Role: "user",
		Parts: []Part{{Type: "data", Data: json.RawMessage(`{"skill_id":"check_conflict","resource_uri":"file://x.ts","operation":"write"}`)}},
	}
	task := NewTask("task-exec-cancel", "", "test")

	executor := StartTaskExecution(ctx, task, msg, srv)
	events := collectEvents(executor)

	// With an already-cancelled context, executeSkill should not be called.
	// The executor should notice ctx is done and emit an error or close gracefully.
	if len(events) == 0 {
		t.Fatal("expected at least one event (cancellation should emit something)")
	}
}

func collectEvents(exec *TaskExecutor) []TaskEvent {
	var events []TaskEvent
	for evt := range exec.Events() {
		events = append(events, evt)
		if evt.Type == "done" || evt.Type == "error" {
			break
		}
	}
	return events
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/pj/Documents/agentstate && go test ./internal/a2a/ -run TestTaskExecutor -v`
Expected: compilation error — `TaskExecutor`, `TaskEvent`, `StartTaskExecution` not defined

- [ ] **Step 3: Write TaskExecutor implementation**

Create `internal/a2a/task_executor.go`:

```go
package a2a

import (
	"context"
)

// TaskEvent represents a single event in a task execution stream.
type TaskEvent struct {
	Type      string        // "status", "artifact", "done", "error"
	Task      *Task         // task at point of event (for status/artifact/done)
	Artifacts []Artifact    // for "artifact" events
	Error     *JSONRPCError // for "error" events
}

// TaskExecutor runs executeSkill in a goroutine and streams progress through a channel.
// The caller reads from Events() and handles framing (SSE, logging, etc.).
type TaskExecutor struct {
	task   *Task
	events chan TaskEvent
	ctx    context.Context
}

// Events returns the event channel. It is closed when execution completes or fails.
func (e *TaskExecutor) Events() <-chan TaskEvent {
	return e.events
}

// StartTaskExecution launches executeSkill in a goroutine. It sends status updates
// for state transitions, artifacts when the skill produces output, and a final "done"
// or "error" event. Callers read from executor.Events() to receive progress.
func StartTaskExecution(ctx context.Context, task *Task, msg Message, srv *Server) *TaskExecutor {
	exec := &TaskExecutor{
		task:   task,
		events: make(chan TaskEvent, 8), // buffered so the goroutine doesn't block on fast sequences
		ctx:    ctx,
	}

	go func() {
		defer close(exec.events)

		// Emit submitted
		if !exec.send(TaskEvent{Type: "status", Task: task}) {
			return
		}

		// Transition to working
		if err := task.Transition(TaskWorking); err != nil {
			exec.send(TaskEvent{
				Type:  "error",
				Error: NewError(-32003, "State transition failed: "+err.Error()),
			})
			return
		}
		if !exec.send(TaskEvent{Type: "status", Task: task}) {
			return
		}

		// Check context before executing
		if ctx.Err() != nil {
			_ = task.Transition(TaskFailed)
			exec.send(TaskEvent{
				Type:  "error",
				Error: NewError(-32003, "Task cancelled: "+ctx.Err().Error()),
			})
			return
		}

		// Execute skill
		result, err := srv.executeSkill(ctx, task, msg)
		if err != nil {
			_ = task.Transition(TaskFailed)
			if rpcErr, ok := err.(*JSONRPCError); ok {
				exec.send(TaskEvent{Type: "error", Error: rpcErr})
			} else {
				exec.send(TaskEvent{
					Type:  "error",
					Error: NewError(-32003, err.Error()),
				})
			}
			return
		}

		// Emit artifacts
		if len(result.Artifacts) > 0 {
			task.Artifacts = result.Artifacts
			if !exec.send(TaskEvent{Type: "artifact", Task: task, Artifacts: result.Artifacts}) {
				return
			}
		}

		// Transition to completed
		if err := task.Transition(TaskCompleted); err != nil {
			exec.send(TaskEvent{
				Type:  "error",
				Error: NewError(-32003, "State transition failed: "+err.Error()),
			})
			return
		}

		// Emit completed status then done
		if !exec.send(TaskEvent{Type: "status", Task: task}) {
			return
		}
		exec.send(TaskEvent{Type: "done", Task: task})
	}()

	return exec
}

// send sends a TaskEvent on the channel, respecting context cancellation.
// Returns false if the context is cancelled (caller should stop).
func (e *TaskExecutor) send(evt TaskEvent) bool {
	select {
	case <-e.ctx.Done():
		return false
	case e.events <- evt:
		return true
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/pj/Documents/agentstate && go test ./internal/a2a/ -run TestTaskExecutor -v`
Expected: all 3 TaskExecutor tests PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/pj/Documents/agentstate
git add internal/a2a/task_executor.go internal/a2a/task_executor_test.go
git commit -m "feat: add TaskExecutor for goroutine-based skill streaming with event channel"
```

---

### Task 4: handleTasksSendSubscribe Handler

**Files:**
- Modify: `internal/a2a/handler_tasks.go` (add `handleTasksSendSubscribe` method)

**Dependencies:** `SSEWriter` (Task 1), `TaskExecutor` (Task 3), `SkillResult` (Task 2)

- [ ] **Step 1: Write handler test (in handler_test.go)**

Add to `internal/a2a/handler_test.go`:

```go
func TestHandleTasksSendSubscribeClaimResource(t *testing.T) {
	p := memory.New()
	_ = p.CreateNamespace(context.Background(), hearsay.Namespace{ID: "test", CreatedAt: time.Now()})
	client := hearsay.NewClient(p, "test")
	cfg := &hearsay.A2AConfig{Addr: "localhost:8081"}
	mw := &AuthMiddleware{}
	srv := NewServer(cfg, client, p, mw, "test")

	params, _ := json.Marshal(map[string]any{
		"id": "task-ss-1",
		"message": map[string]any{
			"role": "user",
			"parts": []map[string]any{
				{"type": "data", "data": map[string]any{
					"skill_id":     "claim_resource",
					"resource_uri": "file://x.ts",
					"operation":    "write",
					"agent_id":     "a1",
					"intent":       "test",
				}},
			},
		},
	})
	paramsJSON := json.RawMessage(params)
	req := JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tasks/sendSubscribe", Params: paramsJSON}

	rec := httptest.NewRecorder()
	srv.handleTasksSendSubscribe(rec, httptest.NewRequest("POST", "/", nil), req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	// Must be text/event-stream
	ct := rec.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Fatalf("expected Content-Type text/event-stream, got %s", ct)
	}
	// Must contain close event
	if !strings.Contains(body, "event: close") {
		t.Fatalf("missing close event in body:\n%s", body)
	}
	// Must contain working status
	if !strings.Contains(body, "event: task-status-update") {
		t.Fatalf("missing status update events in body:\n%s", body)
	}
	// Must contain artifact
	if !strings.Contains(body, "event: task-artifact-update") {
		t.Fatalf("missing artifact event in body:\n%s", body)
	}
}

func TestHandleTasksSendSubscribeInvalidParams(t *testing.T) {
	p := memory.New()
	_ = p.CreateNamespace(context.Background(), hearsay.Namespace{ID: "test", CreatedAt: time.Now()})
	client := hearsay.NewClient(p, "test")
	cfg := &hearsay.A2AConfig{Addr: "localhost:8081"}
	mw := &AuthMiddleware{}
	srv := NewServer(cfg, client, p, mw, "test")

	// Missing required "id"
	params, _ := json.Marshal(map[string]any{
		"message": map[string]any{"role": "user", "parts": []map[string]any{}},
	})
	paramsJSON := json.RawMessage(params)
	req := JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tasks/sendSubscribe", Params: paramsJSON}

	rec := httptest.NewRecorder()
	srv.handleTasksSendSubscribe(rec, httptest.NewRequest("POST", "/", nil), req)

	// Invalid params should return HTTP 400 directly (before SSE handoff)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid params, got %d: %s", rec.Code, rec.Body.String())
	}
}
```

Add imports at the top of `handler_test.go` — add `"net/http/httptest"` and `"strings"` to the existing import block:

```go
import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gstranger/hearsay/internal/memory"
	"github.com/gstranger/hearsay/pkg/hearsay"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/pj/Documents/agentstate && go test ./internal/a2a/ -run TestHandleTasksSendSubscribe -v`
Expected: compilation error — `handleTasksSendSubscribe` not defined

- [ ] **Step 3: Write handleTasksSendSubscribe**

Add to `internal/a2a/handler_tasks.go` after `handleTasksSend` (after line 96):

```go
// handleTasksSendSubscribe handles the A2A tasks/sendSubscribe streaming method.
// It creates a task, starts execution in a goroutine, and streams progress via SSE.
func (s *Server) handleTasksSendSubscribe(w http.ResponseWriter, r *http.Request, req JSONRPCRequest) {
	var params taskParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   NewError(-32602, "Invalid params: "+err.Error()),
		})
		return
	}
	if params.ID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   NewError(-32602, "Missing required param: id"),
		})
		return
	}

	task := NewTask(params.ID, params.SessionID, s.namespace)
	if err := s.provider.CreateTask(r.Context(), s.namespace, taskToStorage(task)); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   NewError(-32003, "Failed to create task: "+err.Error()),
		})
		return
	}

	// Append user message to history
	msgJSON, _ := json.Marshal(params.Message)
	_ = s.provider.AppendTaskHistory(r.Context(), s.namespace, task.ID, 0, hearsay.A2AMessage{
		Role:  "user",
		Parts: msgJSON,
	})

	flusher, ok := w.(http.Flusher)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   NewError(-32003, "Streaming not supported"),
		})
		return
	}

	sse := &SSEWriter{w: w, flusher: flusher, requestID: req.ID}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	executor := StartTaskExecution(r.Context(), task, params.Message, s)

	for evt := range executor.Events() {
		switch evt.Type {
		case "status":
			sse.WriteStatusUpdate(taskToJSON(evt.Task))
		case "artifact":
			// Store artifacts in the provider
			for _, art := range evt.Artifacts {
				partsJSON, _ := json.Marshal(art.Parts)
				s.provider.CreateArtifact(r.Context(), s.namespace, task.ID, hearsay.A2AArtifact{
					Name: art.Name, Description: art.Description, Parts: partsJSON,
					Index: art.Index, Append: art.Append, LastChunk: art.LastChunk,
				})
			}
			sse.WriteArtifactUpdate(taskToJSON(evt.Task))
		case "error":
			sse.WriteError(evt.Error)
			// Store failed state
			_ = s.provider.UpdateTask(r.Context(), s.namespace, taskToStorage(evt.Task))
			return
		case "done":
			// Store final state
			_ = s.provider.UpdateTask(r.Context(), s.namespace, taskToStorage(evt.Task))
			sse.WriteClose(taskToJSON(evt.Task))
			return
		}
	}
}
```

Add `"net/http"` and `"encoding/json"` to the imports if not already present (they are already there).

- [ ] **Step 4: Run tests to verify**

Run: `cd /Users/pj/Documents/agentstate && go test ./internal/a2a/ -run TestHandleTasksSendSubscribe -v`
Expected: both handler tests PASS

- [ ] **Step 5: Run full a2a test suite to verify no regression**

Run: `cd /Users/pj/Documents/agentstate && go test ./internal/a2a/ -v`
Expected: all tests PASS (existing + new)

- [ ] **Step 6: Commit**

```bash
cd /Users/pj/Documents/agentstate
git add internal/a2a/handler_tasks.go internal/a2a/handler_test.go
git commit -m "feat: add handleTasksSendSubscribe with SSE streaming"
```

---

### Task 5: Server Dispatch — Wire in tasks/sendSubscribe

**Files:**
- Modify: `internal/a2a/server.go:48-68` (`handleJSONRPC` dispatch)

- [ ] **Step 1: Add streaming dispatch branch**

In `internal/a2a/server.go`, modify `handleJSONRPC` to check for streaming methods before the sync response path. The method currently starts at line 38:

```go
func (s *Server) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, nil, -32600, "Invalid Request")
		return
	}

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

	if handlerErr != nil {
		if rpcErr, ok := handlerErr.(*JSONRPCError); ok {
			s.writeError(w, req.ID, rpcErr.Code, rpcErr.Message, rpcErr.Data)
		} else {
			s.writeError(w, req.ID, -32003, handlerErr.Error())
		}
		return
	}

	resp.ID = req.ID
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
```

Replace with:

```go
func (s *Server) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, nil, -32600, "Invalid Request")
		return
	}

	// Streaming methods handle their own response lifecycle (SSE)
	if req.Method == "tasks/sendSubscribe" {
		s.handleTasksSendSubscribe(w, r, req)
		return
	}

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

	if handlerErr != nil {
		if rpcErr, ok := handlerErr.(*JSONRPCError); ok {
			s.writeError(w, req.ID, rpcErr.Code, rpcErr.Message, rpcErr.Data)
		} else {
			s.writeError(w, req.ID, -32003, handlerErr.Error())
		}
		return
	}

	resp.ID = req.ID
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
```

- [ ] **Step 2: Run tests to verify dispatch works**

Run: `cd /Users/pj/Documents/agentstate && go test ./internal/a2a/ -v`
Expected: all tests PASS (existing sync handlers + new streaming handler)

- [ ] **Step 3: Commit**

```bash
cd /Users/pj/Documents/agentstate
git add internal/a2a/server.go
git commit -m "feat: wire tasks/sendSubscribe into JSON-RPC dispatch"
```

---

### Task 6: Agent Card — Enable Streaming Capability

**Files:**
- Modify: `internal/a2a/agentcard.go:56`

- [ ] **Step 1: Flip streaming flag**

In `internal/a2a/agentcard.go` line 56, change:

```go
Capabilities:     Capabilities{Streaming: false, PushNotifications: false, StateTransitionHistory: false},
```

to:

```go
Capabilities:     Capabilities{Streaming: true, PushNotifications: false, StateTransitionHistory: false},
```

- [ ] **Step 2: Run agent card test to verify**

Run: `cd /Users/pj/Documents/agentstate && go test ./internal/a2a/ -run TestAgentCard -v`
Expected: PASS (test doesn't assert streaming false, so this is a no-op verification)

- [ ] **Step 3: Commit**

```bash
cd /Users/pj/Documents/agentstate
git add internal/a2a/agentcard.go
git commit -m "feat: advertise capabilities.streaming:true in Agent Card"
```

---

### Task 7: Integration Test — Full SSE End-to-End

**Files:**
- Modify: `tests/integration/a2a_test.go` (add SSE test function, add import for `bufio`)

- [ ] **Step 1: Write the SSE integration test**

Add to `tests/integration/a2a_test.go` after the existing `TestA2AEndToEnd` function. Add `"bufio"` and `"strings"` to imports:

```go
func TestA2ASendSubscribeEndToEnd(t *testing.T) {
	p := memory.New()
	_ = p.CreateNamespace(context.Background(), hearsay.Namespace{ID: "test", CreatedAt: time.Now()})
	client := hearsay.NewClient(p, "test")
	cfg := &hearsay.A2AConfig{Addr: "localhost:8081", APIKey: "ak_test"}
	mw := &a2a.AuthMiddleware{APIKey: "ak_test"}
	srv := a2a.NewServer(cfg, client, p, mw, "test")

	// 1. Verify Agent Card advertises streaming
	req := httptest.NewRequest("GET", "/.well-known/agent.json", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("agent card: expected 200, got %d", rec.Code)
	}
	var card a2a.AgentCard
	if err := json.Unmarshal(rec.Body.Bytes(), &card); err != nil {
		t.Fatal(err)
	}
	if !card.Capabilities.Streaming {
		t.Fatal("expected capabilities.streaming to be true")
	}

	// 2. Send a tasks/sendSubscribe request
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tasks/sendSubscribe",
		"params": map[string]any{
			"id": "task-sse-1",
			"message": map[string]any{
				"role": "user",
				"parts": []map[string]any{
					{"type": "data", "data": map[string]any{
						"skill_id":     "claim_resource",
						"resource_uri": "file://src/lib.ts",
						"operation":    "write",
						"agent_id":     "agent-sse",
						"intent":       "stream test",
					}},
				},
			},
		},
	})
	req = httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("X-Api-Key", "ak_test")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("sendSubscribe: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify Content-Type is text/event-stream
	ct := rec.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Fatalf("expected Content-Type text/event-stream, got %s", ct)
	}

	// Parse SSE stream and verify event sequence
	bodyStr := rec.Body.String()
	events := parseSSEEvents(bodyStr)

	if len(events) < 2 {
		t.Fatalf("expected at least 2 SSE events, got %d in:\n%s", len(events), bodyStr)
	}

	// Verify event types
	eventTypes := map[string]bool{}
	for _, e := range events {
		eventTypes[e.EventType] = true
	}
	if !eventTypes["task-status-update"] {
		t.Fatal("missing task-status-update event")
	}
	if !eventTypes["task-artifact-update"] {
		t.Fatal("missing task-artifact-update event")
	}
	if !eventTypes["close"] {
		t.Fatal("missing close event")
	}

	// The close event should have completed state
	closeEvent := events[len(events)-1]
	if closeEvent.EventType != "close" {
		t.Fatalf("last event should be close, got %s", closeEvent.EventType)
	}

	// 3. Verify the task can be retrieved via tasks/get
	body, _ = json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tasks/get",
		"params": map[string]any{"id": "task-sse-1"},
	})
	req = httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("X-Api-Key", "ak_test")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("tasks/get after sendSubscribe: expected 200, got %d", rec.Code)
	}
	var resp a2a.JSONRPCResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Error != nil {
		t.Fatalf("tasks/get error: %v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	status := result["status"].(map[string]any)
	if status["state"] != "completed" {
		t.Fatalf("expected completed, got %v", status["state"])
	}
}

type sseEvent struct {
	EventType string
	Data      string
}

func parseSSEEvents(body string) []sseEvent {
	var events []sseEvent
	scanner := bufio.NewScanner(strings.NewReader(body))
	current := sseEvent{}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if current.EventType != "" || current.Data != "" {
				events = append(events, current)
				current = sseEvent{}
			}
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			current.EventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			current.Data = strings.TrimPrefix(line, "data: ")
		}
	}
	if current.EventType != "" || current.Data != "" {
		events = append(events, current)
	}
	return events
}
```

Update the imports in `tests/integration/a2a_test.go` to include:

```go
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gstranger/hearsay/internal/a2a"
	"github.com/gstranger/hearsay/internal/memory"
	"github.com/gstranger/hearsay/pkg/hearsay"
)
```

- [ ] **Step 2: Run test to verify it passes**

Run: `cd /Users/pj/Documents/agentstate && go test ./tests/integration/ -run TestA2ASendSubscribeEndToEnd -v`
Expected: PASS — SSE stream contains status-update, artifact-update, and close events; Agent Card shows streaming:true; tasks/get retrieves the completed task

- [ ] **Step 3: Commit**

```bash
cd /Users/pj/Documents/agentstate
git add tests/integration/a2a_test.go
git commit -m "test: add SSE sendSubscribe end-to-end integration test"
```

---

### Task 8: Final Verification — Full Test Suite

- [ ] **Step 1: Run all A2A tests**

Run: `cd /Users/pj/Documents/agentstate && go test ./internal/a2a/ -v`
Expected: all tests PASS

- [ ] **Step 2: Run integration tests**

Run: `cd /Users/pj/Documents/agentstate && go test ./tests/integration/ -v`
Expected: all tests PASS

- [ ] **Step 3: Run full project test suite**

Run: `cd /Users/pj/Documents/agentstate && go test ./... -v`
Expected: all packages PASS (no regressions)

- [ ] **Step 4: Commit any final changes**

```bash
cd /Users/pj/Documents/agentstate
git add -A
git diff --cached --stat  # review what's staged
git commit -m "test: full test suite verification for SSE sendSubscribe" --allow-empty
```