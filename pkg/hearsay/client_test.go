package hearsay_test

import (
	"context"
	"testing"

	"github.com/gstranger/hearsay/internal/memory"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

func TestClient_ClaimAndRelease(t *testing.T) {
	ctx := context.Background()
	p := memory.New()
	if err := p.CreateNamespace(ctx, hearsay.Namespace{ID: "test"}); err != nil {
		t.Fatal(err)
	}

	c := hearsay.NewClient(p, "test")
	resp, err := c.Claim(ctx, hearsay.ClaimRequest{
		ResourceURI: "file://x.ts",
		AgentID:     "a1",
		Operation:   hearsay.OpWrite,
		Intent:      "fix bug",
	})
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	if resp.ClaimID == "" {
		t.Fatal("expected claim ID")
	}

	if err := c.Release(ctx, resp.ClaimID, hearsay.OutcomeSucceeded); err != nil {
		t.Fatalf("release failed: %v", err)
	}
}

func TestClient_ConflictDetected(t *testing.T) {
	ctx := context.Background()
	p := memory.New()
	if err := p.CreateNamespace(ctx, hearsay.Namespace{ID: "test"}); err != nil {
		t.Fatal(err)
	}

	c := hearsay.NewClient(p, "test")
	_, err := c.Claim(ctx, hearsay.ClaimRequest{
		ResourceURI: "file://x.ts",
		AgentID:     "a1",
		Operation:   hearsay.OpWrite,
		Intent:      "first",
	})
	if err != nil {
		t.Fatalf("first claim failed: %v", err)
	}

	_, err = c.Claim(ctx, hearsay.ClaimRequest{
		ResourceURI: "file://x.ts",
		AgentID:     "a2",
		Operation:   hearsay.OpWrite,
		Intent:      "second",
	})
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if _, ok := err.(*hearsay.ConflictError); !ok {
		t.Fatalf("expected ConflictError, got %T", err)
	}
}

func TestClient_QueryClaims(t *testing.T) {
	ctx := context.Background()
	p := memory.New()
	if err := p.CreateNamespace(ctx, hearsay.Namespace{ID: "test"}); err != nil {
		t.Fatal(err)
	}

	c := hearsay.NewClient(p, "test")
	c.Claim(ctx, hearsay.ClaimRequest{ResourceURI: "file://a.ts", AgentID: "a1", Operation: hearsay.OpWrite, Intent: "x"})
	c.Claim(ctx, hearsay.ClaimRequest{ResourceURI: "file://b.ts", AgentID: "a1", Operation: hearsay.OpWrite, Intent: "y"})

	claims, err := c.QueryClaims(ctx, hearsay.QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 {
		t.Fatalf("expected 2 claims, got %d", len(claims))
	}
}

func TestClient_CheckConflict(t *testing.T) {
	ctx := context.Background()
	p := memory.New()
	if err := p.CreateNamespace(ctx, hearsay.Namespace{ID: "test"}); err != nil {
		t.Fatal(err)
	}

	c := hearsay.NewClient(p, "test")
	c.Claim(ctx, hearsay.ClaimRequest{ResourceURI: "file://x.ts", AgentID: "a1", Operation: hearsay.OpWrite, Intent: "x"})

	report, err := c.CheckConflict(ctx, "file://x.ts", hearsay.OpWrite)
	if err != nil {
		t.Fatal(err)
	}
	if !report.HasConflict {
		t.Fatal("expected conflict")
	}
}
