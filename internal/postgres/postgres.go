package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"github.com/thunder/agentstate/pkg/agentstate"
)

type Provider struct {
	db *sql.DB
}

func New(dsn string) (*Provider, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	p := &Provider{db: db}
	if err := p.migrate(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Provider) migrate() error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS namespaces (
			id TEXT PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			"offset" BIGSERIAL PRIMARY KEY,
			type TEXT NOT NULL,
			namespace TEXT NOT NULL,
			agent_id TEXT,
			payload JSONB,
			timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_ns ON messages(namespace)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_ts ON messages(timestamp)`,
		`CREATE TABLE IF NOT EXISTS mailbox_messages (
			message_id       TEXT PRIMARY KEY,
			namespace        TEXT NOT NULL,
			from_agent       TEXT NOT NULL,
			to_agent         TEXT NOT NULL,
			message_type     TEXT NOT NULL,
			content          TEXT NOT NULL,
			related_claim_id TEXT,
			read             BOOLEAN DEFAULT FALSE,
			archived         BOOLEAN DEFAULT FALSE,
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at       TIMESTAMPTZ NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mailbox_inbox ON mailbox_messages(namespace, to_agent, read, archived)`,
		`CREATE INDEX IF NOT EXISTS idx_mailbox_claim ON mailbox_messages(related_claim_id)`,
		`CREATE INDEX IF NOT EXISTS idx_mailbox_expires ON mailbox_messages(expires_at)`,
		`CREATE TABLE IF NOT EXISTS a2a_tasks (
			id TEXT PRIMARY KEY,
			session_id TEXT,
			state TEXT NOT NULL,
			status_message TEXT,
			status_time TIMESTAMPTZ NOT NULL,
			claim_id TEXT,
			namespace TEXT NOT NULL,
			metadata TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_a2a_tasks_ns ON a2a_tasks(namespace)`,
		`CREATE TABLE IF NOT EXISTS a2a_task_history (
			task_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			role TEXT NOT NULL,
			parts TEXT NOT NULL,
			metadata TEXT,
			timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (task_id, seq)
		)`,
		`CREATE TABLE IF NOT EXISTS a2a_artifacts (
			task_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT,
			parts TEXT NOT NULL,
			index_num INTEGER,
			append BOOLEAN DEFAULT FALSE,
			last_chunk BOOLEAN DEFAULT FALSE,
			metadata TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (task_id, name, index_num)
		)`,
	}
	for _, s := range schema {
		if _, err := p.db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (p *Provider) CreateNamespace(ctx context.Context, ns agentstate.Namespace) error {
	_, err := p.db.ExecContext(ctx,
		"INSERT INTO namespaces (id, created_at) VALUES ($1, $2) ON CONFLICT DO NOTHING",
		ns.ID, ns.CreatedAt)
	return err
}

func (p *Provider) DeleteNamespace(ctx context.Context, namespaceID string) error {
	if _, err := p.db.ExecContext(ctx, "DELETE FROM a2a_task_history WHERE task_id IN (SELECT id FROM a2a_tasks WHERE namespace = $1)", namespaceID); err != nil {
		return err
	}
	if _, err := p.db.ExecContext(ctx, "DELETE FROM a2a_artifacts WHERE task_id IN (SELECT id FROM a2a_tasks WHERE namespace = $1)", namespaceID); err != nil {
		return err
	}
	if _, err := p.db.ExecContext(ctx, "DELETE FROM a2a_tasks WHERE namespace = $1", namespaceID); err != nil {
		return err
	}
	if _, err := p.db.ExecContext(ctx, "DELETE FROM messages WHERE namespace = $1", namespaceID); err != nil {
		return err
	}
	if _, err := p.db.ExecContext(ctx, "DELETE FROM mailbox_messages WHERE namespace = $1", namespaceID); err != nil {
		return err
	}
	_, err := p.db.ExecContext(ctx, "DELETE FROM namespaces WHERE id = $1", namespaceID)
	return err
}

func (p *Provider) Append(ctx context.Context, namespaceID string, msgs []agentstate.Message) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i := range msgs {
		if msgs[i].Timestamp.IsZero() {
			msgs[i].Timestamp = time.Now()
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO messages (type, namespace, agent_id, payload, timestamp) VALUES ($1, $2, $3, $4, $5)`,
			msgs[i].Type, namespaceID, msgs[i].AgentID, string(msgs[i].Payload), msgs[i].Timestamp,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (p *Provider) Query(ctx context.Context, namespaceID string, opts agentstate.QueryOpts) ([]agentstate.Message, error) {
	whereClauses := []string{"namespace = $1"}
	args := []interface{}{namespaceID}
	if opts.AgentID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("agent_id = $%d", len(args)+1))
		args = append(args, opts.AgentID)
	}
	if opts.Since > 0 {
		whereClauses = append(whereClauses, fmt.Sprintf(`"offset" > $%d`, len(args)+1))
		args = append(args, int64(opts.Since))
	}
	if len(opts.Types) > 0 {
		placeholders := make([]string, len(opts.Types))
		for i, t := range opts.Types {
			placeholders[i] = fmt.Sprintf("$%d", len(args)+1)
			args = append(args, string(t))
		}
		whereClauses = append(whereClauses, "type IN ("+strings.Join(placeholders, ",")+")")
	}

	query := `SELECT "offset", type, namespace, agent_id, payload, timestamp FROM messages WHERE ` +
		strings.Join(whereClauses, " AND ") +
		` ORDER BY "offset"`
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", len(args)+1)
		args = append(args, opts.Limit)
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []agentstate.Message
	for rows.Next() {
		var m agentstate.Message
		var payload []byte
		if err := rows.Scan(&m.Offset, &m.Type, &m.Namespace, &m.AgentID, &payload, &m.Timestamp); err != nil {
			return nil, err
		}
		m.Payload = json.RawMessage(payload)
		result = append(result, m)
	}
	return result, rows.Err()
}

func (p *Provider) Subscribe(ctx context.Context, namespaceID string, from agentstate.Offset) (<-chan agentstate.Message, error) {
	ch := make(chan agentstate.Message, 100)
	go func() {
		defer close(ch)
		current := from
		for {
			msgs, err := p.Query(ctx, namespaceID, agentstate.QueryOpts{Since: current})
			if err != nil {
				return
			}
			for _, m := range msgs {
				select {
				case ch <- m:
					current = agentstate.Offset(m.Offset)
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
	msgs, err := p.Query(ctx, namespaceID, agentstate.QueryOpts{
		Types: []agentstate.MessageType{agentstate.MsgClaim, agentstate.MsgRelease, agentstate.MsgTransfer},
	})
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
	return nil
}

func (p *Provider) SendMessage(ctx context.Context, namespace string, msg agentstate.MailboxMessage) error {
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	if msg.ExpiresAt.IsZero() {
		msg.ExpiresAt = msg.CreatedAt.Add(300 * time.Second) // 5 min default
	}
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO mailbox_messages (message_id, namespace, from_agent, to_agent, message_type, content, related_claim_id, read, archived, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		msg.MessageID, namespace, msg.From, msg.To, msg.Type, msg.Content, msg.RelatedClaimID,
		msg.Read, msg.Archived, msg.CreatedAt, msg.ExpiresAt)
	return err
}

func (p *Provider) GetMailbox(ctx context.Context, namespace string, agentID string, opts agentstate.MailboxQueryOpts) ([]agentstate.MailboxMessage, error) {
	// TODO: If opts.IncludeClaims is true, expand related_claim_id into a Claim struct.
	// This requires a JOIN with claims data or a secondary query.
	query := `SELECT message_id, from_agent, to_agent, message_type, content, related_claim_id, read, archived, created_at, expires_at
			  FROM mailbox_messages
			  WHERE namespace = $1 AND (to_agent = $2 OR to_agent = $3) AND archived = FALSE AND expires_at > NOW()`
	args := []any{namespace, agentID, agentstate.MailboxToBroadcast}

	if opts.Unread {
		query += " AND read = FALSE"
	}

	query += " ORDER BY created_at DESC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", len(args)+1)
		args = append(args, opts.Limit)
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []agentstate.MailboxMessage
	for rows.Next() {
		var m agentstate.MailboxMessage
		if err := rows.Scan(&m.MessageID, &m.From, &m.To, &m.Type, &m.Content, &m.RelatedClaimID, &m.Read, &m.Archived, &m.CreatedAt, &m.ExpiresAt); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

func (p *Provider) MarkRead(ctx context.Context, namespace string, messageID string) error {
	_, err := p.db.ExecContext(ctx,
		"UPDATE mailbox_messages SET read = TRUE WHERE namespace = $1 AND message_id = $2",
		namespace, messageID)
	return err
}

func (p *Provider) ArchiveMessage(ctx context.Context, namespace string, messageID string) error {
	_, err := p.db.ExecContext(ctx,
		"UPDATE mailbox_messages SET archived = TRUE WHERE namespace = $1 AND message_id = $2",
		namespace, messageID)
	return err
}

func (p *Provider) ExpireMessages(ctx context.Context, namespace string, before time.Time) error {
	_, err := p.db.ExecContext(ctx,
		"DELETE FROM mailbox_messages WHERE namespace = $1 AND expires_at < $2",
		namespace, before)
	return err
}

func (p *Provider) CreateTask(ctx context.Context, namespace string, task *agentstate.A2ATask) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO a2a_tasks (id, session_id, state, status_message, status_time, claim_id, namespace, metadata, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		task.ID, task.SessionID, task.State, string(task.StatusMsg), task.StatusTime, task.ClaimID, namespace, string(task.Metadata), task.CreatedAt)
	return err
}

func (p *Provider) GetTask(ctx context.Context, namespace string, taskID string) (*agentstate.A2ATask, error) {
	var t agentstate.A2ATask
	var statusMsg, metadata string
	row := p.db.QueryRowContext(ctx,
		`SELECT id, session_id, state, status_message, status_time, claim_id, namespace, metadata, created_at
		 FROM a2a_tasks WHERE namespace = $1 AND id = $2`, namespace, taskID)
	err := row.Scan(&t.ID, &t.SessionID, &t.State, &statusMsg, &t.StatusTime, &t.ClaimID, &t.Namespace, &metadata, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	t.StatusMsg = json.RawMessage(statusMsg)
	t.Metadata = json.RawMessage(metadata)
	return &t, nil
}

func (p *Provider) UpdateTask(ctx context.Context, namespace string, task *agentstate.A2ATask) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE a2a_tasks SET session_id = $1, state = $2, status_message = $3, status_time = $4, claim_id = $5, metadata = $6
		 WHERE namespace = $7 AND id = $8`,
		task.SessionID, task.State, string(task.StatusMsg), task.StatusTime, task.ClaimID, string(task.Metadata), namespace, task.ID)
	return err
}

func (p *Provider) AppendTaskHistory(ctx context.Context, namespace string, taskID string, seq int, msg agentstate.A2AMessage) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO a2a_task_history (task_id, seq, role, parts, metadata, timestamp)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		taskID, seq, msg.Role, string(msg.Parts), string(msg.Metadata), time.Now())
	return err
}

func (p *Provider) GetTaskHistory(ctx context.Context, namespace string, taskID string, limit int) ([]agentstate.A2AMessage, error) {
	query := `SELECT role, parts, metadata FROM a2a_task_history WHERE task_id = $1 ORDER BY seq`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := p.db.QueryContext(ctx, query, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []agentstate.A2AMessage
	for rows.Next() {
		var m agentstate.A2AMessage
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

func (p *Provider) CreateArtifact(ctx context.Context, namespace string, taskID string, art agentstate.A2AArtifact) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO a2a_artifacts (task_id, name, description, parts, index_num, append, last_chunk, metadata, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		taskID, art.Name, art.Description, string(art.Parts), art.Index, art.Append, art.LastChunk, string(art.Metadata), time.Now())
	return err
}

func (p *Provider) GetArtifacts(ctx context.Context, namespace string, taskID string) ([]agentstate.A2AArtifact, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT name, description, parts, index_num, append, last_chunk, metadata FROM a2a_artifacts WHERE task_id = $1 ORDER BY name, index_num`,
		taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []agentstate.A2AArtifact
	for rows.Next() {
		var a agentstate.A2AArtifact
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
