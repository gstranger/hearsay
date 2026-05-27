//go:build !wasm

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gstranger/hearsay/pkg/hearsay"
	_ "modernc.org/sqlite"
)

type Provider struct {
	db *sql.DB
}

func New(path string) (*Provider, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	p := &Provider{db: db}
	if err := p.migrate(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Provider) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS namespaces (
	id TEXT PRIMARY KEY,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS messages (
	namespace TEXT NOT NULL,
	offset INTEGER NOT NULL,
	type TEXT NOT NULL,
	agent_id TEXT,
	payload TEXT,
	timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (namespace, offset)
);

CREATE INDEX IF NOT EXISTS idx_messages_type ON messages(namespace, type);
CREATE INDEX IF NOT EXISTS idx_messages_agent ON messages(namespace, agent_id);

CREATE TABLE IF NOT EXISTS mailbox_messages (
    message_id       TEXT PRIMARY KEY,
    namespace        TEXT NOT NULL,
    from_agent       TEXT NOT NULL,
    to_agent         TEXT NOT NULL,
    message_type     TEXT NOT NULL,
    content          TEXT NOT NULL,
    related_claim_id TEXT,
    read             BOOLEAN DEFAULT FALSE,
    archived         BOOLEAN DEFAULT FALSE,
    created_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at       DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_mailbox_inbox ON mailbox_messages(namespace, to_agent, read, archived);
CREATE INDEX IF NOT EXISTS idx_mailbox_claim ON mailbox_messages(related_claim_id);
CREATE INDEX IF NOT EXISTS idx_mailbox_expires ON mailbox_messages(expires_at);

CREATE TABLE IF NOT EXISTS a2a_tasks (
    id              TEXT PRIMARY KEY,
    session_id      TEXT,
    state           TEXT NOT NULL,
    status_message  TEXT,
    status_time     DATETIME NOT NULL,
    claim_id        TEXT,
    namespace       TEXT NOT NULL,
    metadata        TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_a2a_tasks_ns ON a2a_tasks(namespace);

CREATE TABLE IF NOT EXISTS a2a_task_history (
    task_id     TEXT NOT NULL,
    seq         INTEGER NOT NULL,
    role        TEXT NOT NULL,
    parts       TEXT NOT NULL,
    metadata    TEXT,
    timestamp   DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, seq)
);

CREATE TABLE IF NOT EXISTS a2a_artifacts (
    task_id     TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT,
    parts       TEXT NOT NULL,
    index_num   INTEGER,
    append      BOOLEAN DEFAULT FALSE,
    last_chunk  BOOLEAN DEFAULT FALSE,
    metadata    TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, name, index_num)
);

CREATE TABLE IF NOT EXISTS audit_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace TEXT NOT NULL,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    level TEXT NOT NULL,
    event_type TEXT NOT NULL,
    agent_id TEXT,
    resource_uri TEXT,
    outcome TEXT,
    metadata TEXT
);

CREATE INDEX IF NOT EXISTS idx_audit_namespace ON audit_events(namespace);
CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_events(namespace, timestamp);
`
	_, err := p.db.Exec(schema)
	if err != nil {
		return err
	}

	// Migration: add seq column for SSE sequence numbers
	_, _ = p.db.Exec(`ALTER TABLE messages ADD COLUMN seq INTEGER`)
	return nil
}

func (p *Provider) CreateNamespace(ctx context.Context, ns hearsay.Namespace) error {
	_, err := p.db.ExecContext(ctx, "INSERT INTO namespaces (id, created_at) VALUES (?, ?)", ns.ID, ns.CreatedAt)
	return err
}

func (p *Provider) AppendAudit(ctx context.Context, namespaceID string, events []hearsay.AuditEvent) error {
	for _, e := range events {
		_, err := p.db.ExecContext(ctx,
			`INSERT INTO audit_events (namespace, timestamp, level, event_type, agent_id, resource_uri, outcome, metadata)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			namespaceID, e.Timestamp, e.Level, e.EventType, e.AgentID, e.ResourceURI, e.Outcome, e.Metadata)
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *Provider) DeleteNamespace(ctx context.Context, namespaceID string) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete A2A data first (history → artifacts → tasks)
	if _, err := tx.ExecContext(ctx, "DELETE FROM a2a_task_history WHERE task_id IN (SELECT id FROM a2a_tasks WHERE namespace = ?)", namespaceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM a2a_artifacts WHERE task_id IN (SELECT id FROM a2a_tasks WHERE namespace = ?)", namespaceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM a2a_tasks WHERE namespace = ?", namespaceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE namespace = ?", namespaceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM mailbox_messages WHERE namespace = ?", namespaceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM audit_events WHERE namespace = ?", namespaceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM namespaces WHERE id = ?", namespaceID); err != nil {
		return err
	}

	return tx.Commit()
}

