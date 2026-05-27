package a2a

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
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
