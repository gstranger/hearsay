package agentstate

import (
	"context"
	"encoding/json"
	"time"
)

type Offset int64

type Provider interface {
	CreateNamespace(ctx context.Context, ns Namespace) error
	DeleteNamespace(ctx context.Context, namespaceID string) error
	Append(ctx context.Context, namespaceID string, msgs []Message) error
	Query(ctx context.Context, namespaceID string, opts QueryOpts) ([]Message, error)
	Subscribe(ctx context.Context, namespaceID string, from Offset) (<-chan Message, error)
	ActiveClaims(ctx context.Context, namespaceID string, resourcePattern string) ([]Claim, error)
	AgentState(ctx context.Context, namespaceID string, agentID string) (AgentState, error)
	ReleaseExpired(ctx context.Context, namespaceID string, before time.Time) error

	// Mailbox methods (new)
	SendMessage(ctx context.Context, namespace string, msg MailboxMessage) error
	GetMailbox(ctx context.Context, namespace string, agentID string, opts MailboxQueryOpts) ([]MailboxMessage, error)
	MarkRead(ctx context.Context, namespace string, messageID string) error
	ArchiveMessage(ctx context.Context, namespace string, messageID string) error
	ExpireMessages(ctx context.Context, namespace string, before time.Time) error

	// A2A task storage methods
	CreateTask(ctx context.Context, namespace string, task *A2ATask) error
	GetTask(ctx context.Context, namespace string, taskID string) (*A2ATask, error)
	UpdateTask(ctx context.Context, namespace string, task *A2ATask) error
	AppendTaskHistory(ctx context.Context, namespace string, taskID string, seq int, msg A2AMessage) error
	GetTaskHistory(ctx context.Context, namespace string, taskID string, limit int) ([]A2AMessage, error)
	CreateArtifact(ctx context.Context, namespace string, taskID string, art A2AArtifact) error
	GetArtifacts(ctx context.Context, namespace string, taskID string) ([]A2AArtifact, error)
}

type Namespace struct {
	ID        string
	CreatedAt time.Time
}

type Message struct {
	Offset    int64           `json:"offset"`
	Type      MessageType     `json:"type"`
	Namespace string          `json:"namespace"`
	AgentID   string          `json:"agent_id"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp time.Time       `json:"timestamp"`
}

type MessageType string

const (
	MsgClaim     MessageType = "claim"
	MsgHeartbeat MessageType = "heartbeat"
	MsgRelease   MessageType = "release"
	MsgTransfer  MessageType = "transfer"
	MsgEscalate  MessageType = "escalate"
	MsgIntent    MessageType = "intent"
)

type QueryOpts struct {
	Since   Offset
	Types   []MessageType
	AgentID string
	Limit   int
}

type AgentState struct {
	AgentID      string
	ActiveClaims []string
	LastSeen     time.Time
}

type MailboxMessage struct {
	MessageID      string    `json:"message_id"`
	From           string    `json:"from"`
	To             string    `json:"to"`
	Type           MailboxType `json:"type"`
	Content        string    `json:"content"`
	RelatedClaimID string    `json:"related_claim_id,omitempty"`
	Read           bool      `json:"read"`
	Archived       bool      `json:"archived"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type MailboxQueryOpts struct {
	Unread        bool
	IncludeClaims bool
	Limit         int
}

type A2ATask struct {
	ID         string          `json:"id"`
	SessionID  string          `json:"session_id,omitempty"`
	State      string          `json:"state"`
	StatusMsg  json.RawMessage `json:"status_message,omitempty"`
	StatusTime time.Time       `json:"status_time"`
	ClaimID    string          `json:"claim_id,omitempty"`
	Namespace  string          `json:"namespace"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

type A2AMessage struct {
	Role     string          `json:"role"`
	Parts    json.RawMessage `json:"parts"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type A2AArtifact struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parts       json.RawMessage `json:"parts"`
	Index       int             `json:"index,omitempty"`
	Append      bool            `json:"append,omitempty"`
	LastChunk   bool            `json:"last_chunk,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}
