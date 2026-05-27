package postgres

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/thunder/agentstate/internal"
	"github.com/thunder/agentstate/pkg/agentstate"
)

func TestPostgresProviderContract(t *testing.T) {
	dsn := os.Getenv("AGENTSTATE_POSTGRES_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	}
	ctx := context.Background()
	p, err := New(dsn)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}
	_ = p.DeleteNamespace(ctx, "test-ns")
	_ = p.CreateNamespace(ctx, agentstate.Namespace{ID: "test-ns", CreatedAt: time.Now()})

	internal.RunProviderContractTests(t, "postgres", func() (agentstate.Provider, error) {
		return p, nil
	})
}

func TestPostgresProviderNamespaceIsolation(t *testing.T) {
	dsn := os.Getenv("AGENTSTATE_POSTGRES_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	}
	ctx := context.Background()
	p, err := New(dsn)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}

	_ = p.DeleteNamespace(ctx, "ns-a")
	_ = p.DeleteNamespace(ctx, "ns-b")
	_ = p.CreateNamespace(ctx, agentstate.Namespace{ID: "ns-a", CreatedAt: time.Now()})
	_ = p.CreateNamespace(ctx, agentstate.Namespace{ID: "ns-b", CreatedAt: time.Now()})

	claim := agentstate.Claim{ClaimID: "c1", AgentID: "a1", ResourceURI: "file://x.ts", Operation: agentstate.OpWrite, TTLSeconds: 300, CreatedAt: time.Now()}
	payload, _ := json.Marshal(claim)
	if err := p.Append(ctx, "ns-a", []agentstate.Message{{Type: agentstate.MsgClaim, AgentID: "a1", Payload: payload}}); err != nil {
		t.Fatalf("append failed: %v", err)
	}

	claimsA, errA := p.ActiveClaims(ctx, "ns-a", "file://x.ts")
	claimsB, errB := p.ActiveClaims(ctx, "ns-b", "file://x.ts")
	if errA != nil {
		t.Fatalf("ActiveClaims ns-a: %v", errA)
	}
	if errB != nil {
		t.Fatalf("ActiveClaims ns-b: %v", errB)
	}

	if len(claimsA) != 1 {
		t.Fatalf("expected 1 claim in ns-a, got %d", len(claimsA))
	}
	if len(claimsB) != 0 {
		t.Fatalf("expected 0 claims in ns-b, got %d", len(claimsB))
	}
}
