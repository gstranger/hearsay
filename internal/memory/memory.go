package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

type Provider struct {
	mu              sync.RWMutex
	namespaces      map[string]hearsay.Namespace
	messages        map[string][]hearsay.Message
	offsets         map[string]hearsay.Offset
	mailboxMessages map[string][]hearsay.MailboxMessage
	a2aTasks        map[string]*hearsay.A2ATask
	a2aHistory      map[string][]hearsay.A2AMessage
	a2aArtifacts    map[string][]hearsay.A2AArtifact
	auditEvents    map[string][]hearsay.AuditEvent
}

func New() *Provider {
	return &Provider{
		namespaces:      make(map[string]hearsay.Namespace),
		messages:        make(map[string][]hearsay.Message),
		offsets:         make(map[string]hearsay.Offset),
		mailboxMessages: make(map[string][]hearsay.MailboxMessage),
		a2aTasks:        make(map[string]*hearsay.A2ATask),
		a2aHistory:      make(map[string][]hearsay.A2AMessage),
		a2aArtifacts:    make(map[string][]hearsay.A2AArtifact),
		auditEvents:    make(map[string][]hearsay.AuditEvent),
	}
}

func (p *Provider) CreateNamespace(ctx context.Context, ns hearsay.Namespace) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.namespaces[ns.ID]; ok {
		return fmt.Errorf("namespace %s already exists", ns.ID)
	}
	p.namespaces[ns.ID] = ns
	p.messages[ns.ID] = nil
	p.offsets[ns.ID] = 0
	return nil
}

func (p *Provider) DeleteNamespace(ctx context.Context, namespaceID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.namespaces, namespaceID)
	delete(p.messages, namespaceID)
	delete(p.offsets, namespaceID)
	delete(p.mailboxMessages, namespaceID)
	for k := range p.a2aTasks {
		if prefix := namespaceID + "/"; len(k) > len(prefix) && k[:len(prefix)] == prefix {
			delete(p.a2aTasks, k)
			delete(p.a2aHistory, k)
			delete(p.a2aArtifacts, k)
		}
	}
	return nil
}

func (p *Provider) Append(ctx context.Context, namespaceID string, msgs []hearsay.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range msgs {
		p.offsets[namespaceID]++
		msgs[i].Offset = int64(p.offsets[namespaceID])
		msgs[i].Namespace = namespaceID
		msgs[i].Timestamp = time.Now()
		p.messages[namespaceID] = append(p.messages[namespaceID], msgs[i])
	}
	return nil
}

func (p *Provider) Query(ctx context.Context, namespaceID string, opts hearsay.QueryOpts) ([]hearsay.Message, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	msgs := p.messages[namespaceID]
	var result []hearsay.Message
	for _, m := range msgs {
		if m.Offset <= int64(opts.Since) {
			continue
		}
		if len(opts.Types) > 0 && !containsType(opts.Types, m.Type) {
			continue
		}
		if opts.AgentID != "" && m.AgentID != opts.AgentID {
			continue
		}
		result = append(result, m)
		if opts.Limit > 0 && len(result) >= opts.Limit {
			break
		}
	}
	return result, nil
}

