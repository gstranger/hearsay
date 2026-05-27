package server

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestRateLimiter_AllowsUnderLimit(t *testing.T) {
	rl := NewRateLimiter(5, 10)
	handler := rl.Wrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("POST", "/claim", nil)
		req.Header.Set("X-Agent-ID", "agent-1")
		rr := httptest.NewRecorder()
		handler(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, rr.Code)
		}
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	rl := NewRateLimiter(3, 10)
	handler := rl.Wrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("POST", "/claim", nil)
		req.Header.Set("X-Agent-ID", "agent-1")
		rr := httptest.NewRecorder()
		handler(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, rr.Code)
		}
	}

	req := httptest.NewRequest("POST", "/claim", nil)
	req.Header.Set("X-Agent-ID", "agent-1")
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}
}

func TestRateLimiter_PerAgentIsolation(t *testing.T) {
	rl := NewRateLimiter(2, 10)
	handler := rl.Wrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	makeReq := func(agentID string, expectedCode int) {
		req := httptest.NewRequest("POST", "/claim", nil)
		req.Header.Set("X-Agent-ID", agentID)
		rr := httptest.NewRecorder()
		handler(rr, req)
		if rr.Code != expectedCode {
			t.Errorf("%s: expected %d, got %d", agentID, expectedCode, rr.Code)
		}
	}
	makeReq("agent-1", http.StatusOK)
	makeReq("agent-1", http.StatusOK)
	makeReq("agent-1", http.StatusTooManyRequests)
	makeReq("agent-2", http.StatusOK)
}

func TestRateLimiter_DisabledWhenZeroLimit(t *testing.T) {
	rl := NewRateLimiter(0, 10)
	handler := rl.Wrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for i := 0; i < 100; i++ {
		req := httptest.NewRequest("POST", "/claim", nil)
		rr := httptest.NewRecorder()
		handler(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200 when disabled, got %d", i, rr.Code)
		}
	}
}

func TestRateLimiter_SetsHeaders(t *testing.T) {
	rl := NewRateLimiter(10, 10)
	handler := rl.Wrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/claim", nil)
	req.Header.Set("X-Agent-ID", "agent-1")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Header().Get("X-RateLimit-Limit") != "10" {
		t.Fatalf("expected limit 10, got %s", rr.Header().Get("X-RateLimit-Limit"))
	}
	if rr.Header().Get("X-RateLimit-Remaining") != "9" {
		t.Fatalf("expected remaining 9, got %s", rr.Header().Get("X-RateLimit-Remaining"))
	}
}

func TestRateLimiter_UnknownAgent(t *testing.T) {
	rl := NewRateLimiter(2, 10)
	handler := rl.Wrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/claim", nil)
		rr := httptest.NewRecorder()
		handler(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, rr.Code)
		}
	}

	req := httptest.NewRequest("POST", "/claim", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for unknown agent, got %d", rr.Code)
	}
}

func TestRateLimiter_Concurrent(t *testing.T) {
	rl := NewRateLimiter(100, 100)
	handler := rl.Wrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	var wg sync.WaitGroup
	errors := make(chan int, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/claim", nil)
			req.Header.Set("X-Agent-ID", "agent-1")
			rr := httptest.NewRecorder()
			handler(rr, req)
			if rr.Code != http.StatusOK {
				errors <- rr.Code
			}
		}()
	}
	wg.Wait()
	close(errors)
	if len(errors) > 0 {
		t.Fatalf("concurrent requests failed: %d errors", len(errors))
	}
}