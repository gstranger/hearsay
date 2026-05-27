package mailbox

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

type Service struct {
	provider hearsay.Provider
}

func New(provider hearsay.Provider) *Service {
	return &Service{provider: provider}
}

func (s *Service) Send(ctx context.Context, namespace string, from, to, msgType, content string, relatedClaimID string, ttlSeconds int) (string, error) {
	if !hearsay.IsValidMailboxType(hearsay.MailboxType(msgType)) {
		return "", fmt.Errorf("invalid message type: %s", msgType)
	}
	if content == "" {
		return "", fmt.Errorf("content cannot be empty")
	}
	if from == "" {
		return "", fmt.Errorf("from cannot be empty")
	}
	if to == "" {
		return "", fmt.Errorf("to cannot be empty")
	}
	if ttlSeconds <= 0 {
		ttlSeconds = 300
	}

	msg := hearsay.MailboxMessage{
		MessageID:      uuid.New().String(),
		From:           from,
		To:             to,
		Type:           hearsay.MailboxType(msgType),
		Content:        content,
		RelatedClaimID: relatedClaimID,
		Read:           false,
		Archived:       false,
		CreatedAt:      time.Now().UTC(),
		ExpiresAt:      time.Now().UTC().Add(time.Duration(ttlSeconds) * time.Second),
	}

	if err := s.provider.SendMessage(ctx, namespace, msg); err != nil {
		return "", err
	}
	return msg.MessageID, nil
}

func (s *Service) GetMailbox(ctx context.Context, namespace, agentID string, opts hearsay.MailboxQueryOpts) ([]hearsay.MailboxMessage, error) {
	return s.provider.GetMailbox(ctx, namespace, agentID, opts)
}

func (s *Service) MarkRead(ctx context.Context, namespace, messageID string) error {
	return s.provider.MarkRead(ctx, namespace, messageID)
}

func (s *Service) Archive(ctx context.Context, namespace, messageID string) error {
	return s.provider.ArchiveMessage(ctx, namespace, messageID)
}

func (s *Service) Expire(ctx context.Context, namespace string) error {
	return s.provider.ExpireMessages(ctx, namespace, time.Now().UTC())
}