func (p *Provider) Subscribe(ctx context.Context, namespaceID string, from hearsay.Offset) (<-chan hearsay.Message, error) {
	ch := make(chan hearsay.Message, 100)
	go func() {
		defer close(ch)
		for {
			msgs, _ := p.Query(ctx, namespaceID, hearsay.QueryOpts{Since: from})
			for _, m := range msgs {
				select {
				case ch <- m:
					from = hearsay.Offset(m.Offset)
				case <-ctx.Done():
					return
				}
			}
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (p *Provider) ActiveClaims(ctx context.Context, namespaceID string, resourcePattern string) ([]hearsay.Claim, error) {
	msgs, err := p.Query(ctx, namespaceID, hearsay.QueryOpts{})
	if err != nil {
		return nil, err
	}
	var result []hearsay.Claim
	for _, c := range hearsay.FilterActiveClaims(msgs) {
		if hearsay.ResourceMatchesPattern(resourcePattern, c.ResourceURI) {
			result = append(result, c)
		}
	}
	return result, nil
}

func (p *Provider) AgentState(ctx context.Context, namespaceID string, agentID string) (hearsay.AgentState, error) {
	msgs, err := p.Query(ctx, namespaceID, hearsay.QueryOpts{})
	if err != nil {
		return hearsay.AgentState{}, err
	}
	var active []string
	var lastSeen time.Time
	for _, m := range msgs {
		if m.AgentID == agentID && m.Timestamp.After(lastSeen) {
			lastSeen = m.Timestamp
		}
	}
	for _, c := range hearsay.FilterActiveClaims(msgs) {
		if c.AgentID == agentID {
			active = append(active, c.ClaimID)
		}
	}
	return hearsay.AgentState{AgentID: agentID, ActiveClaims: active, LastSeen: lastSeen}, nil
}

func (p *Provider) ReleaseExpired(ctx context.Context, namespaceID string, before time.Time) error {
	return nil // no-op for in-memory; ActiveClaims filters expired claims on read
}

func (p *Provider) SendMessage(ctx context.Context, namespace string, msg hearsay.MailboxMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	if msg.ExpiresAt.IsZero() {
		msg.ExpiresAt = msg.CreatedAt.Add(300 * time.Second)
	}
	p.mailboxMessages[namespace] = append(p.mailboxMessages[namespace], msg)
	return nil
}

func (p *Provider) GetMailbox(ctx context.Context, namespace string, agentID string, opts hearsay.MailboxQueryOpts) ([]hearsay.MailboxMessage, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []hearsay.MailboxMessage
	now := time.Now().UTC()
	for _, m := range p.mailboxMessages[namespace] {
		if m.Archived || m.ExpiresAt.Before(now) {
			continue
		}
		if m.To != agentID && m.To != hearsay.MailboxToBroadcast {
			continue
		}
		if opts.Unread && m.Read {
			continue
		}
		result = append(result, m)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})

	if opts.Limit > 0 && len(result) > opts.Limit {
		result = result[:opts.Limit]
	}
	return result, nil
}

func (p *Provider) MarkRead(ctx context.Context, namespace string, messageID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.mailboxMessages[namespace] {
		if p.mailboxMessages[namespace][i].MessageID == messageID {
			p.mailboxMessages[namespace][i].Read = true
			return nil
		}
	}
	return fmt.Errorf("message %s not found", messageID)
}

func (p *Provider) ArchiveMessage(ctx context.Context, namespace string, messageID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.mailboxMessages[namespace] {
		if p.mailboxMessages[namespace][i].MessageID == messageID {
			p.mailboxMessages[namespace][i].Archived = true
			return nil
		}
	}
	return fmt.Errorf("message %s not found", messageID)
}

func (p *Provider) ExpireMessages(ctx context.Context, namespace string, before time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var kept []hearsay.MailboxMessage
	for _, m := range p.mailboxMessages[namespace] {
		if !m.ExpiresAt.Before(before) {
			kept = append(kept, m)
		}
	}
	p.mailboxMessages[namespace] = kept
	return nil
}

// A2A task storage — in-memory provider (for tests)
func (p *Provider) CreateTask(ctx context.Context, namespace string, task *hearsay.A2ATask) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := namespace + "/" + task.ID
	if _, ok := p.a2aTasks[key]; ok {
		return fmt.Errorf("task %s already exists", task.ID)
	}
	t := *task
	p.a2aTasks[key] = &t
	return nil
}

func (p *Provider) GetTask(ctx context.Context, namespace string, taskID string) (*hearsay.A2ATask, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	key := namespace + "/" + taskID
	task, ok := p.a2aTasks[key]
	if !ok {
		return nil, fmt.Errorf("task %s not found", taskID)
	}
	t := *task
	return &t, nil
}

func (p *Provider) UpdateTask(ctx context.Context, namespace string, task *hearsay.A2ATask) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := namespace + "/" + task.ID
	if _, ok := p.a2aTasks[key]; !ok {
		return fmt.Errorf("task %s not found", task.ID)
	}
	t := *task
	p.a2aTasks[key] = &t
	return nil
}

