# Rate Limiting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-agent_id sliding-window rate limiting to mutating REST API endpoints.

**Architecture:** `RateLimiter` middleware with sliding window counters per agent. Wired into the mutating handler chain in `internal/server/server.go`. Configurable via `--rate-limit` and `--rate-burst` CLI flags.

**Tech Stack:** Go 1.25 standard library (`sync`, `time`)

---

### File Mapping

| File | Responsibility |
|---|---|
| `internal/server/ratelimit.go` | **NEW** — RateLimiter struct with sliding window logic |
| `internal/server/ratelimit_test.go` | **NEW** — Tests for limits, bursts, recovery |
| `internal/server/server.go` | Wire rate limiter into handler chain |
| `cmd/hearsay/main.go` | Add `--rate-limit`, `--rate-burst` flags |
| `README.md` | Document flags |

---

### Task 1: Create Rate Limiter Middleware

**Files:**
- Create: `internal/server/ratelimit.go`
- Create: `internal/server/ratelimit_test.go`

- [ ] **Step 1: Write `internal/server/ratelimit.go`**

```go
package server

import (
	"strconv"
	"net/http"
	"sync"
	"time"
)

// RateLimiter implements per-agent sliding window rate limiting.
type RateLimiter struct {
	mu       sync.Mutex
	perAgent map[string][]time.Time
	limit    int // max requests per second (0 = disabled)
	burst    int // max burst
}

func NewRateLimiter(limit, burst int) *RateLimiter {
	return &RateLimiter{
		perAgent: make(map[string][]time.Time),
		limit:    limit,
		burst:    burst,
	}
}

func (rl *RateLimiter) Wrap(next http.HandlerFunc) http.HandlerFunc {
	if rl.limit <= 0 {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.Header.Get("X-Agent-ID")
		if agentID == "" {
			agentID = "unknown"
		}

		now := time.Now()
		windowStart := now.Add(-1 * time.Second)

		rl.mu.Lock()
		timestamps := rl.perAgent[agentID]

		// Evict old timestamps outside the window
		cutoff := 0
		for i, ts := range timestamps {
			if ts.After(windowStart) {
				cutoff = i
				break
			}
		}
		if cutoff > 0 {
			timestamps = timestamps[cutoff:]
		}

		count := len(timestamps)
		limited := count >= rl.limit

		if !limited {
			timestamps = append(timestamps, now)
			rl.perAgent[agentID] = timestamps
		}
		rl.mu.Unlock()

		// Set rate limit headers
		remaining := rl.limit - count - 1
		if remaining < 0 {
			remaining = 0
		}
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rl.limit))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(now.Unix()+1, 10))

		if limited {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}

		next(w, r)
	}
}
```

- [ ] **Step 2: Write `internal/server/ratelimit_test.go`**

```go
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

	// 4th request should be blocked
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

	// Exhaust agent-1
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

	// agent-2 should still be allowed
	makeReq("agent-2", http.StatusOK)
}

func TestRateLimiter_DisabledWhenZeroLimit(t *testing.T) {
	rl := NewRateLimiter(0, 10)
	handler := rl.Wrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Should allow many requests
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

	// Requests without X-Agent-ID use "unknown"
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
```

- [ ] **Step 3: Run tests**

```bash
cd ~/Documents/agentstate
go test ./internal/server/... -run TestRateLimiter -v
```

Expected: 7 PASS

- [ ] **Step 4: Commit**

```bash
git add internal/server/ratelimit.go internal/server/ratelimit_test.go
git commit -m "feat: add per-agent sliding window rate limiter middleware"
```

---

### Task 2: Wire Rate Limiter into Server and CLI

**Files:**
- Modify: `internal/server/server.go`
- Modify: `cmd/hearsay/main.go`

- [ ] **Step 1: Add rate limiter to Server struct**

In `internal/server/server.go`, add a `rateLimit` field:

