package mailbox

import (
	"context"
	"testing"
	"time"

	"github.com/gstranger/hearsay/internal/memory"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

func TestSend_ValidMessage(t *testing.T) {
	svc := New(memory.New())
	id, err := svc.Send(context.Background(), "test-ns", "agent-A", "agent-B", "note", "hello", "", 300)
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if id == "" {
		t.Fatal("expected message id")
	}
}

func TestSend_InvalidType(t *testing.T) {
	svc := New(memory.New())
	_, err := svc.Send(context.Background(), "test-ns", "agent-A", "agent-B", "invalid", "hello", "", 300)
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
}

func TestSend_EmptyContent(t *testing.T) {
	svc := New(memory.New())
	_, err := svc.Send(context.Background(), "test-ns", "agent-A", "agent-B", "note", "", "", 300)
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestGetMailbox(t *testing.T) {
	svc := New(memory.New())
	ctx := context.Background()

	svc.Send(ctx, "test-ns", "agent-A", "agent-B", "note", "msg1", "", 300)
	svc.Send(ctx, "test-ns", "agent-A", "agent-C", "note", "msg2", "", 300)

	msgs, err := svc.GetMailbox(ctx, "test-ns", "agent-B", hearsay.MailboxQueryOpts{})
	if err != nil {
		t.Fatalf("get mailbox failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "msg1" {
		t.Fatalf("expected msg1, got %s", msgs[0].Content)
	}
}

func TestGetMailbox_UnreadFilter(t *testing.T) {
	svc := New(memory.New())
	ctx := context.Background()

	svc.Send(ctx, "test-ns", "agent-A", "agent-B", "note", "msg1", "", 300)
	svc.Send(ctx, "test-ns", "agent-A", "agent-B", "note", "msg2", "", 300)

	msgs, _ := svc.GetMailbox(ctx, "test-ns", "agent-B", hearsay.MailboxQueryOpts{Unread: true})
	if len(msgs) != 2 {
		t.Fatalf("expected 2 unread, got %d", len(msgs))
	}

	svc.MarkRead(ctx, "test-ns", msgs[0].MessageID)

	msgs, _ = svc.GetMailbox(ctx, "test-ns", "agent-B", hearsay.MailboxQueryOpts{Unread: true})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 unread after mark read, got %d", len(msgs))
	}
}

func TestArchive(t *testing.T) {
	svc := New(memory.New())
	ctx := context.Background()

	id, _ := svc.Send(ctx, "test-ns", "agent-A", "agent-B", "note", "msg1", "", 300)
	svc.Archive(ctx, "test-ns", id)

	msgs, _ := svc.GetMailbox(ctx, "test-ns", "agent-B", hearsay.MailboxQueryOpts{})
	if len(msgs) != 0 {
		t.Fatalf("expected 0 archived messages, got %d", len(msgs))
	}
}

func TestExpire(t *testing.T) {
	svc := New(memory.New())
	ctx := context.Background()

	_, _ = svc.Send(ctx, "test-ns", "agent-A", "agent-B", "note", "msg1", "", 1)
	time.Sleep(2 * time.Second)
	svc.Expire(ctx, "test-ns")

	msgs, _ := svc.GetMailbox(ctx, "test-ns", "agent-B", hearsay.MailboxQueryOpts{})
	if len(msgs) != 0 {
		t.Fatalf("expected 0 expired messages, got %d", len(msgs))
	}
}

func TestBroadcast(t *testing.T) {
	svc := New(memory.New())
	ctx := context.Background()

	svc.Send(ctx, "test-ns", "agent-A", "broadcast", "note", "all hands", "", 300)

	msgs, _ := svc.GetMailbox(ctx, "test-ns", "agent-B", hearsay.MailboxQueryOpts{})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 broadcast message, got %d", len(msgs))
	}
}