func (p *Provider) AppendTaskHistory(ctx context.Context, namespace string, taskID string, seq int, msg hearsay.A2AMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := namespace + "/" + taskID
	p.a2aHistory[key] = append(p.a2aHistory[key], msg)
	return nil
}

func (p *Provider) GetTaskHistory(ctx context.Context, namespace string, taskID string, limit int) ([]hearsay.A2AMessage, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	key := namespace + "/" + taskID
	hist := p.a2aHistory[key]
	if limit > 0 && len(hist) > limit {
		hist = hist[:limit]
	}
	out := make([]hearsay.A2AMessage, len(hist))
	copy(out, hist)
	return out, nil
}

func (p *Provider) CreateArtifact(ctx context.Context, namespace string, taskID string, art hearsay.A2AArtifact) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := namespace + "/" + taskID
	p.a2aArtifacts[key] = append(p.a2aArtifacts[key], art)
	return nil
}

func (p *Provider) GetArtifacts(ctx context.Context, namespace string, taskID string) ([]hearsay.A2AArtifact, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	key := namespace + "/" + taskID
	arts := p.a2aArtifacts[key]
	out := make([]hearsay.A2AArtifact, len(arts))
	copy(out, arts)
	return out, nil
}

func containsType(types []hearsay.MessageType, t hearsay.MessageType) bool {
	for _, tt := range types {
		if tt == t {
			return true
		}
	}
	return false
}

// SubscribeEvents streams events from the in-memory message log.
func (p *Provider) SubscribeEvents(ctx context.Context, namespaceID string, since int64) (<-chan hearsay.Event, error) {
	ch := make(chan hearsay.Event, 100)
	go func() {
		defer close(ch)

		// Replay: catch up on events since the given sequence
		p.mu.RLock()
		msgs := p.messages[namespaceID]
		currentSeq := since
		for _, m := range msgs {
			// memory provider uses offset as sequence
			seq := m.Offset
			if seq <= since {
				continue
			}
			evt := hearsay.Event{
				Seq:       seq,
				Type:      messageTypeToEventType(m.Type),
				Payload:   m.Payload,
				Timestamp: m.Timestamp,
			}
			select {
			case ch <- evt:
				currentSeq = seq
			case <-ctx.Done():
				p.mu.RUnlock()
				return
			}
		}
		since = currentSeq
		p.mu.RUnlock()

		// Stream: poll for new events
		for {
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return
			}

			p.mu.RLock()
			msgs := p.messages[namespaceID]
			for _, m := range msgs {
				if m.Offset <= since {
					continue
				}
				evt := hearsay.Event{
					Seq:       m.Offset,
					Type:      messageTypeToEventType(m.Type),
					Payload:   m.Payload,
					Timestamp: m.Timestamp,
				}
				select {
				case ch <- evt:
					since = m.Offset
				case <-ctx.Done():
					p.mu.RUnlock()
					return
				}
			}
			p.mu.RUnlock()
		}
	}()
	return ch, nil
}

func messageTypeToEventType(t hearsay.MessageType) hearsay.EventType {
	switch t {
	case hearsay.MsgClaim:
		return hearsay.EventClaim
	case hearsay.MsgRelease, hearsay.MsgTransfer:
		return hearsay.EventRelease
	case hearsay.MsgHeartbeat:
		return hearsay.EventHeartbeat
	case hearsay.MsgMailboxSend, hearsay.MsgMailboxRead, hearsay.MsgMailboxArchive:
		return hearsay.EventMailbox
	default:
		return hearsay.EventType(t)
	}
}

func (p *Provider) AppendAudit(ctx context.Context, namespaceID string, events []hearsay.AuditEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.auditEvents[namespaceID] = append(p.auditEvents[namespaceID], events...)
	return nil
}
