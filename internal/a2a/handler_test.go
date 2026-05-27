package a2a

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/thunder/agentstate/internal/memory"
	"github.com/thunder/agentstate/pkg/agentstate"
)

func TestHandleTasksSendClaimResource(t *testing.T) {
	p := memory.New()
	_ = p.CreateNamespace(context.Background(), agentstate.Namespace{ID: "test", CreatedAt: time.Now()})
	client := agentstate.NewClient(p, "test")
	cfg := &agentstate.A2AConfig{Addr: "localhost:8081"}
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
	_ = p.CreateNamespace(context.Background(), agentstate.Namespace{ID: "test", CreatedAt: time.Now()})
	client := agentstate.NewClient(p, "test")
	cfg := &agentstate.A2AConfig{Addr: "localhost:8081"}
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
