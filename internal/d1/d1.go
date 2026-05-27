//go:build wasm

package d1

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"syscall/js"
	"time"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

// Provider stores claims, messages, and tasks in Cloudflare D1.
// It uses the D1 binding passed from the Worker environment.
type Provider struct {
	binding js.Value // Cloudflare D1 binding object
}

// New creates a D1 provider from the D1 Worker binding.
func New(binding js.Value) *Provider {
	return &Provider{binding: binding}
}

// d1Exec runs a SQL statement with bind parameters against D1.
func (p *Provider) d1Exec(query string, params ...any) error {
	stmt := p.binding.Call("prepare", query)
	for i, param := range params {
		stmt = stmt.Call("bind", i+1, param)
	}
	result := stmt.Call("run")
	if result.Get("error").Truthy() {
		return fmt.Errorf("d1 error: %s", result.Get("error").String())
	}
	return nil
}

// d1All runs a query and returns all rows as a js.Value array.
func (p *Provider) d1All(query string, params ...any) js.Value {
	stmt := p.binding.Call("prepare", query)
	for i, param := range params {
		stmt = stmt.Call("bind", i+1, param)
	}
	result := stmt.Call("all")
	if result.Get("error").Truthy() {
		return js.Null()
	}
	return result.Get("results")
}

// d1First runs a query and returns the first row.
func (p *Provider) d1First(query string, params ...any) js.Value {
	results := p.d1All(query, params...)
	if results.IsNull() || results.Length() == 0 {
		return js.Null()
	}
	return results.Index(0)
}

func (p *Provider) CreateNamespace(ctx context.Context, ns hearsay.Namespace) error {
	return p.d1Exec("INSERT INTO namespaces (id, created_at) VALUES (?, ?)", ns.ID, ns.CreatedAt)
}

func (p *Provider) DeleteNamespace(ctx context.Context, namespaceID string) error {
	p.d1Exec("DELETE FROM a2a_task_history WHERE task_id IN (SELECT id FROM a2a_tasks WHERE namespace = ?)", namespaceID)
	p.d1Exec("DELETE FROM a2a_artifacts WHERE task_id IN (SELECT id FROM a2a_tasks WHERE namespace = ?)", namespaceID)
	p.d1Exec("DELETE FROM a2a_tasks WHERE namespace = ?", namespaceID)
	p.d1Exec("DELETE FROM messages WHERE namespace = ?", namespaceID)
	p.d1Exec("DELETE FROM mailbox_messages WHERE namespace = ?", namespaceID)
	p.d1Exec("DELETE FROM audit_events WHERE namespace = ?", namespaceID)
	return p.d1Exec("DELETE FROM namespaces WHERE id = ?", namespaceID)
}

func (p *Provider) Append(ctx context.Context, namespaceID string, msgs []hearsay.Message) error {
	for i := range msgs {
		row := p.d1First("SELECT COALESCE(MAX(offset), 0) + 1 as next_offset FROM messages WHERE namespace = ?", namespaceID)
		nextOffset := int64(row.Get("next_offset").Int())

		row2 := p.d1First("SELECT COALESCE(MAX(seq), 0) + 1 as next_seq FROM messages WHERE namespace = ?", namespaceID)
		nextSeq := int64(row2.Get("next_seq").Int())

		if msgs[i].Timestamp.IsZero() {
			msgs[i].Timestamp = time.Now()
		}
		if err := p.d1Exec(
			"INSERT INTO messages (namespace, offset, seq, type, agent_id, payload, timestamp) VALUES (?, ?, ?, ?, ?, ?, ?)",
			namespaceID, nextOffset, nextSeq, string(msgs[i].Type), msgs[i].AgentID, string(msgs[i].Payload), msgs[i].Timestamp,
		); err != nil {
			return err
		}
	}
	return nil
}

