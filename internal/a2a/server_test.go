package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gstranger/hearsay/internal/memory"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

func TestServerAgentCard(t *testing.T) {
	cfg := &hearsay.A2AConfig{Addr: "localhost:8081", APIKey: "ak"}
	p := memory.New()
	_ = p.CreateNamespace(context.Background(), hearsay.Namespace{ID: "test", CreatedAt: time.Now()})
	client := hearsay.NewClient(p, "test")
	mw := &AuthMiddleware{APIKey: "ak"}
	srv := NewServer(cfg, client, p, mw, "test")

	req := httptest.NewRequest("GET", "/.well-known/agent.json", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var card AgentCard
	if err := json.Unmarshal(rec.Body.Bytes(), &card); err != nil {
		t.Fatal(err)
	}
	if card.Name != "hearsay-coordinator" {
		t.Fatal("name mismatch")
	}
}

func TestServerJSONRPC(t *testing.T) {
	cfg := &hearsay.A2AConfig{Addr: "localhost:8081", APIKey: "ak"}
	p := memory.New()
	_ = p.CreateNamespace(context.Background(), hearsay.Namespace{ID: "test", CreatedAt: time.Now()})
	client := hearsay.NewClient(p, "test")
	mw := &AuthMiddleware{APIKey: "ak"}
	srv := NewServer(cfg, client, p, mw, "test")

	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tasks/get", "params": map[string]any{"id": "nonexistent"},
	})
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("X-Api-Key", "ak")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var resp JSONRPCResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Error == nil || resp.Error.Code != -32000 {
		t.Fatalf("expected task not found error, got %+v", resp.Error)
	}
}
