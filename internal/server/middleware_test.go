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