func (p *Provider) Query(ctx context.Context, namespaceID string, opts hearsay.QueryOpts) ([]hearsay.Message, error) {
	query := "SELECT offset, type, agent_id, payload, timestamp FROM messages WHERE namespace = ? AND offset > ?"
	args := []any{namespaceID, opts.Since}

	if len(opts.Types) > 0 {
		placeholders := make([]string, len(opts.Types))
		for i, t := range opts.Types {
			placeholders[i] = "?"
			args = append(args, string(t))
		}
		query += " AND type IN (" + strings.Join(placeholders, ",") + ")"
	}
	if opts.AgentID != "" {
		query += " AND agent_id = ?"
		args = append(args, opts.AgentID)
	}
	query += " ORDER BY offset"
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}

	results := p.d1All(query, args...)
	if results.IsNull() {
		return nil, nil
	}

	var msgs []hearsay.Message
	for i := 0; i < results.Length(); i++ {
		row := results.Index(i)
		msgs = append(msgs, hearsay.Message{
			Offset:    int64(row.Get("offset").Int()),
			Type:      hearsay.MessageType(row.Get("type").String()),
			AgentID:   row.Get("agent_id").String(),
			Payload:   json.RawMessage(row.Get("payload").String()),
			Timestamp: parseTime(row.Get("timestamp").String()),
			Namespace: namespaceID,
		})
	}
	return msgs, nil
}

