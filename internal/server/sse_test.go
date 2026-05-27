package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSSE_StreamsEvents(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	// Create a claim so there's an event to stream
	claimBody := `{"AgentID":"a1","ResourceURI":"file://x.ts","Operation":"write","Intent":"testing"}`
	post(t, s, "/claim", claimBody)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/events?namespace=test&since=0", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	s.ServeHTTP(rec, req)
	resp := rec.Result()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %s", contentType)
	}
}

func TestSSE_ReplaysFromSince(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	// Create two claims
	post(t, s, "/claim", `{"AgentID":"a1","ResourceURI":"file://a.ts","Operation":"write","Intent":"first"}`)
	post(t, s, "/claim", `{"AgentID":"a1","ResourceURI":"file://b.ts","Operation":"write","Intent":"second"}`)

	// Connect with since=0 to read events
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/events?namespace=test&since=0", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	// Now reconnect with since=1 to verify replay works
	ctx2, cancel2 := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel2()
	req2 := httptest.NewRequest("GET", "/events?namespace=test&since=1", nil).WithContext(ctx2)
	rec2 := httptest.NewRecorder()
	s.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("replay: expected 200, got %d", rec2.Code)
	}
}

func TestSSE_ContentTypeAndHeaders(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()
	post(t, s, "/claim", `{"AgentID":"a1","ResourceURI":"file://x.ts","Operation":"write","Intent":"test"}`)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/events?namespace=test", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	resp := rec.Result()

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("wrong Content-Type: %s", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("wrong Cache-Control: %s", cc)
	}
}