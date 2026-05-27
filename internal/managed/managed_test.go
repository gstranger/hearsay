package managed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gstranger/hearsay/internal"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

// mockManagedServer implements the hosted-hearsay HTTP API that the
// managed provider expects.  It stores state in-memory.
type mockManagedServer struct {
	mu         sync.Mutex
	namespaces map[string]struct{}
	messages   map[string][]hearsay.Message // ns -> msgs
	tasks      map[string]map[string]*hearsay.A2ATask // ns -> taskID -> task
}

func newMockManagedServer() *mockManagedServer {
	return &mockManagedServer{
		namespaces: make(map[string]struct{}),
		messages:   make(map[string][]hearsay.Message),
		tasks:      make(map[string]map[string]*hearsay.A2ATask),
	}
}

func (m *mockManagedServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /namespaces/create", func(w http.ResponseWriter, r *http.Request) {
		var ns hearsay.Namespace
		json.NewDecoder(r.Body).Decode(&ns)
		m.mu.Lock()
		m.namespaces[ns.ID] = struct{}{}
		m.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	// Namespace delete clears tasks too
	mux.HandleFunc("POST /namespaces/delete", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ ID string `json:"id"` }
		json.NewDecoder(r.Body).Decode(&req)
		m.mu.Lock()
		delete(m.namespaces, req.ID)
		delete(m.messages, req.ID)
		delete(m.tasks, req.ID)
		m.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /append", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Namespace string               `json:"namespace"`
			Messages  []hearsay.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		m.mu.Lock()
		for i := range req.Messages {
			req.Messages[i].Offset = int64(len(m.messages[req.Namespace]) + i + 1)
			req.Messages[i].Namespace = req.Namespace
			req.Messages[i].Timestamp = time.Now()
		}
		m.messages[req.Namespace] = append(m.messages[req.Namespace], req.Messages...)
		m.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /query", func(w http.ResponseWriter, r *http.Request) {
		ns := r.URL.Query().Get("namespace")
		since := 0
		fmt.Sscanf(r.URL.Query().Get("since"), "%d", &since)
		agentID := r.URL.Query().Get("agent")
		limit := 0
		fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit)

		m.mu.Lock()
		msgs := m.messages[ns]
		m.mu.Unlock()

		var result []hearsay.Message
		for _, msg := range msgs {
			if int(msg.Offset) <= since {
				continue
			}
			if agentID != "" && msg.AgentID != agentID {
				continue
			}
			result = append(result, msg)
			if limit > 0 && len(result) >= limit {
				break
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("GET /claims", func(w http.ResponseWriter, r *http.Request) {
		ns := r.URL.Query().Get("namespace")
		m.mu.Lock()
		msgs := m.messages[ns]
		m.mu.Unlock()

		var active []hearsay.Claim
		for _, c := range hearsay.FilterActiveClaims(msgs) {
			active = append(active, c)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(active)
	})
	mux.HandleFunc("GET /agents", func(w http.ResponseWriter, r *http.Request) {
		ns := r.URL.Query().Get("namespace")
		agentID := r.URL.Query().Get("agent")
		m.mu.Lock()
		msgs := m.messages[ns]
		m.mu.Unlock()

		var active []string
		var lastSeen time.Time
		for _, m := range msgs {
			if m.AgentID == agentID && m.Timestamp.After(lastSeen) {
				lastSeen = m.Timestamp
			}
			if m.Type == hearsay.MsgClaim && m.AgentID == agentID {
				var c hearsay.Claim
				json.Unmarshal(m.Payload, &c)
				active = append(active, c.ClaimID)
			}
		}
		state := hearsay.AgentState{AgentID: agentID, ActiveClaims: active, LastSeen: lastSeen}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(state)
	})
	mux.HandleFunc("POST /expire", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// A2A task storage handlers
	mux.HandleFunc("POST /tasks/create", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Namespace string                `json:"namespace"`
			Task      *hearsay.A2ATask   `json:"task"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		m.mu.Lock()
		if m.tasks[req.Namespace] == nil {
			m.tasks[req.Namespace] = make(map[string]*hearsay.A2ATask)
		}
		m.tasks[req.Namespace][req.Task.ID] = req.Task
		m.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /tasks/get", func(w http.ResponseWriter, r *http.Request) {
		ns := r.URL.Query().Get("namespace")
		id := r.URL.Query().Get("id")
		m.mu.Lock()
		task := m.tasks[ns][id]
		m.mu.Unlock()
		if task == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(task)
	})
	mux.HandleFunc("POST /tasks/update", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Namespace string                `json:"namespace"`
			Task      *hearsay.A2ATask   `json:"task"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		m.mu.Lock()
		if m.tasks[req.Namespace] == nil {
			m.tasks[req.Namespace] = make(map[string]*hearsay.A2ATask)
		}
		m.tasks[req.Namespace][req.Task.ID] = req.Task
		m.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /tasks/history", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /tasks/history", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]hearsay.A2AMessage{})
	})
	mux.HandleFunc("POST /tasks/artifact", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /tasks/artifacts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]hearsay.A2AArtifact{})
	})
	return mux
}

func TestManagedProviderContract(t *testing.T) {
	mock := newMockManagedServer()
	ts := httptest.NewServer(mock.handler())
	defer ts.Close()

	p := New(ts.URL, "")

	internal.RunProviderContractTests(t, "managed", func() (hearsay.Provider, error) {
		return p, nil
	})
}

func TestManagedProviderWithToken(t *testing.T) {
	var receivedToken string
	mock := newMockManagedServer()
	underlying := mock.handler()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "" {
			receivedToken = auth
		}
		underlying.ServeHTTP(w, r)
	}))
	defer ts.Close()

	p := New(ts.URL, "sekret-token")
	ctx := context.Background()
	_ = p.DeleteNamespace(ctx, "token-test")
	if err := p.CreateNamespace(ctx, hearsay.Namespace{ID: "token-test", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	if receivedToken != "Bearer sekret-token" {
		t.Fatalf("expected Bearer sekret-token, got %s", receivedToken)
	}
}
