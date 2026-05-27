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