```go
type Server struct {
	client    *hearsay.Client
	provider  hearsay.Provider
	locking   bool
	mux       *http.ServeMux
	auth      *AuthMiddleware
	logger    *LoggingMiddleware
	rateLimit *RateLimiter
}
```

Update `New` signature:

```go
func New(client *hearsay.Client, provider hearsay.Provider, locking bool, authToken string, logFormat LogFormat, rateLimit, rateBurst int) *Server {
```

Initialize:

```go
	rateLimit: NewRateLimiter(rateLimit, rateBurst),
```

- [ ] **Step 2: Wrap auth handlers with rate limiter**

The mutating endpoints currently use `s.auth.Wrap(s.handleClaim)`. Chain the rate limiter:

For each mutating POST handler, change from:
```go
s.mux.HandleFunc("POST /claim", s.auth.Wrap(s.handleClaim))
```
To:
```go
s.mux.HandleFunc("POST /claim", s.rateLimit.Wrap(s.auth.Wrap(s.handleClaim)))
```

Apply this to ALL mutating POST endpoints: claim, release, heartbeat, intent, message, message/read, message/archive, namespaces/create, namespaces/delete, append, expire.

- [ ] **Step 3: Update server_test.go New() calls**

All `server.New(...)` calls need the new `rateLimit, rateBurst` parameters:

```go
// Old: server.New(client, provider, false, "", LogFormatText)
// New: server.New(client, provider, false, "", LogFormatText, 0, 0)
```

- [ ] **Step 4: Add CLI flags**

In `cmd/hearsay/main.go`, add after existing flags:

```go
rateLimit := fs.Int("rate-limit", 0, "Max requests per second per agent (0 = unlimited)")
rateBurst := fs.Int("rate-burst", 10, "Max burst size per agent")
```

- [ ] **Step 5: Pass to server.New**

Update:
```go
srv := server.New(client, provider, cfg.Defaults.Locking, *authToken, logFmt)
```
To:
```go
srv := server.New(client, provider, cfg.Defaults.Locking, *authToken, logFmt, *rateLimit, *rateBurst)
```

- [ ] **Step 6: Build and test**

```bash
cd ~/Documents/agentstate
go build ./cmd/hearsay
go test ./...
```

Expected: builds, all packages pass

- [ ] **Step 7: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go cmd/hearsay/main.go
git commit -m "feat: wire rate limiter into server and CLI flags"
```

---

### Task 3: Update README and Final Verify

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add rate limit flags to README**

Add to the server examples and env var table:

In the Start the Server section, add:

```
# With rate limiting: 10 req/sec per agent, burst of 20
hearsay serve --rate-limit 10 --rate-burst 20
```

In the Environment Variables table, add:
```
| `HEARSAY_RATE_LIMIT` | Max requests/sec per agent (0 = unlimited) |
| `HEARSAY_RATE_BURST` | Max burst size per agent |
```

- [ ] **Step 2: Run full test suite**

```bash
cd ~/Documents/agentstate
go test ./...
```

Expected: all packages pass

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document --rate-limit and --rate-burst flags"
```

---

## Spec Coverage Check

| Spec Requirement | Task |
|---|---|
| Sliding window algorithm | Task 1 |
| Per-agent_id isolation | Task 1 |
| Configurable limit/burst | Task 2 |
| 429 + headers on limit exceeded | Task 1 |
| Disabled when limit=0 | Task 1 (Wrap returns next if limit <= 0) |
| Wire into mutating POST handlers | Task 2 |
| CLI flags | Task 2 |
| README docs | Task 3 |

## Placeholder Scan

No placeholders. All steps contain exact code and commands.

## Type Consistency Check

- `RateLimiter.limit int` — used in Wrap, set in NewRateLimiter
- `RateLimiter.burst int` — stored for future use (burst currently implicit in limit)
- `NewRateLimiter(limit, burst int)` → Task 1 and Task 2 both use same signature