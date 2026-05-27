package integration

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

func TestA2AEndToEnd(t *testing.T) {
	p := memory.New()
	_ = p.CreateNamespace(context.Background(), hearsay.Namespace{ID: "test", CreatedAt: time.Now()})
	client := hearsay.NewClient(p, "test")
	cfg := &hearsay.A2AConfig{Addr: "localhost:8081", APIKey: "ak_test"}
	mw := &a2a.AuthMiddleware{APIKey: "ak_test"}
	srv := a2a.NewServer(cfg, client, p, mw, "test")

	// 1. Agent Card
	req := httptest.NewRequest("GET", "/.well-known/agent.json", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 { t.Fatalf("agent card: expected 200, got %d", rec.Code) }
	var card a2a.AgentCard
	if err := json.Unmarshal(rec.Body.Bytes(), &card); err != nil { t.Fatal(err) }
	if card.Name != "hearsay-coordinator" { t.Fatal("name mismatch") }
	if len(card.Skills) != 5 { t.Fatalf("expected 5 skills, got %d", len(card.Skills)) }

	// 2. Claim resource
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tasks/send",
		"params": map[string]any{
			"id": "task-claim-1",
			"message": map[string]any{
				"role": "user",
				"parts": []map[string]any{
					{"type": "data", "data": map[string]any{
						"skill_id": "claim_resource",
						"resource_uri": "file://src/api.go",
						"operation": "write",
						"agent_id": "agent-a",
						"intent": "refactoring",
					}},
				},
			},
		},
	})
	req = httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("X-Api-Key", "ak_test")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 { t.Fatalf("claim: expected 200, got %d", rec.Code) }

	var resp a2a.JSONRPCResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Error != nil { t.Fatalf("claim error: %v", resp.Error) }
	result := resp.Result.(map[string]any)
	status := result["status"].(map[string]any)
	if status["state"] != "completed" {
		t.Fatalf("expected completed, got %v", status["state"])
	}

	// 3. Get task
	body, _ = json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tasks/get",
		"params": map[string]any{"id": "task-claim-1"},
	})
	req = httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("X-Api-Key", "ak_test")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Error != nil { t.Fatalf("get error: %v", resp.Error) }
	result = resp.Result.(map[string]any)
	if result["id"] != "task-claim-1" {
		t.Fatalf("expected task-claim-1, got %v", result["id"])
	}

	// 4. Cancel task — create a working task directly so it can be canceled
	_ = p.CreateTask(context.Background(), "test", &hearsay.A2ATask{
		ID: "task-cancel-1", State: "working", Namespace: "test", CreatedAt: time.Now(),
	})
	body, _ = json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tasks/cancel",
		"params": map[string]any{"id": "task-cancel-1"},
	})
	req = httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("X-Api-Key", "ak_test")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Error != nil { t.Fatalf("cancel error: %v", resp.Error) }
	result = resp.Result.(map[string]any)
	status = result["status"].(map[string]any)
	if status["state"] != "canceled" {
		t.Fatalf("expected canceled, got %v", status["state"])
	}
}
