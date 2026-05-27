package a2a

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gstranger/hearsay/internal/memory"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

func TestHandleTasksSendClaimResource(t *testing.T) {
	p := memory.New()
	_ = p.CreateNamespace(context.Background(), hearsay.Namespace{ID: "test", CreatedAt: time.Now()})
	client := hearsay.NewClient(p, "test")
	cfg := &hearsay.A2AConfig{Addr: "localhost:8081"}
	mw := &AuthMiddleware{}
	srv := NewServer(cfg, client, p, mw, "test")

	params, _ := json.Marshal(map[string]any{
		"id": "task-claim-1",
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

	resp, err := srv.handleTasksSend(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	status := result["status"].(map[string]any)
	if status["state"] != "completed" {
		t.Fatalf("expected completed, got %v", status["state"])
	}
}

func TestHandleTasksGet(t *testing.T) {
	p := memory.New()
	_ = p.CreateNamespace(context.Background(), hearsay.Namespace{ID: "test", CreatedAt: time.Now()})
	client := hearsay.NewClient(p, "test")
	cfg := &hearsay.A2AConfig{Addr: "localhost:8081"}
	mw := &AuthMiddleware{}
	srv := NewServer(cfg, client, p, mw, "test")

	// First create a task
	params, _ := json.Marshal(map[string]any{
		"id": "task-get-1",
		"message": map[string]any{
			"role": "user",
			"parts": []map[string]any{
				{"type": "data", "data": map[string]any{
					"skill_id":     "check_conflict",
					"resource_uri": "file://x.ts",
					"operation":    "write",
				}},
			},
		},
	})
	srv.handleTasksSend(context.Background(), params)

	// Now get it
	params, _ = json.Marshal(map[string]any{"id": "task-get-1"})
	resp, err := srv.handleTasksGet(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	if result["id"] != "task-get-1" {
		t.Fatalf("expected task-get-1, got %v", result["id"])
	}
}

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
