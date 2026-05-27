package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

// ProviderFactory creates a fresh provider for contract testing.
type ProviderFactory func() (hearsay.Provider, error)

// RunProviderContractTests runs the standard provider contract suite.
// Each subtest uses a unique namespace derived from the test name to avoid
// cross-test state pollution when the factory reuses the same provider.
func RunProviderContractTests(t *testing.T, name string, factory ProviderFactory) {
	t.Run(name+"/CreateNamespace", func(t *testing.T) {
		testCreateNamespace(t, factory, fmt.Sprintf("ns-%s-CreateNamespace", name))
	})
	t.Run(name+"/AppendAndQuery", func(t *testing.T) {
		testAppendAndQuery(t, factory, fmt.Sprintf("ns-%s-AppendAndQuery", name))
	})
	t.Run(name+"/ActiveClaims", func(t *testing.T) {
		testActiveClaims(t, factory, fmt.Sprintf("ns-%s-ActiveClaims", name))
	})
	t.Run(name+"/Release", func(t *testing.T) {
		testRelease(t, factory, fmt.Sprintf("ns-%s-Release", name))
	})
	t.Run(name+"/A2ATaskCRUD", func(t *testing.T) {
		testA2ATaskCRUD(t, factory, fmt.Sprintf("ns-%s-A2ATaskCRUD", name))
	})
}

func testCreateNamespace(t *testing.T, factory ProviderFactory, nsID string) {
	ctx := context.Background()
	p, err := factory()
	if err != nil {
		t.Fatal(err)
	}
	_ = p.DeleteNamespace(ctx, nsID)
	if err := p.CreateNamespace(ctx, hearsay.Namespace{ID: nsID, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func testAppendAndQuery(t *testing.T, factory ProviderFactory, nsID string) {
	ctx := context.Background()
	p, err := factory()
	if err != nil {
		t.Fatal(err)
	}
	_ = p.DeleteNamespace(ctx, nsID)
	p.CreateNamespace(ctx, hearsay.Namespace{ID: nsID, CreatedAt: time.Now()})

	msg := hearsay.Message{Type: hearsay.MsgClaim, AgentID: "a1", Payload: []byte(`{"x":1}`)}
	if err := p.Append(ctx, nsID, []hearsay.Message{msg}); err != nil {
		t.Fatal(err)
	}

	msgs, err := p.Query(ctx, nsID, hearsay.QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1, got %d", len(msgs))
	}
	if msgs[0].Offset < 1 {
		t.Fatalf("expected offset >= 1, got %d", msgs[0].Offset)
	}
}

func testActiveClaims(t *testing.T, factory ProviderFactory, nsID string) {
	ctx := context.Background()
	p, err := factory()
	if err != nil {
		t.Fatal(err)
	}
	_ = p.DeleteNamespace(ctx, nsID)
	p.CreateNamespace(ctx, hearsay.Namespace{ID: nsID, CreatedAt: time.Now()})

	claim := hearsay.Claim{ClaimID: "c1", AgentID: "a1", ResourceURI: "file://x.ts", Operation: hearsay.OpWrite, TTLSeconds: 300, CreatedAt: time.Now()}
	payload, _ := json.Marshal(claim)
	p.Append(ctx, nsID, []hearsay.Message{{Type: hearsay.MsgClaim, AgentID: "a1", Payload: payload}})

	claims, err := p.ActiveClaims(ctx, nsID, "file://x.ts")
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].ClaimID != "c1" {
		t.Fatalf("expected claim c1, got %+v", claims)
	}
}

func testA2ATaskCRUD(t *testing.T, factory ProviderFactory, nsID string) {
	ctx := context.Background()
	p, err := factory()
	if err != nil {
		t.Fatal(err)
	}
	_ = p.DeleteNamespace(ctx, nsID)
	p.CreateNamespace(ctx, hearsay.Namespace{ID: nsID, CreatedAt: time.Now()})

	task := &hearsay.A2ATask{
		ID: "task-1", State: "submitted", StatusTime: time.Now(),
		Namespace: nsID, CreatedAt: time.Now(),
	}
	if err := p.CreateTask(ctx, nsID, task); err != nil {
		t.Fatal(err)
	}

	got, err := p.GetTask(ctx, nsID, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "task-1" || got.State != "submitted" {
		t.Fatalf("expected task-1 submitted, got %+v", got)
	}

	task.State = "completed"
	if err := p.UpdateTask(ctx, nsID, task); err != nil {
		t.Fatal(err)
	}

	got, _ = p.GetTask(ctx, nsID, "task-1")
	if got.State != "completed" {
		t.Fatalf("expected completed, got %s", got.State)
	}
}

func testRelease(t *testing.T, factory ProviderFactory, nsID string) {
	ctx := context.Background()
	p, err := factory()
	if err != nil {
		t.Fatal(err)
	}
	_ = p.DeleteNamespace(ctx, nsID)
	p.CreateNamespace(ctx, hearsay.Namespace{ID: nsID, CreatedAt: time.Now()})

	claim := hearsay.Claim{ClaimID: "c1", AgentID: "a1", ResourceURI: "file://x.ts", Operation: hearsay.OpWrite, TTLSeconds: 300, CreatedAt: time.Now()}
	payload, _ := json.Marshal(claim)
	p.Append(ctx, nsID, []hearsay.Message{{Type: hearsay.MsgClaim, AgentID: "a1", Payload: payload}})

	relPayload, _ := json.Marshal(map[string]string{"claim_id": "c1", "outcome": "succeeded"})
	p.Append(ctx, nsID, []hearsay.Message{{Type: hearsay.MsgRelease, Payload: relPayload}})

	claims, err := p.ActiveClaims(ctx, nsID, "file://x.ts")
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("expected 0 active claims after release, got %d", len(claims))
	}
}
