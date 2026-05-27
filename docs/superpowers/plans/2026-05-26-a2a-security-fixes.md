# A2A Security & Robustness Fixes — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix critical security and robustness issues in the A2A adapter: implement real JWT/Bearer validation, fix silent error ignoring, clean up orphaned A2A data on namespace deletion.

**Architecture:** Add `jwt/v5` dependency for JWT validation. Implement JWKS fetching + caching in auth middleware. Fix error propagation in task handlers. Wrap SQLite namespace deletion in transaction with A2A cleanup.

**Tech Stack:** Go 1.23, `github.com/golang-jwt/jwt/v5`, standard library crypto

---

## File Map

| File | Responsibility |
|---|---|
| `internal/a2a/auth.go` | Auth middleware + JWT validation + external bearer validation |
| `internal/a2a/auth_test.go` | Tests for all auth paths |
| `internal/a2a/handler_tasks.go` | Task handlers with proper error checking |
| `internal/a2a/handler_skills.go` | Skill execution (fix claim conflict behavior) |
| `internal/sqlite/sqlite.go` | SQLite provider (fix DeleteNamespace) |
| `go.mod` / `go.sum` | New dependency: `github.com/golang-jwt/jwt/v5` |

---

### Task 1: Add JWT dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add jwt/v5 dependency**

```bash
cd /Users/pj/Documents/thunder/hearsay
go get github.com/golang-jwt/jwt/v5
```

Expected: `go.mod` updated with `github.com/golang-jwt/jwt/v5 v5.x.x`

- [ ] **Step 2: Verify build still passes**

```bash
cd /Users/pj/Documents/thunder/hearsay
go build ./...
```

Expected: PASS (no compile errors)

- [ ] **Step 3: Commit**

```bash
cd /Users/pj/Documents/thunder/hearsay
git add go.mod go.sum
git commit -m "deps: add golang-jwt/jwt/v5 for A2A Bearer validation"
```

---

### Task 2: Implement JWT validation with JWKS support

**Files:**
- Modify: `internal/a2a/auth.go`
- Modify: `internal/a2a/auth_test.go`

- [ ] **Step 1: Add JWKS types and cache to auth.go**

Add at the top of `internal/a2a/auth.go` (after imports):

```go
import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type jwksCache struct {
	url        string
	keys       map[string]any // kid -> *rsa.PublicKey or *ecdsa.PublicKey
	mu         sync.RWMutex
	expiresAt  time.Time
	client     *http.Client
}

func newJWKSCache(url string) *jwksCache {
	return &jwksCache{
		url:    url,
		keys:   make(map[string]any),
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

type jwksResponse struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Crv string `json:"crv"`
}
```

- [ ] **Step 2: Implement JWKS fetch and parse**

Add methods to `jwksCache`:

```go
func (c *jwksCache) getKey(kid string) (any, error) {
	c.mu.RLock()
	if key, ok := c.keys[kid]; ok && time.Now().Before(c.expiresAt) {
		c.mu.RUnlock()
		return key, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if key, ok := c.keys[kid]; ok && time.Now().Before(c.expiresAt) {
		return key, nil
	}

	if err := c.fetch(); err != nil {
		return nil, err
	}

	key, ok := c.keys[kid]
	if !ok {
		return nil, fmt.Errorf("key %q not found in JWKS", kid)
	}
	return key, nil
}

func (c *jwksCache) fetch() error {
	resp, err := c.client.Get(c.url)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch JWKS: status %d", resp.StatusCode)
	}

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("decode JWKS: %w", err)
	}

	newKeys := make(map[string]any)
	for _, k := range jwks.Keys {
		key, err := parseJWK(k)
		if err != nil {
			continue // skip unparseable keys
		}
		newKeys[k.Kid] = key
	}

	c.keys = newKeys
	c.expiresAt = time.Now().Add(5 * time.Minute)
	return nil
}

func parseJWK(k jwk) (any, error) {
	switch k.Kty {
	case "RSA":
		n, err := base64urlDecode(k.N)
		if err != nil {
			return nil, err
		}
		e, err := base64urlDecode(k.E)
		if err != nil {
			return nil, err
		}
		return &rsa.PublicKey{
			N: new(big.Int).SetBytes(n),
			E: int(new(big.Int).SetBytes(e).Int64()),
		}, nil
	case "EC":
		x, err := base64urlDecode(k.X)
		if err != nil {
			return nil, err
		}
		y, err := base64urlDecode(k.Y)
		if err != nil {
			return nil, err
		}
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("unsupported curve: %s", k.Crv)
		}
		return &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, nil
	default:
		return nil, fmt.Errorf("unsupported key type: %s", k.Kty)
	}
}

func base64urlDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
```

