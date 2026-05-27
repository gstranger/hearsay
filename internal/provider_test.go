package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/thunder/agentstate/internal/managed"
	"github.com/thunder/agentstate/internal/memory"
	"github.com/thunder/agentstate/internal/postgres"
	"github.com/thunder/agentstate/internal/sqlite"
	"github.com/thunder/agentstate/pkg/agentstate"
)

func TestManagedProvider(t *testing.T) {
	// Inline minimal mock server for contract testing
	var mu sync.Mutex
	namespaces := make(map[string]struct{})
	messages := make(map[string][]agentstate.Message)
	tasks := make(map[string]map[string]*agentstate.A2ATask) // ns -> taskID -> task

	mux := http.NewServeMux()
	mux.HandleFunc("POST /namespaces/create", func(w http.ResponseWriter, r *http.Request) {
		var ns agentstate.Namespace
		json.NewDecoder(r.Body).Decode(&ns)
		mu.Lock()
		namespaces[ns.ID] = struct{}{}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /namespaces/delete", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ ID string `json:"id"` }
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		delete(namespaces, req.ID)
		delete(messages, req.ID)
		delete(tasks, req.ID)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /append", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Namespace string               `json:"namespace"`
			Messages  []agentstate.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		for i := range req.Messages {
			req.Messages[i].Offset = int64(len(messages[req.Namespace]) + i + 1)
			req.Messages[i].Namespace = req.Namespace
			req.Messages[i].Timestamp = time.Now()
		}
		messages[req.Namespace] = append(messages[req.Namespace], req.Messages...)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /query", func(w http.ResponseWriter, r *http.Request) {
		ns := r.URL.Query().Get("namespace")
		since := 0
		fmt.Sscanf(r.URL.Query().Get("since"), "%d", &since)
		mu.Lock()
		msgs := messages[ns]
		mu.Unlock()
		var result []agentstate.Message
		for _, m := range msgs {
			if int(m.Offset) <= since {
				continue
			}
			result = append(result, m)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("GET /claims", func(w http.ResponseWriter, r *http.Request) {
		ns := r.URL.Query().Get("namespace")
		mu.Lock()
		msgs := messages[ns]
		mu.Unlock()
		var active []agentstate.Claim
		for _, c := range agentstate.FilterActiveClaims(msgs) {
			active = append(active, c)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(active)
	})
	mux.HandleFunc("GET /agents", func(w http.ResponseWriter, r *http.Request) {
		ns := r.URL.Query().Get("namespace")
		agentID := r.URL.Query().Get("agent")
		mu.Lock()
		msgs := messages[ns]
		mu.Unlock()
		var active []string
		var lastSeen time.Time
		for _, m := range msgs {
			if m.AgentID == agentID && m.Timestamp.After(lastSeen) {
				lastSeen = m.Timestamp
			}
			if m.Type == agentstate.MsgClaim && m.AgentID == agentID {
				var c agentstate.Claim
				json.Unmarshal(m.Payload, &c)
				active = append(active, c.ClaimID)
			}
		}
		state := agentstate.AgentState{AgentID: agentID, ActiveClaims: active, LastSeen: lastSeen}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(state)
	})
	mux.HandleFunc("POST /expire", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// A2A task storage endpoints for managed provider contract tests
	mux.HandleFunc("POST /tasks/create", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Namespace string              `json:"namespace"`
			Task      *agentstate.A2ATask `json:"task"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		if tasks[req.Namespace] == nil {
			tasks[req.Namespace] = make(map[string]*agentstate.A2ATask)
		}
		tasks[req.Namespace][req.Task.ID] = req.Task
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /tasks/get", func(w http.ResponseWriter, r *http.Request) {
		ns := r.URL.Query().Get("namespace")
		id := r.URL.Query().Get("id")
		mu.Lock()
		task := tasks[ns][id]
		mu.Unlock()
		if task == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(task)
	})
	mux.HandleFunc("POST /tasks/update", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Namespace string              `json:"namespace"`
			Task      *agentstate.A2ATask `json:"task"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		if tasks[req.Namespace] == nil {
			tasks[req.Namespace] = make(map[string]*agentstate.A2ATask)
		}
		tasks[req.Namespace][req.Task.ID] = req.Task
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /tasks/history", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /tasks/history", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]agentstate.A2AMessage{})
	})
	mux.HandleFunc("POST /tasks/artifact", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /tasks/artifacts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]agentstate.A2AArtifact{})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	p := managed.New(ts.URL, "")
	RunProviderContractTests(t, "managed", func() (agentstate.Provider, error) {
		return p, nil
	})
}

func TestMemoryProvider(t *testing.T) {
	RunProviderContractTests(t, "memory", func() (agentstate.Provider, error) {
		return memory.New(), nil
	})
}

func TestSQLiteProvider(t *testing.T) {
	RunProviderContractTests(t, "sqlite", func() (agentstate.Provider, error) {
		return sqlite.New(":memory:")
	})
}

func TestPostgresProvider(t *testing.T) {
	dsn := os.Getenv("AGENTSTATE_POSTGRES_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	}
	ctx := context.Background()
	p, err := postgres.New(dsn)
	if err != nil {
		t.Skipf("PostgreSQL unavailable: %v", err)
	}
	_ = p.DeleteNamespace(ctx, "test-ns")
	_ = p.CreateNamespace(ctx, agentstate.Namespace{ID: "test-ns", CreatedAt: time.Now()})

	RunProviderContractTests(t, "postgres", func() (agentstate.Provider, error) {
		return p, nil
	})
}
