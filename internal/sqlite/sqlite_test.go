package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

func TestProvider_CreateNamespace(t *testing.T) {
	ctx := context.Background()
	p, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CreateNamespace(ctx, hearsay.Namespace{ID: "test"}); err != nil {
		t.Fatal(err)
	}
}

func TestProvider_AppendAndQuery(t *testing.T) {
	ctx := context.Background()
	p, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	p.CreateNamespace(ctx, hearsay.Namespace{ID: "test"})

	msg := hearsay.Message{Type: hearsay.MsgClaim, AgentID: "a1", Payload: []byte(`{"claim_id":"c1"}`)}
	if err := p.Append(ctx, "test", []hearsay.Message{msg}); err != nil {
		t.Fatal(err)
	}

	msgs, err := p.Query(ctx, "test", hearsay.QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
}

func TestProvider_ActiveClaims(t *testing.T) {
	ctx := context.Background()
	p, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	p.CreateNamespace(ctx, hearsay.Namespace{ID: "test"})

	claim := hearsay.Claim{ClaimID: "c1", AgentID: "a1", ResourceURI: "file://x.ts", Operation: hearsay.OpWrite, TTLSeconds: 300, CreatedAt: time.Now()}
	payload, _ := json.Marshal(claim)
	p.Append(ctx, "test", []hearsay.Message{{Type: hearsay.MsgClaim, AgentID: "a1", Payload: payload}})

	claims, err := p.ActiveClaims(ctx, "test", "file://x.ts")
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 {
		t.Fatalf("expected 1 active claim, got %d", len(claims))
	}
}

func TestSendMessage(t *testing.T) {
	ctx := context.Background()
	p, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CreateNamespace(ctx, hearsay.Namespace{ID: "ns1"}); err != nil {
		t.Fatal(err)
	}

	msg := hearsay.MailboxMessage{
		MessageID: "m1",
		From:      "a1",
		To:        "a2",
		Type:      hearsay.MailboxTypeNote,
		Content:   "hello",
	}
	if err := p.SendMessage(ctx, "ns1", msg); err != nil {
		t.Fatal(err)
	}

	msgs, err := p.GetMailbox(ctx, "ns1", "a2", hearsay.MailboxQueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].MessageID != "m1" {
		t.Fatalf("expected message_id m1, got %s", msgs[0].MessageID)
	}
}

func TestGetMailbox(t *testing.T) {
	ctx := context.Background()
	p, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CreateNamespace(ctx, hearsay.Namespace{ID: "ns1"}); err != nil {
		t.Fatal(err)
	}

	_ = p.SendMessage(ctx, "ns1", hearsay.MailboxMessage{MessageID: "m1", From: "a1", To: "a2", Type: hearsay.MailboxTypeNote, Content: "c1", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)})
	_ = p.SendMessage(ctx, "ns1", hearsay.MailboxMessage{MessageID: "m2", From: "a2", To: "a3", Type: hearsay.MailboxTypeNote, Content: "c2", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)})
	_ = p.SendMessage(ctx, "ns1", hearsay.MailboxMessage{MessageID: "m3", From: "a1", To: "broadcast", Type: hearsay.MailboxTypeNote, Content: "c3", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)})

	msgs, err := p.GetMailbox(ctx, "ns1", "a2", hearsay.MailboxQueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages for a2, got %d", len(msgs))
	}
}

func TestGetMailbox_UnreadFilter(t *testing.T) {
	ctx := context.Background()
	p, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CreateNamespace(ctx, hearsay.Namespace{ID: "ns1"}); err != nil {
		t.Fatal(err)
	}

	_ = p.SendMessage(ctx, "ns1", hearsay.MailboxMessage{MessageID: "m1", From: "a1", To: "a2", Type: hearsay.MailboxTypeNote, Content: "c1", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)})
	_ = p.SendMessage(ctx, "ns1", hearsay.MailboxMessage{MessageID: "m2", From: "a1", To: "a2", Type: hearsay.MailboxTypeNote, Content: "c2", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)})
	_ = p.MarkRead(ctx, "ns1", "m1")

	msgs, err := p.GetMailbox(ctx, "ns1", "a2", hearsay.MailboxQueryOpts{Unread: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 unread message, got %d", len(msgs))
	}
	if msgs[0].MessageID != "m2" {
		t.Fatalf("expected unread message m2, got %s", msgs[0].MessageID)
	}
}

func TestMarkRead(t *testing.T) {
	ctx := context.Background()
	p, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CreateNamespace(ctx, hearsay.Namespace{ID: "ns1"}); err != nil {
		t.Fatal(err)
	}

	_ = p.SendMessage(ctx, "ns1", hearsay.MailboxMessage{MessageID: "m1", From: "a1", To: "a2", Type: hearsay.MailboxTypeNote, Content: "c1", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err := p.MarkRead(ctx, "ns1", "m1"); err != nil {
		t.Fatal(err)
	}

	msgs, err := p.GetMailbox(ctx, "ns1", "a2", hearsay.MailboxQueryOpts{Unread: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 unread messages after mark read, got %d", len(msgs))
	}
}

func TestArchiveMessage(t *testing.T) {
	ctx := context.Background()
	p, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CreateNamespace(ctx, hearsay.Namespace{ID: "ns1"}); err != nil {
		t.Fatal(err)
	}

	_ = p.SendMessage(ctx, "ns1", hearsay.MailboxMessage{MessageID: "m1", From: "a1", To: "a2", Type: hearsay.MailboxTypeNote, Content: "c1", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err := p.ArchiveMessage(ctx, "ns1", "m1"); err != nil {
		t.Fatal(err)
	}

	msgs, err := p.GetMailbox(ctx, "ns1", "a2", hearsay.MailboxQueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages after archive, got %d", len(msgs))
	}
}

func TestExpireMessages(t *testing.T) {
	ctx := context.Background()
	p, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CreateNamespace(ctx, hearsay.Namespace{ID: "ns1"}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	_ = p.SendMessage(ctx, "ns1", hearsay.MailboxMessage{MessageID: "m1", From: "a1", To: "a2", Type: hearsay.MailboxTypeNote, Content: "c1", CreatedAt: now, ExpiresAt: now.Add(-time.Second)})
	if err := p.ExpireMessages(ctx, "ns1", now); err != nil {
		t.Fatal(err)
	}

	msgs, err := p.GetMailbox(ctx, "ns1", "a2", hearsay.MailboxQueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages after expire, got %d", len(msgs))
	}
}
