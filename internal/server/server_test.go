package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thunder/agentstate/internal/memory"
	"github.com/thunder/agentstate/pkg/agentstate"
)

func setupTestServer(t *testing.T) (*Server, func()) {
	ctx := context.Background()
	p := memory.New()
	if err := p.CreateNamespace(ctx, agentstate.Namespace{ID: "test"}); err != nil {
		t.Fatal(err)
	}
	client := agentstate.NewClient(p, "test")
	return New(client, p, false), func() {}
}

func post(t *testing.T, srv *Server, path, body string) *http.Response {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec.Result()
}

func get(t *testing.T, srv *Server, path string) *http.Response {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec.Result()
}

func TestHandleClaim(t *testing.T) {
	s, _ := setupTestServer(t)
	reqBody, _ := json.Marshal(agentstate.ClaimRequest{
		ResourceURI: "file://x.ts",
		AgentID:     "a1",
		Operation:   agentstate.OpWrite,
		Intent:      "test",
	})
	req := httptest.NewRequest("POST", "/claim", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp agentstate.ClaimResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.ClaimID == "" {
		t.Fatal("expected claim ID")
	}
}

func TestHandleClaim_Conflict(t *testing.T) {
	s, _ := setupTestServer(t)
	// First claim
	reqBody, _ := json.Marshal(agentstate.ClaimRequest{
		ResourceURI: "file://x.ts", AgentID: "a1", Operation: agentstate.OpWrite, Intent: "first",
	})
	s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/claim", bytes.NewReader(reqBody)))

	// Second claim — conflict
	reqBody2, _ := json.Marshal(agentstate.ClaimRequest{
		ResourceURI: "file://x.ts", AgentID: "a2", Operation: agentstate.OpWrite, Intent: "second",
	})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("POST", "/claim", bytes.NewReader(reqBody2)))

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}

func TestHandleCheck(t *testing.T) {
	s, _ := setupTestServer(t)
	// Claim first
	reqBody, _ := json.Marshal(agentstate.ClaimRequest{
		ResourceURI: "file://x.ts", AgentID: "a1", Operation: agentstate.OpWrite, Intent: "x",
	})
	s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/claim", bytes.NewReader(reqBody)))

	// Check
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/check?resource=file://x.ts&operation=write", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var report agentstate.ConflictReport
	json.Unmarshal(rec.Body.Bytes(), &report)
	if !report.HasConflict {
		t.Fatal("expected conflict")
	}
}

func TestSendMessage(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	body := `{"namespace":"test","from":"agent-A","to":"agent-B","type":"note","content":"hello","ttl_seconds":300}`
	resp := post(t, srv, "/message", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["message_id"] == "" {
		t.Fatal("expected message_id")
	}
}

func TestGetMailbox(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	body := `{"namespace":"test","from":"agent-A","to":"agent-B","type":"note","content":"hello","ttl_seconds":300}`
	post(t, srv, "/message", body)

	resp := get(t, srv, "/mailbox?namespace=test&agent_id=agent-B")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var msgs []agentstate.MailboxMessage
	json.NewDecoder(resp.Body).Decode(&msgs)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "hello" {
		t.Fatalf("expected hello, got %s", msgs[0].Content)
	}
}

func TestMarkRead(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	body := `{"namespace":"test","from":"agent-A","to":"agent-B","type":"note","content":"hello","ttl_seconds":300}`
	post(t, srv, "/message", body)

	resp := get(t, srv, "/mailbox?namespace=test&agent_id=agent-B&unread=true")
	var msgs []agentstate.MailboxMessage
	json.NewDecoder(resp.Body).Decode(&msgs)
	msgID := msgs[0].MessageID

	body = fmt.Sprintf(`{"namespace":"test","message_id":"%s"}`, msgID)
	resp = post(t, srv, "/message/read", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	resp = get(t, srv, "/mailbox?namespace=test&agent_id=agent-B&unread=true")
	json.NewDecoder(resp.Body).Decode(&msgs)
	if len(msgs) != 0 {
		t.Fatalf("expected 0 unread, got %d", len(msgs))
	}
}

func TestArchiveMessage(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	body := `{"namespace":"test","from":"agent-A","to":"agent-B","type":"note","content":"hello","ttl_seconds":300}`
	post(t, srv, "/message", body)

	resp := get(t, srv, "/mailbox?namespace=test&agent_id=agent-B")
	var msgs []agentstate.MailboxMessage
	json.NewDecoder(resp.Body).Decode(&msgs)
	msgID := msgs[0].MessageID

	body = fmt.Sprintf(`{"namespace":"test","message_id":"%s"}`, msgID)
	resp = post(t, srv, "/message/archive", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	resp = get(t, srv, "/mailbox?namespace=test&agent_id=agent-B")
	json.NewDecoder(resp.Body).Decode(&msgs)
	if len(msgs) != 0 {
		t.Fatalf("expected 0 archived, got %d", len(msgs))
	}
}

func TestInvalidMessageType(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	body := `{"namespace":"test","from":"agent-A","to":"agent-B","type":"invalid","content":"hello"}`
	resp := post(t, srv, "/message", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}
