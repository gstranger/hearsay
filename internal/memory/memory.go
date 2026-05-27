package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/thunder/agentstate/pkg/agentstate"
)

type Provider struct {
	mu              sync.RWMutex
	namespaces      map[string]agentstate.Namespace
	messages        map[string][]agentstate.Message
	offsets         map[string]agentstate.Offset
	mailboxMessages map[string][]agentstate.MailboxMessage
	a2aTasks        map[string]*agentstate.A2ATask
	a2aHistory      map[string][]agentstate.A2AMessage
	a2aArtifacts    map[string][]agentstate.A2AArtifact
}

func New() *Provider {
	return &Provider{
		namespaces:      make(map[string]agentstate.Namespace),
		messages:        make(map[string][]agentstate.Message),
		offsets:         make(map[string]agentstate.Offset),
		mailboxMessages: make(map[string][]agentstate.MailboxMessage),
		a2aTasks:        make(map[string]*agentstate.A2ATask),
		a2aHistory:      make(map[string][]agentstate.A2AMessage),
		a2aArtifacts:    make(map[string][]agentstate.A2AArtifact),
	}
}

func (p *Provider) CreateNamespace(ctx context.Context, ns agentstate.Namespace) error {
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

func (p *Provider) Append(ctx context.Context, namespaceID string, msgs []agentstate.Message) error {
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

func (p *Provider) Query(ctx context.Context, namespaceID string, opts agentstate.QueryOpts) ([]agentstate.Message, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	msgs := p.messages[namespaceID]
	var result []agentstate.Message
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

func (p *Provider) Subscribe(ctx context.Context, namespaceID string, from agentstate.Offset) (<-chan agentstate.Message, error) {
	ch := make(chan agentstate.Message, 100)
	go func() {
		defer close(ch)
		for {
			msgs, _ := p.Query(ctx, namespaceID, agentstate.QueryOpts{Since: from})
			for _, m := range msgs {
				select {
				case ch <- m:
					from = agentstate.Offset(m.Offset)
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

func (p *Provider) ActiveClaims(ctx context.Context, namespaceID string, resourcePattern string) ([]agentstate.Claim, error) {
	msgs, err := p.Query(ctx, namespaceID, agentstate.QueryOpts{})
	if err != nil {
		return nil, err
	}
	var result []agentstate.Claim
	for _, c := range agentstate.FilterActiveClaims(msgs) {
		if agentstate.ResourceMatchesPattern(resourcePattern, c.ResourceURI) {
			result = append(result, c)
		}
	}
	return result, nil
}

func (p *Provider) AgentState(ctx context.Context, namespaceID string, agentID string) (agentstate.AgentState, error) {
	msgs, err := p.Query(ctx, namespaceID, agentstate.QueryOpts{})
	if err != nil {
		return agentstate.AgentState{}, err
	}
	var active []string
	var lastSeen time.Time
	for _, m := range msgs {
		if m.AgentID == agentID && m.Timestamp.After(lastSeen) {
			lastSeen = m.Timestamp
		}
	}
	for _, c := range agentstate.FilterActiveClaims(msgs) {
		if c.AgentID == agentID {
			active = append(active, c.ClaimID)
		}
	}
	return agentstate.AgentState{AgentID: agentID, ActiveClaims: active, LastSeen: lastSeen}, nil
}

func (p *Provider) ReleaseExpired(ctx context.Context, namespaceID string, before time.Time) error {
	return nil // no-op for in-memory; ActiveClaims filters expired claims on read
}

func (p *Provider) SendMessage(ctx context.Context, namespace string, msg agentstate.MailboxMessage) error {
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

func (p *Provider) GetMailbox(ctx context.Context, namespace string, agentID string, opts agentstate.MailboxQueryOpts) ([]agentstate.MailboxMessage, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []agentstate.MailboxMessage
	now := time.Now().UTC()
	for _, m := range p.mailboxMessages[namespace] {
		if m.Archived || m.ExpiresAt.Before(now) {
			continue
		}
		if m.To != agentID && m.To != agentstate.MailboxToBroadcast {
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
	var kept []agentstate.MailboxMessage
	for _, m := range p.mailboxMessages[namespace] {
		if !m.ExpiresAt.Before(before) {
			kept = append(kept, m)
		}
	}
	p.mailboxMessages[namespace] = kept
	return nil
}

// A2A task storage — in-memory provider (for tests)
func (p *Provider) CreateTask(ctx context.Context, namespace string, task *agentstate.A2ATask) error {
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

func (p *Provider) GetTask(ctx context.Context, namespace string, taskID string) (*agentstate.A2ATask, error) {
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

func (p *Provider) UpdateTask(ctx context.Context, namespace string, task *agentstate.A2ATask) error {
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

func (p *Provider) AppendTaskHistory(ctx context.Context, namespace string, taskID string, seq int, msg agentstate.A2AMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := namespace + "/" + taskID
	p.a2aHistory[key] = append(p.a2aHistory[key], msg)
	return nil
}

func (p *Provider) GetTaskHistory(ctx context.Context, namespace string, taskID string, limit int) ([]agentstate.A2AMessage, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	key := namespace + "/" + taskID
	hist := p.a2aHistory[key]
	if limit > 0 && len(hist) > limit {
		hist = hist[:limit]
	}
	out := make([]agentstate.A2AMessage, len(hist))
	copy(out, hist)
	return out, nil
}

func (p *Provider) CreateArtifact(ctx context.Context, namespace string, taskID string, art agentstate.A2AArtifact) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := namespace + "/" + taskID
	p.a2aArtifacts[key] = append(p.a2aArtifacts[key], art)
	return nil
}

func (p *Provider) GetArtifacts(ctx context.Context, namespace string, taskID string) ([]agentstate.A2AArtifact, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	key := namespace + "/" + taskID
	arts := p.a2aArtifacts[key]
	out := make([]agentstate.A2AArtifact, len(arts))
	copy(out, arts)
	return out, nil
}

func containsType(types []agentstate.MessageType, t agentstate.MessageType) bool {
	for _, tt := range types {
		if tt == t {
			return true
		}
	}
	return false
}
