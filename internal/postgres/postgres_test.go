package postgres

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/gstranger/hearsay/internal"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

func TestPostgresProviderContract(t *testing.T) {
	dsn := os.Getenv("HEARSAY_POSTGRES_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	}
	ctx := context.Background()
	p, err := New(dsn)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}
	_ = p.DeleteNamespace(ctx, "test-ns")
	_ = p.CreateNamespace(ctx, hearsay.Namespace{ID: "test-ns", CreatedAt: time.Now()})

	internal.RunProviderContractTests(t, "postgres", func() (hearsay.Provider, error) {
		return p, nil
	})
}

func TestPostgresProviderNamespaceIsolation(t *testing.T) {
	dsn := os.Getenv("HEARSAY_POSTGRES_DSN")
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
	_ = p.CreateNamespace(ctx, hearsay.Namespace{ID: "ns-a", CreatedAt: time.Now()})
	_ = p.CreateNamespace(ctx, hearsay.Namespace{ID: "ns-b", CreatedAt: time.Now()})

	claim := hearsay.Claim{ClaimID: "c1", AgentID: "a1", ResourceURI: "file://x.ts", Operation: hearsay.OpWrite, TTLSeconds: 300, CreatedAt: time.Now()}
	payload, _ := json.Marshal(claim)
	if err := p.Append(ctx, "ns-a", []hearsay.Message{{Type: hearsay.MsgClaim, AgentID: "a1", Payload: payload}}); err != nil {
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
