package server

import (
	"encoding/json"
	"net/http"

	"github.com/gstranger/hearsay/internal/mailbox"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

type sendMessageRequest struct {
	Namespace      string `json:"namespace"`
	From           string `json:"from"`
	To             string `json:"to"`
	Type           string `json:"type"`
	Content        string `json:"content"`
	RelatedClaimID string `json:"related_claim_id,omitempty"`
	TTLSeconds     int    `json:"ttl_seconds"`
}

type messageActionRequest struct {
	Namespace string `json:"namespace"`
	MessageID string `json:"message_id"`
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req sendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	svc := mailbox.New(s.provider)
	id, err := svc.Send(r.Context(), req.Namespace, req.From, req.To, req.Type, req.Content, req.RelatedClaimID, req.TTLSeconds)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"message_id": id})
}

func (s *Server) handleGetMailbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	namespace := r.URL.Query().Get("namespace")
	agentID := r.URL.Query().Get("agent_id")
	unread := r.URL.Query().Get("unread") == "true"
	includeClaims := r.URL.Query().Get("include_claims") == "true"

	if namespace == "" || agentID == "" {
		http.Error(w, "namespace and agent_id required", http.StatusBadRequest)
		return
	}

	svc := mailbox.New(s.provider)
	msgs, err := svc.GetMailbox(r.Context(), namespace, agentID, hearsay.MailboxQueryOpts{
		Unread:        unread,
		IncludeClaims: includeClaims,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(msgs)
}

func (s *Server) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req messageActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	svc := mailbox.New(s.provider)
	if err := svc.MarkRead(r.Context(), req.Namespace, req.MessageID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) handleArchiveMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req messageActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	svc := mailbox.New(s.provider)
	if err := svc.Archive(r.Context(), req.Namespace, req.MessageID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
