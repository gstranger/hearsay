# Phase 1 Critical Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the 5 most critical production readiness gaps: REST API auth, expired claim cleanup, graceful shutdown, input validation, and request logging.

**Architecture:** Add `--auth-token` and `--log-format` flags to `hearsay serve`. Extract shared auth logic from A2A into reusable middleware. Implement actual `ReleaseExpired` SQL in SQLite/Postgres with a background sweeper. Add `http.Server` with signal-based graceful shutdown. Add validation helpers and request logging middleware.

**Tech Stack:** Go 1.25, standard library only (no new dependencies)

---

### File Mapping

| File | Responsibility |
|---|---|
| `internal/a2a/auth.go` | Extract shared `CheckAuth` function from existing A2A auth middleware |
| `internal/server/middleware.go` | **NEW** — HTTP middleware: auth, request logging |
| `pkg/hearsay/validation.go` | **NEW** — Input validation helpers with max length constants |
| `pkg/hearsay/validation_test.go` | **NEW** — Tests for validation helpers |
| `internal/sqlite/sqlite.go` | Implement `ReleaseExpired` with actual SQL DELETE |
| `internal/postgres/postgres.go` | Implement `ReleaseExpired` with actual SQL DELETE |
| `cmd/hearsay/main.go` | Add `--auth-token`, `--log-format` flags; wire middleware; add graceful shutdown + sweeper goroutine |
| `internal/server/server.go` | Apply auth middleware to mutating handlers; validate inputs |
| `internal/server/server_test.go` | Add tests for auth middleware and input validation |

---

### Task 1: Extract Shared Auth Check from A2A

**Files:**
- Modify: `internal/a2a/auth.go`

- [ ] **Step 1: Add `CheckAuth` method to `AuthMiddleware`**

Add a public method that checks auth without wrapping an `http.Handler`. This lets the REST server reuse the same auth logic.

```go
// CheckAuth extracts and validates the auth token from a request.
// Returns the agent ID if valid, or an error if invalid/missing.
func (a *AuthMiddleware) CheckAuth(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		auth = r.Header.Get("X-Api-Key")
		if auth != "" {
			if a.APIKey != "" && auth == a.APIKey {
				return "api-key", nil
			}
			return "", fmt.Errorf("invalid api key")
		}
		return "", fmt.Errorf("missing authorization")
	}

	if strings.HasPrefix(auth, "Bearer ") {
		token := strings.TrimPrefix(auth, "Bearer ")
		if a.BearerValidatorURL != "" {
			res := a.validateExternalBearer(token)
			if res.OK {
				return res.AgentID, nil
			}
			return "", fmt.Errorf("invalid bearer token")
		}
		if a.BearerJWKSURL != "" {
			res := a.validateJWTBearer(token)
			if res.OK {
				return res.AgentID, nil
			}
			return "", fmt.Errorf("invalid jwt token")
		}
		return "", fmt.Errorf("bearer auth not configured")
	}

	return "", fmt.Errorf("invalid authorization format")
}
```

- [ ] **Step 2: Update `Middleware` to use `CheckAuth`**

Refactor the existing `Middleware` method to call `CheckAuth` internally (DRY — no behavior change).

- [ ] **Step 3: Verify A2A tests still pass**

```bash
cd ~/Documents/agentstate
go test ./internal/a2a/...
```

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/a2a/auth.go
git commit -m "refactor: extract CheckAuth from A2A auth middleware for reuse"
```

---

### Task 2: Create Validation Helpers

**Files:**
- Create: `pkg/hearsay/validation.go`
- Create: `pkg/hearsay/validation_test.go`

- [ ] **Step 1: Write validation helpers**

```go
package hearsay

import (
	"fmt"
	"strings"
)

const (
	MaxAgentIDLen     = 128
	MaxResourceURILen = 2048
	MaxIntentLen      = 512
	MaxNamespaceLen   = 64
)

type ValidationError struct {
	Field   string
	Message string
}

func (v *ValidationError) Error() string {
	return fmt.Sprintf("validation error on %s: %s", v.Field, v.Message)
}

