package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

type Server struct {
	client   *hearsay.Client
	provider hearsay.Provider
	locking  bool
	mux      *http.ServeMux
}

func New(client *hearsay.Client, provider hearsay.Provider, locking bool) *Server {
	s := &Server{client: client, provider: provider, locking: locking, mux: http.NewServeMux()}
	// Health check
	s.mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	// Client-facing endpoints
	s.mux.HandleFunc("POST /claim", s.handleClaim)
	s.mux.HandleFunc("POST /release", s.handleRelease)
	s.mux.HandleFunc("POST /heartbeat", s.handleHeartbeat)
	s.mux.HandleFunc("GET /claims", s.handleQueryClaims)
	s.mux.HandleFunc("GET /check", s.handleCheck)
	s.mux.HandleFunc("POST /intent", s.handleIntent)
	// Mailbox endpoints
	s.mux.HandleFunc("POST /message", s.handleSendMessage)
	s.mux.HandleFunc("GET /mailbox", s.handleGetMailbox)
	s.mux.HandleFunc("POST /message/read", s.handleMarkRead)
	s.mux.HandleFunc("POST /message/archive", s.handleArchiveMessage)
	// Provider proxy endpoints (for managed provider)
	s.mux.HandleFunc("POST /namespaces/create", s.handleCreateNamespace)
	s.mux.HandleFunc("POST /namespaces/delete", s.handleDeleteNamespace)
	s.mux.HandleFunc("POST /append", s.handleAppend)
	s.mux.HandleFunc("GET /query", s.handleQuery)
	s.mux.HandleFunc("GET /agents", s.handleAgentState)
	s.mux.HandleFunc("POST /expire", s.handleReleaseExpired)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	var req hearsay.ClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp, err := s.client.Claim(r.Context(), req)
	if err != nil {
		if _, ok := err.(*hearsay.ConflictError); ok {
			if s.locking && resp.Conflict != nil && len(resp.Conflict.Conflicts) > 0 {
				c := resp.Conflict.Conflicts[0]
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusLocked)
				json.NewEncoder(w).Encode(map[string]any{
					"error":       "resource_locked",
					"message":     fmt.Sprintf("Resource locked by %s (%s)", c.AgentID, c.Intent),
					"locked_by":   c.AgentID,
					"intent":      c.Intent,
					"resource":    c.Resource,
					"claim_id":    resp.ClaimID,
					"conflicts":   resp.Conflict,
				})
				return
			}
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(resp)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClaimID string           `json:"claim_id"`
		Outcome hearsay.Outcome `json:"outcome"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.client.Release(r.Context(), req.ClaimID, req.Outcome); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClaimID string `json:"claim_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.client.Heartbeat(r.Context(), req.ClaimID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleQueryClaims(w http.ResponseWriter, r *http.Request) {
	pattern := r.URL.Query().Get("resource")
	claims, err := s.client.ActiveClaims(r.Context(), pattern)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(claims)
}

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	resource := r.URL.Query().Get("resource")
	op := hearsay.Operation(r.URL.Query().Get("operation"))
	report, err := s.client.CheckConflict(r.Context(), resource, op)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(report)
}

func (s *Server) handleIntent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClaimID string `json:"claim_id"`
		Intent  string `json:"intent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.client.UpdateIntent(r.Context(), req.ClaimID, req.Intent); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Provider proxy handlers

func (s *Server) handleCreateNamespace(w http.ResponseWriter, r *http.Request) {
	var ns hearsay.Namespace
	if err := json.NewDecoder(r.Body).Decode(&ns); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.provider.CreateNamespace(r.Context(), ns); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDeleteNamespace(w http.ResponseWriter, r *http.Request) {
	var req struct{ ID string `json:"id"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.provider.DeleteNamespace(r.Context(), req.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleAppend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Namespace string              `json:"namespace"`
		Messages  []hearsay.Message `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.provider.Append(r.Context(), req.Namespace, req.Messages); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	opts := hearsay.QueryOpts{}
	sinceStr := r.URL.Query().Get("since")
	if sinceStr != "" {
		if v, err := strconv.ParseInt(sinceStr, 10, 64); err == nil {
			opts.Since = hearsay.Offset(v)
		}
	}
	opts.AgentID = r.URL.Query().Get("agent")
	limitStr := r.URL.Query().Get("limit")
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil {
			opts.Limit = v
		}
	}

	msgs, err := s.provider.Query(r.Context(), namespace, opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(msgs)
}

func (s *Server) handleAgentState(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	agentID := r.URL.Query().Get("agent")
	state, err := s.provider.AgentState(r.Context(), namespace, agentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(state)
}

// TODO: Add background mailbox expiration goroutine that sweeps all namespaces periodically.
// For v0, expired messages are filtered at query time.

func (s *Server) handleReleaseExpired(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Namespace string    `json:"namespace"`
		Before    time.Time `json:"before"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.provider.ReleaseExpired(r.Context(), req.Namespace, req.Before); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