func (p *Provider) Append(ctx context.Context, namespaceID string, msgs []hearsay.Message) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var nextOffset int64
	err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(offset), 0) + 1 FROM messages WHERE namespace = ?", namespaceID).Scan(&nextOffset)
	if err != nil {
		return err
	}

	var nextSeq int64
	err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), 0) + 1 FROM messages WHERE namespace = ?", namespaceID).Scan(&nextSeq)
	if err != nil {
		return err
	}

	stmt, err := tx.PrepareContext(ctx, "INSERT INTO messages (namespace, offset, seq, type, agent_id, payload, timestamp) VALUES (?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i := range msgs {
		if msgs[i].Timestamp.IsZero() {
			msgs[i].Timestamp = time.Now()
		}
		_, err := stmt.ExecContext(ctx, namespaceID, nextOffset, nextSeq, string(msgs[i].Type), msgs[i].AgentID, string(msgs[i].Payload), msgs[i].Timestamp)
		if err != nil {
			return err
		}
		nextOffset++
		nextSeq++
	}

	return tx.Commit()
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

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []hearsay.Message
	for rows.Next() {
		var m hearsay.Message
		var payload string
		if err := rows.Scan(&m.Offset, &m.Type, &m.AgentID, &payload, &m.Timestamp); err != nil {
			return nil, err
		}
		m.Payload = json.RawMessage(payload)
		m.Namespace = namespaceID
		result = append(result, m)
	}
	return result, rows.Err()
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
		rows, err := p.db.QueryContext(ctx,
			"SELECT seq, type, payload, timestamp FROM messages WHERE namespace = ? AND seq > ? ORDER BY seq",
			namespaceID, since)
		if err != nil {
			return
		}
		var currentSeq int64 = since
		for rows.Next() {
			var seq int64
			var msgType, payload string
			var ts time.Time
			if err := rows.Scan(&seq, &msgType, &payload, &ts); err != nil {
				continue
			}
			evt := hearsay.Event{
				Seq:       seq,
				Type:      msgTypeToEvent(msgType),
				Payload:   json.RawMessage(payload),
				Timestamp: ts,
			}
			select {
			case ch <- evt:
				currentSeq = seq
			case <-ctx.Done():
				rows.Close()
				return
			}
		}
		rows.Close()
		since = currentSeq

		// Stream: poll for new events
		for {
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return
			}

			rows, err := p.db.QueryContext(ctx,
				"SELECT seq, type, payload, timestamp FROM messages WHERE namespace = ? AND seq > ? ORDER BY seq",
				namespaceID, since)
			if err != nil {
				continue
			}
			for rows.Next() {
				var seq int64
				var msgType, payload string
				var ts time.Time
				if err := rows.Scan(&seq, &msgType, &payload, &ts); err != nil {
					continue
				}
				evt := hearsay.Event{
					Seq:       seq,
					Type:      msgTypeToEvent(msgType),
					Payload:   json.RawMessage(payload),
					Timestamp: ts,
				}
				select {
				case ch <- evt:
					since = seq
				case <-ctx.Done():
					rows.Close()
					return
				}
			}
			rows.Close()
		}
	}()
	return ch, nil
}