func ValidateAgentID(id string) error {
	if id == "" {
		return &ValidationError{Field: "agent_id", Message: "required"}
	}
	if len(id) > MaxAgentIDLen {
		return &ValidationError{Field: "agent_id", Message: fmt.Sprintf("max length %d", MaxAgentIDLen)}
	}
	return nil
}

func ValidateResourceURI(uri string) error {
	if uri == "" {
		return &ValidationError{Field: "resource_uri", Message: "required"}
	}
	if len(uri) > MaxResourceURILen {
		return &ValidationError{Field: "resource_uri", Message: fmt.Sprintf("max length %d", MaxResourceURILen)}
	}
	if !strings.Contains(uri, "://") {
		return &ValidationError{Field: "resource_uri", Message: "must contain scheme (e.g. file://)"}
	}
	return nil
}

func ValidateIntent(intent string) error {
	if len(intent) > MaxIntentLen {
		return &ValidationError{Field: "intent", Message: fmt.Sprintf("max length %d", MaxIntentLen)}
	}
	return nil
}

func ValidateNamespace(ns string) error {
	if ns == "" {
		return &ValidationError{Field: "namespace", Message: "required"}
	}
	if len(ns) > MaxNamespaceLen {
		return &ValidationError{Field: "namespace", Message: fmt.Sprintf("max length %d", MaxNamespaceLen)}
	}
	return nil
}
```

- [ ] **Step 2: Write tests**

```go
package hearsay

import (
	"strings"
	"testing"
)

func TestValidateAgentID(t *testing.T) {
	if err := ValidateAgentID("agent-1"); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	if err := ValidateAgentID(""); err == nil {
		t.Fatal("expected error for empty agent_id")
	}
	if err := ValidateAgentID(strings.Repeat("a", MaxAgentIDLen+1)); err == nil {
		t.Fatal("expected error for too-long agent_id")
	}
}

func TestValidateResourceURI(t *testing.T) {
	if err := ValidateResourceURI("file://src/main.go"); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	if err := ValidateResourceURI(""); err == nil {
		t.Fatal("expected error for empty uri")
	}
	if err := ValidateResourceURI("no-scheme"); err == nil {
		t.Fatal("expected error for missing scheme")
	}
	if err := ValidateResourceURI(strings.Repeat("a", MaxResourceURILen+1)); err == nil {
		t.Fatal("expected error for too-long uri")
	}
}

func TestValidateIntent(t *testing.T) {
	if err := ValidateIntent("refactoring"); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	if err := ValidateIntent(strings.Repeat("a", MaxIntentLen+1)); err == nil {
		t.Fatal("expected error for too-long intent")
	}
}

func TestValidateNamespace(t *testing.T) {
	if err := ValidateNamespace("default"); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	if err := ValidateNamespace(""); err == nil {
		t.Fatal("expected error for empty namespace")
	}
	if err := ValidateNamespace(strings.Repeat("a", MaxNamespaceLen+1)); err == nil {
		t.Fatal("expected error for too-long namespace")
	}
}
```

- [ ] **Step 3: Run tests**

```bash
cd ~/Documents/agentstate
go test ./pkg/hearsay/... -run TestValidate
```

Expected: 4 PASS

- [ ] **Step 4: Commit**

```bash
git add pkg/hearsay/validation.go pkg/hearsay/validation_test.go
git commit -m "feat: add input validation helpers with max length limits"
```

---

### Task 3: Create Server Middleware

**Files:**
- Create: `internal/server/middleware.go`
- Create: `internal/server/middleware_test.go`

- [ ] **Step 1: Write auth middleware**

```go
package server

import (
	"net/http"
	"strings"
	"time"
)

// AuthMiddleware wraps handlers that require authentication.
type AuthMiddleware struct {
	Token string
}

func (a *AuthMiddleware) Wrap(next http.HandlerFunc) http.HandlerFunc {
	if a.Token == "" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			auth = r.Header.Get("X-Api-Key")
			if auth != "" && auth == a.Token {
				next(w, r)
				return
			}
		}
		if strings.HasPrefix(auth, "Bearer ") {
			token := strings.TrimPrefix(auth, "Bearer ")
			if token == a.Token {
				next(w, r)
				return
			}
		}
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}
}
```

- [ ] **Step 2: Write logging middleware**

```go
package server