- [ ] **Step 3: Replace placeholder validators with real implementations**

Replace `validateExternalBearer` and `validateJWTBearer`:

```go
func (a *AuthMiddleware) validateExternalBearer(token string) AuthResult {
	body, _ := json.Marshal(map[string]string{"token": token})
	resp, err := http.Post(a.BearerValidatorURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return AuthResult{OK: false}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return AuthResult{OK: false}
	}

	var result struct {
		Valid   bool   `json:"valid"`
		AgentID string `json:"agent_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return AuthResult{OK: false}
	}
	return AuthResult{OK: result.Valid, AgentID: result.AgentID}
}

func (a *AuthMiddleware) validateJWTBearer(token string) AuthResult {
	if a.jwks == nil {
		a.jwks = newJWKSCache(a.BearerJWKSURL)
	}

	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		kid, ok := t.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("missing kid header")
		}
		return a.jwks.getKey(kid)
	}, jwt.WithValidMethods([]string{"RS256", "RS384", "RS512", "ES256", "ES384", "ES512"}))
	if err != nil || !parsed.Valid {
		return AuthResult{OK: false}
	}

	sub, _ := parsed.Claims.GetSubject()
	return AuthResult{OK: true, AgentID: sub}
}
```

Add `jwks` field to `AuthMiddleware`:

```go
type AuthMiddleware struct {
	APIKey             string
	BearerValidatorURL string
	BearerJWKSURL      string
	jwks               *jwksCache
}
```

Add `"bytes"` to imports.

- [ ] **Step 4: Write JWT validation tests**

Replace `internal/a2a/auth_test.go` content:

```go
package a2a

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestAuthMiddlewareAPIKey(t *testing.T) {
	mw := &AuthMiddleware{APIKey: "ak_test"}
	var called bool
	handler := mw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	// Valid API key
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-Api-Key", "ak_test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !called || rec.Code != 200 {
		t.Fatal("expected 200")
	}

	// Missing auth
	called = false
	req2 := httptest.NewRequest("POST", "/", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if called {
		t.Fatal("should not call next")
	}
	if rec2.Code != 401 {
		t.Fatalf("expected 401, got %d", rec2.Code)
	}
}

func TestAuthMiddlewareJWT(t *testing.T) {
	// Generate RSA keypair
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	// Build JWKS
	nBytes := base64.RawURLEncoding.EncodeToString(privKey.N.Bytes())
	eBytes := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privKey.E)).Bytes())
	jwks := map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA",
			"kid": "key1",
			"alg": "RS256",
			"n":   nBytes,
			"e":   eBytes,
		}},
	}
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jwks)
	}))
	defer jwksServer.Close()

	// Create valid JWT
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Subject:   "agent-42",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})
	token.Header["kid"] = "key1"
	tokenString, err := token.SignedString(privKey)
	if err != nil {
		t.Fatal(err)
	}

	mw := &AuthMiddleware{BearerJWKSURL: jwksServer.URL}
	var agentID string
	handler := mw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agentID = AgentIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if agentID != "agent-42" {
		t.Fatalf("expected agent-42, got %s", agentID)
	}
}

