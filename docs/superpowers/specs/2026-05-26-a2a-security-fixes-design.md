# A2A Adapter Security & Robustness Fixes — Design Spec

> Date: 2026-05-26  
> Scope: Fix critical issues identified in final code review  
> Branch: `feat/a2a-adapter`

---

## 1. Problem Statement

The final code review identified three **critical** issues that must be fixed before the A2A adapter is production-ready:

1. **Unimplemented Bearer/JWT validators** (`auth.go`) — placeholder functions accept ANY token, creating a security vulnerability.
2. **Silent error ignoring** (`handler_tasks.go`) — `CreateArtifact`, `AppendTaskHistory`, `GetArtifacts`, and cancel operations ignore errors, risking data loss and inconsistent state.
3. **Orphaned A2A data on namespace deletion** (`sqlite.go`) — `DeleteNamespace` does not clean `a2a_tasks`, `a2a_task_history`, or `a2a_artifacts`.

Additionally, two **important** issues will be fixed:
4. **ClaimID stored on conflict** (`handler_skills.go`) — `skillClaimResource` stores `ClaimID` even when the claim was not granted due to conflict.
5. **SQLite `DeleteNamespace` not transactional** — multiple separate `Exec` calls risk partial deletion.

---

## 2. Design

### 2.1 JWT Bearer Validation

**Library:** `github.com/golang-jwt/jwt/v5` (new dependency)

**Approach:**
- Fetch JWKS JSON from `BearerJWKSURL` via HTTP GET with timeout
- Parse JWKs into `jwt.Keyfunc` that looks up by `kid` header claim
- Support RSA (`RS256`, `RS384`, `RS512`) and ECDSA (`ES256`, `ES384`, `ES512`) keys
- Cache JWKS in memory with a TTL (5 minutes) to avoid fetching on every request
- Validate: signature, `exp`, `nbf`, `iat`
- Extract `sub` claim as `AgentID`
- Return `AuthResult{OK: false}` on any validation failure

**JWKS parsing:** Implemented manually (no additional dependency) since JWKS is just JSON with base64url-encoded key parameters. For RSA: decode `n` and `e` to build `*rsa.PublicKey`. For ECDSA: decode `x` and `y` to build `*ecdsa.PublicKey`.

**External Bearer Validation:**
- HTTP POST to `BearerValidatorURL` with JSON body `{"token": "..."}`
- Expect 200 response with JSON `{"valid": true, "agent_id": "..."}`
- Timeout: 5 seconds
- Return `AuthResult{OK: false}` on non-200 or `valid: false`

### 2.2 Error Handling in Task Handlers

**Principle:** Every storage operation error must be checked and propagated.

Changes:
- `handleTasksSend`: Check `CreateArtifact` error, return `-32003` if any artifact fails to store
- `handleTasksSend`: `AppendTaskHistory` error is already checked ✓
- `handleTasksGet`: Check `GetArtifacts` error, return `-32003` if retrieval fails
- `handleTasksCancel`: Check `Release` error — log warning but still proceed with cancel (release failure shouldn't prevent cancel)
- `handleTasksCancel`: Check `Transition` error — if already terminal, return `-32001`; otherwise return `-32003`
- `handleTasksCancel`: Check `UpdateTask` error — return `-32003`

### 2.3 SQLite Namespace Deletion

Changes:
- Add `DELETE FROM a2a_task_history WHERE task_id IN (SELECT id FROM a2a_tasks WHERE namespace = ?)`
- Add `DELETE FROM a2a_artifacts WHERE task_id IN (SELECT id FROM a2a_tasks WHERE namespace = ?)`
- Add `DELETE FROM a2a_tasks WHERE namespace = ?`
- Wrap entire `DeleteNamespace` in a transaction (BEGIN/COMMIT/ROLLBACK)

### 2.4 Claim Conflict Fix

Changes:
- In `skillClaimResource`, do NOT set `task.ClaimID` when a conflict occurs
- The conflict report artifact is still stored, but `ClaimID` remains empty

---

## 3. Files Changed

| File | Change |
|---|---|
| `internal/a2a/auth.go` | Implement `validateExternalBearer`, `validateJWTBearer`, add JWKS cache |
| `internal/a2a/auth_test.go` | Add tests for JWT validation, external validation, error paths |
| `internal/a2a/handler_tasks.go` | Check all storage operation errors |
| `internal/a2a/handler_skills.go` | Don't set ClaimID on conflict |
| `internal/sqlite/sqlite.go` | Add A2A cleanup + transaction in DeleteNamespace |
| `go.mod` / `go.sum` | Add `github.com/golang-jwt/jwt/v5` |

---

## 4. Testing Strategy

1. **Unit tests:** JWT validation with a generated RSA keypair and JWKS
2. **Unit tests:** External bearer validation with `httptest` mock server
3. **Unit tests:** Auth failure paths (expired token, invalid signature, missing kid)
4. **Integration tests:** Error paths in task handlers (artifact storage failure simulation)
5. **Contract tests:** SQLite DeleteNamespace verifies A2A data is cleaned up
6. **Full suite:** `go test ./...` must pass

---

## 5. Security Considerations

- JWKS URL must use HTTPS in production (enforced by documentation, not code)
- JWT tokens must have `exp` claim (enforced by validation)
- External validator URL must use HTTPS (enforced by documentation)
- API keys remain plaintext in config (documented limitation, not changed in this fix)

---

## 6. Rollback Plan

If JWT library causes issues:
1. Revert `go.mod`/`go.sum` changes
2. Revert `auth.go` to placeholder returning errors
3. All other fixes (error handling, SQLite cleanup) remain

---

*Approved for implementation.*
