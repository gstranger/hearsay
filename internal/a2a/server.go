package a2a

import (
	"encoding/json"
	"net/http"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

type Server struct {
	cfg       *hearsay.A2AConfig
	client    *hearsay.Client
	provider  hearsay.Provider
	auth      *AuthMiddleware
	namespace string
}

func NewServer(cfg *hearsay.A2AConfig, client *hearsay.Client, provider hearsay.Provider, auth *AuthMiddleware, namespace string) *Server {
	return &Server{cfg: cfg, client: client, provider: provider, auth: auth, namespace: namespace}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Agent Card
	if r.Method == http.MethodGet && r.URL.Path == "/.well-known/agent.json" {
		card := GenerateAgentCard(s.cfg, "1.0.0")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(card)
		return
	}

	// JSON-RPC endpoint
	if r.Method == http.MethodPost && r.URL.Path == "/" {
		s.handleJSONRPC(w, r)
		return
	}

	w.WriteHeader(http.StatusNotFound)
}

func (s *Server) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, nil, -32600, "Invalid Request")
		return
	}

	// Streaming methods handle their own response lifecycle (SSE).
	// WASM/Worker runtimes (cfg == nil) cannot stream — no http.Flusher
	// support — so we reject with A2A UnsupportedOperationError (-32004),
	// matching the streaming:false advertised in the agent card.
	if req.Method == "tasks/sendSubscribe" {
		if s.cfg == nil {
			s.writeError(w, req.ID, -32004, "Streaming not supported in this runtime; use tasks/send")
			return
		}
		s.handleTasksSendSubscribe(w, r, req)
		return
	}

	var resp *JSONRPCResponse
	var handlerErr error

	switch req.Method {
	case "tasks/send":
		resp, handlerErr = s.handleTasksSend(r.Context(), req.Params)
	case "tasks/get":
		resp, handlerErr = s.handleTasksGet(r.Context(), req.Params)
	case "tasks/cancel":
		resp, handlerErr = s.handleTasksCancel(r.Context(), req.Params)
	default:
		s.writeError(w, req.ID, -32601, "Method not found")
		return
	}

	if handlerErr != nil {
		if rpcErr, ok := handlerErr.(*JSONRPCError); ok {
			s.writeError(w, req.ID, rpcErr.Code, rpcErr.Message, rpcErr.Data)
		} else {
			s.writeError(w, req.ID, -32003, handlerErr.Error())
		}
		return
	}

	resp.ID = req.ID
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) writeError(w http.ResponseWriter, id any, code int, message string, data ...any) {
	resp := &JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: NewError(code, message)}
	if len(data) > 0 {
		resp.Error.Data = data[0]
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // JSON-RPC errors return 200 with error in body
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) Addr() string {
	if s.cfg != nil {
		return s.cfg.Addr
	}
	return ""
}