func (p *Provider) Subscribe(ctx context.Context, namespaceID string, from hearsay.Offset) (<-chan hearsay.Message, error) {
	ch := make(chan hearsay.Message, 100)
	go func() {
		defer close(ch)
		current := from
		for {
			msgs, err := p.Query(ctx, namespaceID, hearsay.QueryOpts{Since: current})
			if err != nil {
				return
			}
			for _, m := range msgs {
				select {
				case ch <- m:
					current = hearsay.Offset(m.Offset)
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

// SubscribeEvents streams coordination events with sequence numbers for SSE.
func (p *Provider) SubscribeEvents(ctx context.Context, namespaceID string, since int64) (<-chan hearsay.Event, error) {
	ch := make(chan hearsay.Event, 100)
	go func() {
		defer close(ch)

		// Replay: catch up on events since the given sequence
		results := p.d1All(
			"SELECT seq, type, payload, timestamp FROM messages WHERE namespace = ? AND seq > ? ORDER BY seq",
			namespaceID, since)
		var currentSeq int64 = since
		if !results.IsNull() {
			for i := 0; i < results.Length(); i++ {
				row := results.Index(i)
				seq := int64(row.Get("seq").Int())
				evt := hearsay.Event{
					Seq:       seq,
					Type:      msgTypeStringToEvent(row.Get("type").String()),
					Payload:   json.RawMessage(row.Get("payload").String()),
					Timestamp: parseTime(row.Get("timestamp").String()),
				}
				select {
				case ch <- evt:
					currentSeq = seq
				case <-ctx.Done():
					return
				}
			}
		}
		since = currentSeq

		// Stream: poll for new events
		for {
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return
			}

			results := p.d1All(
				"SELECT seq, type, payload, timestamp FROM messages WHERE namespace = ? AND seq > ? ORDER BY seq",
				namespaceID, since)
			if results.IsNull() {
				continue
			}
			for i := 0; i < results.Length(); i++ {
				row := results.Index(i)
				seq := int64(row.Get("seq").Int())
				evt := hearsay.Event{
					Seq:       seq,
					Type:      msgTypeStringToEvent(row.Get("type").String()),
					Payload:   json.RawMessage(row.Get("payload").String()),
					Timestamp: parseTime(row.Get("timestamp").String()),
				}
				select {
				case ch <- evt:
					since = seq
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return ch, nil
}

func (p *Provider) ActiveClaims(ctx context.Context, namespaceID string, resourcePattern string) ([]hearsay.Claim, error) {
	msgs, err := p.Query(ctx, namespaceID, hearsay.QueryOpts{
		Types: []hearsay.MessageType{hearsay.MsgClaim, hearsay.MsgRelease, hearsay.MsgTransfer},
	})
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
	results := p.d1All(
		"SELECT offset, payload FROM messages WHERE namespace = ? AND type = 'claim'",
		namespaceID)
	if results.IsNull() {
		return nil
	}
	for i := 0; i < results.Length(); i++ {
		row := results.Index(i)
		offset := row.Get("offset").Int()
		payload := row.Get("payload").String()
		var claim hearsay.Claim
		if err := json.Unmarshal([]byte(payload), &claim); err != nil {
			continue
		}
		if claim.CreatedAt.Add(time.Duration(claim.TTLSeconds) * time.Second).Before(before) {
			p.d1Exec("DELETE FROM messages WHERE namespace = ? AND offset = ?", namespaceID, offset)
		}
	}
	return nil
}

func (p *Provider) SendMessage(ctx context.Context, namespace string, msg hearsay.MailboxMessage) error {
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	if msg.ExpiresAt.IsZero() {
		msg.ExpiresAt = msg.CreatedAt.Add(300 * time.Second)
	}
	return p.d1Exec(
		`INSERT INTO mailbox_messages (message_id, namespace, from_agent, to_agent, message_type, content, related_claim_id, read, archived, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.MessageID, namespace, msg.From, msg.To, msg.Type, msg.Content, msg.RelatedClaimID,
		msg.Read, msg.Archived, msg.CreatedAt, msg.ExpiresAt)
}

func (p *Provider) GetMailbox(ctx context.Context, namespace string, agentID string, opts hearsay.MailboxQueryOpts) ([]hearsay.MailboxMessage, error) {
	query := `SELECT message_id, from_agent, to_agent, message_type, content, related_claim_id, read, archived, created_at, expires_at
			  FROM mailbox_messages
			  WHERE namespace = ? AND (to_agent = ? OR to_agent = ?) AND archived = FALSE`
	args := []any{namespace, agentID, hearsay.MailboxToBroadcast}

	if opts.Unread {
		query += " AND read = FALSE"
	}
	query += " ORDER BY created_at DESC"
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}

	results := p.d1All(query, args...)
	if results.IsNull() {
		return nil, nil
	}

	var msgs []hearsay.MailboxMessage
	for i := 0; i < results.Length(); i++ {
		row := results.Index(i)
		msgs = append(msgs, hearsay.MailboxMessage{
			MessageID:      row.Get("message_id").String(),
			From:           row.Get("from_agent").String(),
			To:             row.Get("to_agent").String(),
			Type:           hearsay.MailboxType(row.Get("message_type").String()),
			Content:        row.Get("content").String(),
			RelatedClaimID: row.Get("related_claim_id").String(),
			Read:           row.Get("read").Bool(),
			Archived:       row.Get("archived").Bool(),
			CreatedAt:      parseTime(row.Get("created_at").String()),
			ExpiresAt:      parseTime(row.Get("expires_at").String()),
		})
	}
	return msgs, nil
}

func (p *Provider) MarkRead(ctx context.Context, namespace string, messageID string) error {
	return p.d1Exec(
		"UPDATE mailbox_messages SET read = TRUE WHERE namespace = ? AND message_id = ?",
		namespace, messageID)
}

func (p *Provider) ArchiveMessage(ctx context.Context, namespace string, messageID string) error {
	return p.d1Exec(
		"UPDATE mailbox_messages SET archived = TRUE WHERE namespace = ? AND message_id = ?",
		namespace, messageID)
}

func (p *Provider) ExpireMessages(ctx context.Context, namespace string, before time.Time) error {
	return p.d1Exec(
		"DELETE FROM mailbox_messages WHERE namespace = ? AND expires_at < ?",
		namespace, before)
}

func (p *Provider) CreateTask(ctx context.Context, namespace string, task *hearsay.A2ATask) error {
	return p.d1Exec(
		`INSERT INTO a2a_tasks (id, session_id, state, status_message, status_time, claim_id, namespace, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.SessionID, task.State, string(task.StatusMsg), task.StatusTime, task.ClaimID, namespace, string(task.Metadata), task.CreatedAt)
}

func (p *Provider) GetTask(ctx context.Context, namespace string, taskID string) (*hearsay.A2ATask, error) {
	row := p.d1First(
		`SELECT id, session_id, state, status_message, status_time, claim_id, namespace, metadata, created_at
		 FROM a2a_tasks WHERE namespace = ? AND id = ?`, namespace, taskID)
	if row.IsNull() {
		return nil, fmt.Errorf("task %s not found", taskID)
	}
	return &hearsay.A2ATask{
		ID:         row.Get("id").String(),
		SessionID:  row.Get("session_id").String(),
		State:      row.Get("state").String(),
		StatusMsg:  json.RawMessage(row.Get("status_message").String()),
		StatusTime: parseTime(row.Get("status_time").String()),
		ClaimID:    row.Get("claim_id").String(),
		Namespace:  row.Get("namespace").String(),
		Metadata:   json.RawMessage(row.Get("metadata").String()),
		CreatedAt:  parseTime(row.Get("created_at").String()),
	}, nil
}

func (p *Provider) UpdateTask(ctx context.Context, namespace string, task *hearsay.A2ATask) error {
	return p.d1Exec(
		`UPDATE a2a_tasks SET session_id = ?, state = ?, status_message = ?, status_time = ?, claim_id = ?, metadata = ?
		 WHERE namespace = ? AND id = ?`,
		task.SessionID, task.State, string(task.StatusMsg), task.StatusTime, task.ClaimID, string(task.Metadata), namespace, task.ID)
}

func (p *Provider) AppendTaskHistory(ctx context.Context, namespace string, taskID string, seq int, msg hearsay.A2AMessage) error {
	return p.d1Exec(
		`INSERT INTO a2a_task_history (task_id, seq, role, parts, metadata, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		taskID, seq, msg.Role, string(msg.Parts), string(msg.Metadata), time.Now())
}

func (p *Provider) GetTaskHistory(ctx context.Context, namespace string, taskID string, limit int) ([]hearsay.A2AMessage, error) {
	query := `SELECT role, parts, metadata FROM a2a_task_history WHERE task_id = ? ORDER BY seq`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	results := p.d1All(query, taskID)
	if results.IsNull() {
		return nil, nil
	}
	var out []hearsay.A2AMessage
	for i := 0; i < results.Length(); i++ {
		row := results.Index(i)
		out = append(out, hearsay.A2AMessage{
			Role:     row.Get("role").String(),
			Parts:    json.RawMessage(row.Get("parts").String()),
			Metadata: json.RawMessage(row.Get("metadata").String()),
		})
	}
	return out, nil
}

func (p *Provider) CreateArtifact(ctx context.Context, namespace string, taskID string, art hearsay.A2AArtifact) error {
	return p.d1Exec(
		`INSERT INTO a2a_artifacts (task_id, name, description, parts, index_num, append, last_chunk, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, art.Name, art.Description, string(art.Parts), art.Index, art.Append, art.LastChunk, string(art.Metadata), time.Now())
}

func (p *Provider) GetArtifacts(ctx context.Context, namespace string, taskID string) ([]hearsay.A2AArtifact, error) {
	results := p.d1All(
		`SELECT name, description, parts, index_num, append, last_chunk, metadata FROM a2a_artifacts WHERE task_id = ? ORDER BY name, index_num`,
		taskID)
	if results.IsNull() {
		return nil, nil
	}
	var out []hearsay.A2AArtifact
	for i := 0; i < results.Length(); i++ {
		row := results.Index(i)
		idx := 0
		if !row.Get("index_num").IsNull() {
			idx = row.Get("index_num").Int()
		}
		out = append(out, hearsay.A2AArtifact{
			Name:        row.Get("name").String(),
			Description: row.Get("description").String(),
			Parts:       json.RawMessage(row.Get("parts").String()),
			Index:       idx,
			Append:      row.Get("append").Bool(),
			LastChunk:   row.Get("last_chunk").Bool(),
			Metadata:    json.RawMessage(row.Get("metadata").String()),
		})
	}
	return out, nil
}

func (p *Provider) AppendAudit(ctx context.Context, namespaceID string, events []hearsay.AuditEvent) error {
	for _, e := range events {
		if err := p.d1Exec(
			`INSERT INTO audit_events (namespace, timestamp, level, event_type, agent_id, resource_uri, outcome, metadata)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			namespaceID, e.Timestamp, e.Level, e.EventType, e.AgentID, e.ResourceURI, e.Outcome, e.Metadata); err != nil {
			return err
		}
	}
	return nil
}

func msgTypeStringToEvent(t string) hearsay.EventType {
	switch hearsay.MessageType(t) {
	case hearsay.MsgClaim:
		return hearsay.EventClaim
	case hearsay.MsgRelease:
		return hearsay.EventRelease
	case hearsay.MsgHeartbeat:
		return hearsay.EventHeartbeat
	default:
		return hearsay.EventType(t)
	}
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, _ = time.Parse("2006-01-02 15:04:05", s)
	}
	return t
}