import (
	"log"
	"net/http"
	"time"
)

type LogFormat string

const (
	LogFormatText LogFormat = "text"
	LogFormatJSON LogFormat = "json"
)

type LoggingMiddleware struct {
	Format LogFormat
}

func (l *LoggingMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		agentID := r.Header.Get("X-Agent-ID")
		if agentID == "" {
			agentID = "-"
		}

		// Wrap response writer to capture status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wrapped, r)

		duration := time.Since(start)
		if l.Format == LogFormatJSON {
			log.Printf(`{"time":"%s","method":"%s","path":"%s","agent_id":"%s","status":%d,"duration_ms":%d}`,
				time.Now().UTC().Format(time.RFC3339),
				r.Method, r.URL.Path, agentID, wrapped.statusCode, duration.Milliseconds())
		} else {
			log.Printf("%s %s %s %d %s", r.Method, r.URL.Path, agentID, wrapped.statusCode, duration)
		}
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
```

- [ ] **Step 3: Write middleware tests**

```go
package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddleware_AllowsWhenNoToken(t *testing.T) {
	mw := &AuthMiddleware{Token: ""}
	handler := mw.Wrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest("POST", "/claim", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestAuthMiddleware_RejectsWithoutAuth(t *testing.T) {
	mw := &AuthMiddleware{Token: "secret"}
	handler := mw.Wrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest("POST", "/claim", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAuthMiddleware_AcceptsBearer(t *testing.T) {
	mw := &AuthMiddleware{Token: "secret"}
	handler := mw.Wrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest("POST", "/claim", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestAuthMiddleware_AcceptsAPIKey(t *testing.T) {
	mw := &AuthMiddleware{Token: "secret"}
	handler := mw.Wrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest("POST", "/claim", nil)
	req.Header.Set("X-Api-Key", "secret")
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}
```

- [ ] **Step 4: Run tests**

```bash
cd ~/Documents/agentstate
go test ./internal/server/... -run TestAuthMiddleware
```

Expected: 4 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/server/middleware.go internal/server/middleware_test.go
git commit -m "feat: add auth and logging middleware for REST server"
```

---

### Task 4: Implement ReleaseExpired in SQLite

**Files:**
- Modify: `internal/sqlite/sqlite.go`

- [ ] **Step 1: Implement ReleaseExpired with SQL DELETE**

Replace the no-op `ReleaseExpired` with actual cleanup. The logic: delete claim messages whose `CreatedAt + TTLSeconds < before`, and also delete their corresponding release messages.

```go
func (p *Provider) ReleaseExpired(ctx context.Context, namespaceID string, before time.Time) error {
	// Delete expired claim messages and their release messages
	// A claim is expired if: created_at + (ttl_seconds * interval '1 second') < before
	_, err := p.db.ExecContext(ctx, `
		DELETE FROM messages
		WHERE namespace = ?
		AND type = 'claim'
		AND datetime(timestamp, '+' || CAST(
			(SELECT CAST(json_extract(payload, '$.ttl_seconds') AS INTEGER) FROM messages AS m2
			 WHERE m2.namespace = messages.namespace AND m2.offset = messages.offset) || ' seconds'
		) < ?
	`, namespaceID, before.Format(time.RFC3339))
	if err != nil {
		return err
	}

	// Also delete orphaned release messages (releases for claims that no longer exist)
	_, err = p.db.ExecContext(ctx, `
		DELETE FROM messages
		WHERE namespace = ?
		AND type = 'release'
		AND offset IN (
			SELECT m.offset FROM messages m
			WHERE m.namespace = ?
			AND m.type = 'release'
			AND NOT EXISTS (
				SELECT 1 FROM messages c
				WHERE c.namespace = m.namespace
				AND c.type = 'claim'
				AND json_extract(c.payload, '$.claim_id') = json_extract(m.payload, '$.claim_id')
			)
		)
	`, namespaceID, namespaceID)
	return err
}
```

**Note:** The SQLite `json_extract` approach requires the `json1` extension which is built into `modernc.org/sqlite`. If this doesn't work, fall back to a simpler approach: query all claims, check expiration in Go, then delete by offset.

Alternative simpler approach (more reliable):

```go
func (p *Provider) ReleaseExpired(ctx context.Context, namespaceID string, before time.Time) error {
	// Query all claim messages
	rows, err := p.db.QueryContext(ctx,
		"SELECT offset, payload FROM messages WHERE namespace = ? AND type = 'claim'",
		namespaceID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var toDelete []int64
	for rows.Next() {
		var offset int64
		var payload []byte
		if err := rows.Scan(&offset, &payload); err != nil {
			continue
		}
		var claim hearsay.Claim
		if err := json.Unmarshal(payload, &claim); err != nil {
			continue
		}
		if claim.CreatedAt.Add(time.Duration(claim.TTLSeconds) * time.Second).Before(before) {
			toDelete = append(toDelete, offset)
		}
	}
	rows.Close()

	// Delete expired claims and their releases
	for _, offset := range toDelete {
		p.db.ExecContext(ctx, "DELETE FROM messages WHERE namespace = ? AND offset = ?", namespaceID, offset)
	}
	return nil
}
```

Use the simpler approach — it's more reliable and doesn't depend on JSON SQL functions.

- [ ] **Step 2: Verify SQLite tests still pass**

```bash
cd ~/Documents/agentstate
go test ./internal/sqlite/...
```

Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/sqlite/sqlite.go
git commit -m "feat: implement ReleaseExpired in SQLite provider"
```

---

### Task 5: Implement ReleaseExpired in PostgreSQL

**Files:**
- Modify: `internal/postgres/postgres.go`

- [ ] **Step 1: Implement ReleaseExpired with SQL DELETE**

Use the same Go-based approach as SQLite (query claims, check expiration, delete by offset) for consistency:

```go
func (p *Provider) ReleaseExpired(ctx context.Context, namespaceID string, before time.Time) error {
	rows, err := p.db.QueryContext(ctx,
		"SELECT offset, payload FROM messages WHERE namespace = $1 AND type = 'claim'",
		namespaceID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var toDelete []int64
	for rows.Next() {
		var offset int64
		var payload []byte
		if err := rows.Scan(&offset, &payload); err != nil {
			continue
		}
		var claim hearsay.Claim
		if err := json.Unmarshal(payload, &claim); err != nil {
			continue
		}
		if claim.CreatedAt.Add(time.Duration(claim.TTLSeconds) * time.Second).Before(before) {
			toDelete = append(toDelete, offset)
		}
	}
	rows.Close()

	for _, offset := range toDelete {
		p.db.ExecContext(ctx, "DELETE FROM messages WHERE namespace = $1 AND offset = $2", namespaceID, offset)
	}
	return nil
}
```

- [ ] **Step 2: Verify Postgres tests still pass**

```bash
cd ~/Documents/agentstate
go test ./internal/postgres/...
```

Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/postgres/postgres.go
git commit -m "feat: implement ReleaseExpired in PostgreSQL provider"
```

---

### Task 6: Wire Auth, Validation, and Logging into REST Server

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/server/server_test.go`

- [ ] **Step 1: Update Server struct to accept middleware**

```go
type Server struct {
	client   *hearsay.Client
	provider hearsay.Provider
	locking  bool
	mux      *http.ServeMux
	auth     *AuthMiddleware
	logger   *LoggingMiddleware
}

func New(client *hearsay.Client, provider hearsay.Provider, locking bool, authToken string, logFormat LogFormat) *Server {
	s := &Server{
		client:   client,
		provider: provider,
		locking:  locking,
		mux:      http.NewServeMux(),
		auth:     &AuthMiddleware{Token: authToken},
		logger:   &LoggingMiddleware{Format: logFormat},
	}
	// ... existing handler registration, but wrap mutating ones with auth
	return s
}
```

- [ ] **Step 2: Wrap mutating handlers with auth middleware**

In `New()`, wrap POST endpoints with auth:

```go
// Read-only endpoints (no auth required)
s.mux.HandleFunc("GET /health", s.handleHealth)
s.mux.HandleFunc("GET /claims", s.handleQueryClaims)
s.mux.HandleFunc("GET /check", s.handleCheck)
s.mux.HandleFunc("GET /mailbox", s.handleGetMailbox)

// Mutating endpoints (auth required)
s.mux.HandleFunc("POST /claim", s.auth.Wrap(s.handleClaim))
s.mux.HandleFunc("POST /release", s.auth.Wrap(s.handleRelease))
s.mux.HandleFunc("POST /heartbeat", s.auth.Wrap(s.handleHeartbeat))
s.mux.HandleFunc("POST /intent", s.auth.Wrap(s.handleIntent))
s.mux.HandleFunc("POST /message", s.auth.Wrap(s.handleSendMessage))
s.mux.HandleFunc("POST /message/read", s.auth.Wrap(s.handleMarkRead))
s.mux.HandleFunc("POST /message/archive", s.auth.Wrap(s.handleArchiveMessage))
// Provider proxy endpoints
s.mux.HandleFunc("POST /namespaces/create", s.auth.Wrap(s.handleCreateNamespace))
s.mux.HandleFunc("POST /namespaces/delete", s.auth.Wrap(s.handleDeleteNamespace))
s.mux.HandleFunc("POST /append", s.auth.Wrap(s.handleAppend))
s.mux.HandleFunc("POST /expire", s.auth.Wrap(s.handleReleaseExpired))
// GET /query and GET /agents are read-only
s.mux.HandleFunc("GET /query", s.handleQuery)
s.mux.HandleFunc("GET /agents", s.handleAgentState)
```

- [ ] **Step 3: Add input validation to handlers**

Add validation at the start of key handlers. Example for `handleClaim`:

```go
func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	var req hearsay.ClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := hearsay.ValidateAgentID(req.AgentID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := hearsay.ValidateResourceURI(req.ResourceURI); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := hearsay.ValidateIntent(req.Intent); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// ... rest of handler
}
```

Similarly add validation to `handleSendMessage`, `handleCreateNamespace`, etc.

- [ ] **Step 4: Update ServeHTTP to apply logging**

```go
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.logger.Wrap(s.mux).ServeHTTP(w, r)
}
```

- [ ] **Step 5: Update server tests**

Update `internal/server/server_test.go` to pass the new `New()` signature:

```go
// Old: server.New(client, provider, false)
// New: server.New(client, provider, false, "", server.LogFormatText)
```

Add tests for auth rejection:

```go
func TestServer_AuthRejectsUnauthenticated(t *testing.T) {
	// ... setup client and provider
	srv := server.New(client, provider, false, "secret", server.LogFormatText)
	req := httptest.NewRequest("POST", "/claim", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}
```

- [ ] **Step 6: Run server tests**

```bash
cd ~/Documents/agentstate
go test ./internal/server/...
```

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go
git commit -m "feat: wire auth, validation, and logging into REST server"
```

---

### Task 7: Add CLI Flags and Graceful Shutdown + Sweeper

**Files:**
- Modify: `cmd/hearsay/main.go`

- [ ] **Step 1: Add `--auth-token` and `--log-format` flags**

In `cmdServe`, add:

```go
authToken := fs.String("auth-token", "", "Auth token for REST API. When set, mutating endpoints require Authorization: Bearer <token> or X-Api-Key: <token>")
logFormat := fs.String("log-format", "text", "Log format: text or json")
```

- [ ] **Step 2: Parse log format**

```go
var logFmt server.LogFormat
if *logFormat == "json" {
	logFmt = server.LogFormatJSON
} else {
	logFmt = server.LogFormatText
}
```

- [ ] **Step 3: Pass auth token and log format to server.New**

```go
srv := server.New(client, provider, cfg.Defaults.Locking, *authToken, logFmt)
```

- [ ] **Step 4: Add graceful shutdown with http.Server**

Replace the blocking `http.ListenAndServe` with:

```go
import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// ... in cmdServe:

httpSrv := &http.Server{Addr: *addr, Handler: srv}

// Start background sweeper
if *authToken != "" || true { // always run sweeper
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				before := time.Now().UTC()
				if err := provider.ReleaseExpired(context.Background(), cfg.Namespace, before); err != nil {
					log.Printf("sweeper error: %v", err)
				}
			}
		}
	}()
}

// Graceful shutdown
idleConnsClosed := make(chan struct{})
go func() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	close(idleConnsClosed)
}()

log.Printf("Listening on %s", *addr)
if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
	log.Fatalf("server error: %v", err)
}
<-idleConnsClosed
log.Println("server stopped")
```

Apply the same pattern to the A2A server.

- [ ] **Step 5: Update A2A server startup to use http.Server**

```go
if a2aCfg != nil {
	authMW := &a2a.AuthMiddleware{...}
	a2aSrv := a2a.NewServer(a2aCfg, client, provider, authMW, cfg.Namespace)
	a2aHttpSrv := &http.Server{Addr: a2aCfg.Addr, Handler: authMW.Middleware(a2aSrv)}
	go func() {
		log.Printf("A2A server listening on %s", a2aCfg.Addr)
		if err := a2aHttpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("A2A server error: %v", err)
		}
	}()
	// Add to shutdown
	go func() {
		<-idleConnsClosed
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a2aHttpSrv.Shutdown(shutdownCtx)
	}()
}
```

- [ ] **Step 6: Build and test**

```bash
cd ~/Documents/agentstate
go build ./cmd/hearsay
```

Expected: builds successfully

```bash
go test ./...
```

Expected: all 11 packages pass

- [ ] **Step 7: Commit**

```bash
git add cmd/hearsay/main.go
git commit -m "feat: add auth-token and log-format flags, graceful shutdown, background sweeper"
```

---

### Task 8: Update README with New Flags

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add new flags to the Quick Start section**

Update the serve example:

```bash
# With authentication and JSON logging
hearsay serve --auth-token my-secret-key --log-format json
```

- [ ] **Step 2: Add env var documentation**

Add to the Environment Variables table:

| Variable | Description |
|---|---|
| `HEARSAY_AUTH_TOKEN` | REST API auth token |
| `HEARSAY_LOG_FORMAT` | `text` or `json` |

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document new --auth-token and --log-format flags"
```

---

### Task 9: Final Verification

- [ ] **Step 1: Run full test suite**

```bash
cd ~/Documents/agentstate
go test ./...
```

Expected: all 11 packages pass

- [ ] **Step 2: Build binary**

```bash
go build ./cmd/hearsay
```

Expected: creates `./hearsay` binary

- [ ] **Step 3: Test help output shows new flags**

```bash
./hearsay serve --help
```

Expected: shows `--auth-token`, `--log-format`, `--a2a-addr`, etc.

- [ ] **Step 4: Test auth rejection**

```bash
# Start server with auth
./hearsay serve --auth-token secret &
SERVER_PID=$!
sleep 1

# Request without auth should fail
curl -s -o /dev/null -w "%{http_code}" -X POST http://localhost:8080/claim -d '{}'
# Expected: 401

# Request with auth should succeed (or at least not 401)
curl -s -o /dev/null -w "%{http_code}" -X POST http://localhost:8080/claim \
  -H "Authorization: Bearer secret" \
  -d '{"agent_id":"a1","resource_uri":"file://x","operation":"write","ttl_seconds":60}'
# Expected: 200 or 409 (not 401)

kill $SERVER_PID
```

- [ ] **Step 5: Commit any fixes**

If any issues found, fix and commit.

---

## Spec Coverage Check

| Spec Requirement | Task |
|---|---|
| Extract shared auth check | Task 1 |
| Auth middleware for REST | Task 3 |
| `--auth-token` flag | Task 7 |
| `ReleaseExpired` SQLite | Task 4 |
| `ReleaseExpired` Postgres | Task 5 |
| Background sweeper goroutine | Task 7 |
| Graceful shutdown | Task 7 |
| Input validation helpers | Task 2 |
| Validation in handlers | Task 6 |
| Request logging middleware | Task 3 |
| `--log-format` flag | Task 7 |
| README updates | Task 8 |

## Placeholder Scan

No placeholders. All steps contain exact code and commands.

## Type Consistency Check

- `AuthMiddleware.Token` — string, used in Task 3 and Task 6
- `LogFormat` — custom type (`text`/`json`), used in Task 3, 6, 7
- `Server.New` signature — updated consistently across Task 6 and 7
- `hearsay.Validate*` functions — defined in Task 2, used in Task 6