func msgTypeToEvent(t string) hearsay.EventType {
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
	// Query all messages so we see cross-agent releases and compute accurate lastSeen.
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
	// Query all claim messages in the namespace
	rows, err := p.db.QueryContext(ctx,
		"SELECT offset, payload FROM messages WHERE namespace = ? AND type = 'claim'",
		namespaceID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var toDelete []int64
	for rows.Next() {
		var offset int64
		var payload []byte
		if err := rows.Scan(&offset, &payload); err != nil {
			continue
		}
		var claim hearsay.Claim
		if err := json.Unmarshal(payload, &claim); err != nil {
			continue
		}
		if claim.CreatedAt.Add(time.Duration(claim.TTLSeconds) * time.Second).Before(before) {
			toDelete = append(toDelete, offset)
		}
	}
	rows.Close()

	// Delete expired claims and their corresponding release messages
	for _, offset := range toDelete {
		_, err := p.db.ExecContext(ctx,
			"DELETE FROM messages WHERE namespace = ? AND offset = ?",
			namespaceID, offset)
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *Provider) SendMessage(ctx context.Context, namespace string, msg hearsay.MailboxMessage) error {
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	if msg.ExpiresAt.IsZero() {
		msg.ExpiresAt = msg.CreatedAt.Add(300 * time.Second) // 5 min default
	}
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO mailbox_messages (message_id, namespace, from_agent, to_agent, message_type, content, related_claim_id, read, archived, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.MessageID, namespace, msg.From, msg.To, msg.Type, msg.Content, msg.RelatedClaimID,
		msg.Read, msg.Archived, msg.CreatedAt, msg.ExpiresAt)
	return err
}

func (p *Provider) GetMailbox(ctx context.Context, namespace string, agentID string, opts hearsay.MailboxQueryOpts) ([]hearsay.MailboxMessage, error) {
	// TODO: If opts.IncludeClaims is true, expand related_claim_id into a Claim struct.
	// This requires a JOIN with claims data or a secondary query.
	query := `SELECT message_id, from_agent, to_agent, message_type, content, related_claim_id, read, archived, created_at, expires_at
			  FROM mailbox_messages
			  WHERE namespace = ? AND (to_agent = ? OR to_agent = ?) AND archived = FALSE AND expires_at > CURRENT_TIMESTAMP`
	args := []any{namespace, agentID, hearsay.MailboxToBroadcast}

	if opts.Unread {
		query += " AND read = FALSE"
	}

	query += " ORDER BY created_at DESC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []hearsay.MailboxMessage
	for rows.Next() {
		var m hearsay.MailboxMessage
		if err := rows.Scan(&m.MessageID, &m.From, &m.To, &m.Type, &m.Content, &m.RelatedClaimID, &m.Read, &m.Archived, &m.CreatedAt, &m.ExpiresAt); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

func (p *Provider) MarkRead(ctx context.Context, namespace string, messageID string) error {
	_, err := p.db.ExecContext(ctx,
		"UPDATE mailbox_messages SET read = TRUE WHERE namespace = ? AND message_id = ?",
		namespace, messageID)
	return err
}

func (p *Provider) ArchiveMessage(ctx context.Context, namespace string, messageID string) error {
	_, err := p.db.ExecContext(ctx,
		"UPDATE mailbox_messages SET archived = TRUE WHERE namespace = ? AND message_id = ?",
		namespace, messageID)
	return err
}

func (p *Provider) ExpireMessages(ctx context.Context, namespace string, before time.Time) error {
	_, err := p.db.ExecContext(ctx,
		"DELETE FROM mailbox_messages WHERE namespace = ? AND expires_at < ?",
		namespace, before)
	return err
}

func (p *Provider) CreateTask(ctx context.Context, namespace string, task *hearsay.A2ATask) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO a2a_tasks (id, session_id, state, status_message, status_time, claim_id, namespace, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.SessionID, task.State, string(task.StatusMsg), task.StatusTime, task.ClaimID, namespace, string(task.Metadata), task.CreatedAt)
	return err
}

func (p *Provider) GetTask(ctx context.Context, namespace string, taskID string) (*hearsay.A2ATask, error) {
	var t hearsay.A2ATask
	var statusMsg, metadata string
	row := p.db.QueryRowContext(ctx,
		`SELECT id, session_id, state, status_message, status_time, claim_id, namespace, metadata, created_at
		 FROM a2a_tasks WHERE namespace = ? AND id = ?`, namespace, taskID)
	err := row.Scan(&t.ID, &t.SessionID, &t.State, &statusMsg, &t.StatusTime, &t.ClaimID, &t.Namespace, &metadata, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	t.StatusMsg = json.RawMessage(statusMsg)
	t.Metadata = json.RawMessage(metadata)
	return &t, nil
}

func (p *Provider) UpdateTask(ctx context.Context, namespace string, task *hearsay.A2ATask) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE a2a_tasks SET session_id = ?, state = ?, status_message = ?, status_time = ?, claim_id = ?, metadata = ?
		 WHERE namespace = ? AND id = ?`,
		task.SessionID, task.State, string(task.StatusMsg), task.StatusTime, task.ClaimID, string(task.Metadata), namespace, task.ID)
	return err
}

func (p *Provider) AppendTaskHistory(ctx context.Context, namespace string, taskID string, seq int, msg hearsay.A2AMessage) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO a2a_task_history (task_id, seq, role, parts, metadata, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		taskID, seq, msg.Role, string(msg.Parts), string(msg.Metadata), time.Now())
	return err
}

func (p *Provider) GetTaskHistory(ctx context.Context, namespace string, taskID string, limit int) ([]hearsay.A2AMessage, error) {
	query := `SELECT role, parts, metadata FROM a2a_task_history WHERE task_id = ? ORDER BY seq`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := p.db.QueryContext(ctx, query, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hearsay.A2AMessage
	for rows.Next() {
		var m hearsay.A2AMessage
		var parts, metadata string
		if err := rows.Scan(&m.Role, &parts, &metadata); err != nil {
			return nil, err
		}
		m.Parts = json.RawMessage(parts)
		m.Metadata = json.RawMessage(metadata)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (p *Provider) CreateArtifact(ctx context.Context, namespace string, taskID string, art hearsay.A2AArtifact) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO a2a_artifacts (task_id, name, description, parts, index_num, append, last_chunk, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, art.Name, art.Description, string(art.Parts), art.Index, art.Append, art.LastChunk, string(art.Metadata), time.Now())
	return err
}

func (p *Provider) GetArtifacts(ctx context.Context, namespace string, taskID string) ([]hearsay.A2AArtifact, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT name, description, parts, index_num, append, last_chunk, metadata FROM a2a_artifacts WHERE task_id = ? ORDER BY name, index_num`,
		taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hearsay.A2AArtifact
	for rows.Next() {
		var a hearsay.A2AArtifact
		var parts, metadata string
		if err := rows.Scan(&a.Name, &a.Description, &parts, &a.Index, &a.Append, &a.LastChunk, &metadata); err != nil {
			return nil, err
		}
		a.Parts = json.RawMessage(parts)
		a.Metadata = json.RawMessage(metadata)
		out = append(out, a)
	}
	return out, rows.Err()
}