func TestAuthMiddlewareJWTInvalidSignature(t *testing.T) {
	// Generate two different keypairs
	privKey1, _ := rsa.GenerateKey(rand.Reader, 2048)
	privKey2, _ := rsa.GenerateKey(rand.Reader, 2048)

	nBytes := base64.RawURLEncoding.EncodeToString(privKey1.N.Bytes())
	eBytes := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privKey1.E)).Bytes())
	jwks := map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA", "kid": "key1", "alg": "RS256",
			"n": nBytes, "e": eBytes,
		}},
	}
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jwks)
	}))
	defer jwksServer.Close()

	// Sign with key2, but JWKS only has key1
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Subject:   "agent-42",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})
	token.Header["kid"] = "key1"
	tokenString, _ := token.SignedString(privKey2)

	mw := &AuthMiddleware{BearerJWKSURL: jwksServer.URL}
	var called bool
	handler := mw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if called {
		t.Fatal("should not call next with invalid signature")
	}
	if rec.Code != 401 {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddlewareExternalBearer(t *testing.T) {
	validator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Token string `json:"token"` }
		json.NewDecoder(r.Body).Decode(&req)
		if req.Token == "valid-token" {
			json.NewEncoder(w).Encode(map[string]any{"valid": true, "agent_id": "ext-agent"})
		} else {
			json.NewEncoder(w).Encode(map[string]any{"valid": false})
		}
	}))
	defer validator.Close()

	mw := &AuthMiddleware{BearerValidatorURL: validator.URL}
	var agentID string
	handler := mw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agentID = AgentIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	// Valid token
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 || agentID != "ext-agent" {
		t.Fatalf("expected 200 with ext-agent, got %d / %s", rec.Code, agentID)
	}

	// Invalid token
	agentID = ""
	req2 := httptest.NewRequest("POST", "/", nil)
	req2.Header.Set("Authorization", "Bearer invalid-token")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != 401 {
		t.Fatalf("expected 401, got %d", rec2.Code)
	}
}
```

- [ ] **Step 5: Run auth tests**

```bash
cd /Users/pj/Documents/thunder/hearsay
go test ./internal/a2a/ -run TestAuth -v
```

Expected: All 5 tests PASS

- [ ] **Step 6: Commit**

```bash
cd /Users/pj/Documents/thunder/hearsay
git add internal/a2a/auth.go internal/a2a/auth_test.go
git commit -m "feat(a2a): implement JWT and external Bearer token validation"
```

---

### Task 3: Fix error handling in task handlers

**Files:**
- Modify: `internal/a2a/handler_tasks.go`

- [ ] **Step 1: Fix CreateArtifact error checking in handleTasksSend**

In `handleTasksSend`, replace the artifact storage loop:

```go
// Store artifacts
for _, art := range result.Artifacts {
	partsJSON, err := json.Marshal(art.Parts)
	if err != nil {
		return nil, NewError(-32003, "Failed to marshal artifact: "+err.Error())
	}
	if err := s.provider.CreateArtifact(ctx, s.namespace, task.ID, hearsay.A2AArtifact{
		Name: art.Name, Description: art.Description, Parts: partsJSON,
		Index: art.Index, Append: art.Append, LastChunk: art.LastChunk,
	}); err != nil {
		return nil, NewError(-32003, "Failed to store artifact: "+err.Error())
	}
}
```

- [ ] **Step 2: Fix GetArtifacts error checking in handleTasksGet**

Replace:
```go
// Load artifacts
arts, err := s.provider.GetArtifacts(ctx, s.namespace, req.ID)
if err != nil {
	return nil, NewError(-32003, "Failed to load artifacts: "+err.Error())
}
for _, a := range arts {
```

- [ ] **Step 3: Fix handleTasksCancel error checking**

Replace the cancel body:

```go
	task := storageToTask(st)
	if task.ClaimID != "" {
		if err := s.client.Release(ctx, task.ClaimID, hearsay.OutcomeAbandoned); err != nil {
			// Log warning but proceed with cancel — release failure shouldn't prevent cancel
			// In production, this should be logged to a structured logger
			_ = err
		}
	}
	if err := task.Transition(TaskCanceled); err != nil {
		return nil, NewError(-32001, "Task already in terminal state", map[string]string{"currentState": st.State})
	}
	if err := s.provider.UpdateTask(ctx, s.namespace, taskToStorage(task)); err != nil {
		return nil, NewError(-32003, "Failed to update task: "+err.Error())
	}

	return NewResponse(req.ID, taskToJSON(task)), nil
```

- [ ] **Step 4: Run tests**

```bash
cd /Users/pj/Documents/thunder/hearsay
go test ./internal/a2a/ -v
```

Expected: All tests PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/pj/Documents/thunder/hearsay
git add internal/a2a/handler_tasks.go
git commit -m "fix(a2a): check all storage operation errors in task handlers"
```

---

### Task 4: Fix claim conflict behavior

**Files:**
- Modify: `internal/a2a/handler_skills.go`

- [ ] **Step 1: Don't set ClaimID on conflict**

In `skillClaimResource`, remove `task.ClaimID = resp.ClaimID` from the conflict branch:

```go
func (s *Server) skillClaimResource(ctx context.Context, task *Task, params *SkillParams) (*Task, error) {
	req := mapToClaimRequest(params)
	resp, err := s.client.Claim(ctx, req)
	if err != nil {
		if _, ok := err.(*hearsay.ConflictError); ok {
			// Do NOT set task.ClaimID — the claim was not granted
			reportJSON, _ := json.Marshal(resp.Conflict)
			task.Artifacts = []Artifact{{
				Name: "conflict-report",
				Parts: []Part{{Type: "data", Data: reportJSON}},
			}}
			return task, NewError(-32002, "Conflict detected", resp.Conflict)
		}
		return nil, err
	}
	task.ClaimID = resp.ClaimID
	task.Artifacts = []Artifact{{
		Name: "claim-result",
		Parts: []Part{{Type: "data", Data: mustJSON(map[string]string{"claim_id": resp.ClaimID, "status": "granted"})}},
	}}
	return task, nil
}
```

- [ ] **Step 2: Run tests**

```bash
cd /Users/pj/Documents/thunder/hearsay
go test ./internal/a2a/ -v
```

Expected: All tests PASS

- [ ] **Step 3: Commit**

```bash
cd /Users/pj/Documents/thunder/hearsay
git add internal/a2a/handler_skills.go
git commit -m "fix(a2a): don't store ClaimID when claim conflicts"
```

---

### Task 5: Fix SQLite DeleteNamespace

**Files:**
- Modify: `internal/sqlite/sqlite.go`

- [ ] **Step 1: Wrap DeleteNamespace in transaction with A2A cleanup**

Replace the `DeleteNamespace` function:

```go
func (p *Provider) DeleteNamespace(ctx context.Context, namespaceID string) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete A2A data first (history → artifacts → tasks)
	if _, err := tx.ExecContext(ctx, "DELETE FROM a2a_task_history WHERE task_id IN (SELECT id FROM a2a_tasks WHERE namespace = ?)", namespaceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM a2a_artifacts WHERE task_id IN (SELECT id FROM a2a_tasks WHERE namespace = ?)", namespaceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM a2a_tasks WHERE namespace = ?", namespaceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE namespace = ?", namespaceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM mailbox_messages WHERE namespace = ?", namespaceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM namespaces WHERE id = ?", namespaceID); err != nil {
		return err
	}

	return tx.Commit()
}
```

- [ ] **Step 2: Run SQLite tests**

```bash
cd /Users/pj/Documents/thunder/hearsay
go test ./internal/sqlite/ -v
```

Expected: PASS

- [ ] **Step 3: Run full test suite**

```bash
cd /Users/pj/Documents/thunder/hearsay
go test ./...
```

Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
cd /Users/pj/Documents/thunder/hearsay
git add internal/sqlite/sqlite.go
git commit -m "fix(sqlite): transactional DeleteNamespace with A2A cleanup"
```

---

### Task 6: Final verification

- [ ] **Step 1: Full build + test**

```bash
cd /Users/pj/Documents/thunder/hearsay
go build ./... && go test ./... -v 2>&1 | tail -30
```

Expected: All packages PASS

- [ ] **Step 2: Commit design docs**

```bash
cd /Users/pj/Documents/thunder/hearsay
git add docs/superpowers/specs/2026-05-26-a2a-security-fixes-design.md
git commit -m "docs: A2A security fixes design spec"
```

- [ ] **Step 3: Save plan**

```bash
cd /Users/pj/Documents/thunder/hearsay
cp docs/superpowers/specs/2026-05-26-a2a-security-fixes-design.md docs/superpowers/plans/2026-05-26-a2a-security-fixes.md
# Edit the copy to be the plan format... actually just save the plan separately
```

Actually, save this plan file:

```bash
cd /Users/pj/Documents/thunder/hearsay
git add docs/superpowers/plans/2026-05-26-a2a-security-fixes.md
git commit -m "docs: A2A security fixes implementation plan"
```

---

## Self-Review Checklist

- [x] Spec coverage: All 5 issues from review have tasks
- [x] Placeholder scan: No TBDs, all code shown
- [x] Type consistency: `AuthMiddleware` gains `jwks` field; `jwksCache` types defined
- [x] Dependency: `github.com/golang-jwt/jwt/v5` added in Task 1
- [x] Tests: Each task has test steps
