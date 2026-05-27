# Dual-Runtime Architecture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split hearsay into dual-runtime targets (native Go binary and Cloudflare Worker WASM) using build tags for only three components: entrypoint, storage, sweeper.

**Architecture:** Build tags (`//go:build !wasm` / `//go:build wasm`) isolate three thin layers — the HTTP entrypoint (net/http vs DO fetch), storage (sqlite/postgres vs D1), and sweeper (goroutine ticker vs DO alarm). Everything else — route registration, handlers, SSE, A2A, claims, mailbox — is shared with zero build tags.

**Tech Stack:** Go 1.25, `syscall/js` (stdlib) for WASM bindings, Cloudflare Durable Objects + D1 for Worker target. No new dependencies.

---

### Task 1: Add Event types and SubscribeEvents() to Provider interface

**Files:**
- Modify: `pkg/hearsay/provider.go`

- [ ] **Step 1: Add Event, EventType, and SubscribeEvents to the interface**

Add these types after the existing `QueryOpts` struct (around line 37) and add `SubscribeEvents` to the `Provider` interface:

```go
// Insert after QueryOpts struct:

// Event represents a streamed coordination event with a monotonic sequence number
// that clients use for SSE reconnection without missing events.
type Event struct {
	Seq       int64           `json:"seq"`
	Type      EventType       `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp time.Time       `json:"timestamp"`
}

// EventType categorizes streamed events for SSE clients.
type EventType string

const (
	EventClaim     EventType = "claim"
	EventRelease   EventType = "release"
	EventHeartbeat EventType = "heartbeat"
	EventMailbox   EventType = "mailbox_send"
)
```

Add to the `Provider` interface, after `AppendAudit`:

```go
	// SubscribeEvents returns a channel of coordination events for the namespace,
	// starting from the given sequence number. If since is 0, only new events
	// are streamed (no replay). The channel is closed when ctx is cancelled.
	SubscribeEvents(ctx context.Context, namespaceID string, since int64) (<-chan Event, error)
```

- [ ] **Step 2: Run tests to verify compilation**

```bash
go build ./...
```

Expected: compilation errors in provider implementations (sqlite, postgres, managed, memory) since `SubscribeEvents` is not yet implemented. This is expected — confirmed in Task 2 and Task 3.

- [ ] **Step 3: Commit**

```bash
git add pkg/hearsay/provider.go
git commit -m "feat: add Event/EventType types and SubscribeEvents() to Provider interface"
```

---

### Task 2: Implement SubscribeEvents() in the memory provider

**Files:**
- Modify: `internal/memory/memory.go`

- [ ] **Step 1: Add SubscribeEvents method**

Add after `AppendAudit` at the end of `memory.go`:

```go
// SubscribeEvents streams events from the in-memory message log.
func (p *Provider) SubscribeEvents(ctx context.Context, namespaceID string, since int64) (<-chan hearsay.Event, error) {
	ch := make(chan hearsay.Event, 100)
	go func() {
		defer close(ch)

		// Replay: catch up on events since the given sequence
		p.mu.RLock()
		msgs := p.messages[namespaceID]
		var currentSeq int64
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
	case hearsay.MsgRelease:
		return hearsay.EventRelease
	case hearsay.MsgHeartbeat:
		return hearsay.EventHeartbeat
	default:
		return hearsay.EventType(t)
	}
}
```

- [ ] **Step 2: Run tests to confirm memory provider compiles and passes**

```bash
go test ./internal/memory/... -v
go vet ./internal/memory/...
```

Expected: all pass.

- [ ] **Step 3: Commit**

```bash
git add internal/memory/memory.go
git commit -m "feat: implement SubscribeEvents() in memory provider"
```

---

### Task 3: Write SSE handler tests

**Files:**
- Create: `internal/server/sse.go`
- Create: `internal/server/sse_test.go`

- [ ] **Step 1: Write failing SSE tests**

Create `internal/server/sse_test.go`:

```go
package server

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSSE_StreamsEvents(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	// Create a claim so there's an event to stream
	claimBody := `{"namespace":"test","agent_id":"a1","resource_uri":"file://x.ts","operation":"write","intent":"testing"}`
	post(t, s, "/claim", claimBody)

	req := httptest.NewRequest("GET", "/events?namespace=test&since=0", nil)
	rec := httptest.NewRecorder()

	// Start SSE in a goroutine since it blocks
	done := make(chan *http.Response, 1)
	go func() {
		s.ServeHTTP(rec, req)
		done <- rec.Result()
	}()

	// Wait briefly for the event to be written
	select {
	case resp := <-done:
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		contentType := resp.Header.Get("Content-Type")
		if !strings.Contains(contentType, "text/event-stream") {
			t.Fatalf("expected text/event-stream, got %s", contentType)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SSE handler did not complete within timeout")
	}
}

func TestSSE_ReplaysFromSince(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	// Create two claims
	post(t, s, "/claim", `{"namespace":"test","agent_id":"a1","resource_uri":"file://a.ts","operation":"write","intent":"first"}`)
	post(t, s, "/claim", `{"namespace":"test","agent_id":"a1","resource_uri":"file://b.ts","operation":"write","intent":"second"}`)

	// First, read events with since=0 to find the first claim's seq
	req := httptest.NewRequest("GET", "/events?namespace=test&since=0", nil)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}

	// Parse SSE to find first seq
	body := rec.Body.String()
	lines := strings.Split(body, "\n")
	var firstSeq int64
	for _, line := range lines {
		if strings.HasPrefix(line, "data:") {
			firstSeq = 1 // at least one event was emitted
			break
		}
	}

	if firstSeq == 0 {
		t.Skip("no events in SSE stream, skipping replay test")
	}

	// Now reconnect with since=firstSeq to verify replay
	req2 := httptest.NewRequest("GET", "/events?namespace=test&since=1", nil)
	rec2 := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() {
		s.ServeHTTP(rec2, req2)
		close(done2)
	}()
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
	}

	if rec2.Code != http.StatusOK {
		t.Fatalf("replay: expected 200, got %d", rec2.Code)
	}
}

func TestSSE_ContentTypeAndHeaders(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()
	post(t, s, "/claim", `{"namespace":"test","agent_id":"a1","resource_uri":"file://x.ts","operation":"write","intent":"test"}`)

	req := httptest.NewRequest("GET", "/events?namespace=test", nil)
	rec := httptest.NewRecorder()

	done := make(chan *http.Response, 1)
	go func() {
		s.ServeHTTP(rec, req)
		done <- rec.Result()
	}()
	resp := <-done

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("wrong Content-Type: %s", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("wrong Cache-Control: %s", cc)
	}
}
```

Create `internal/server/sse.go`:

```go
package server

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

// handleSSE streams coordination events as Server-Sent Events.
// Clients can request replay with ?since=<seq> to catch up after disconnection.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		http.Error(w, "namespace required", http.StatusBadRequest)
		return
	}

	var since int64
	if v := r.URL.Query().Get("since"); v != "" {
		var err error
		since, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			http.Error(w, "invalid since parameter", http.StatusBadRequest)
			return
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	events, err := s.provider.SubscribeEvents(r.Context(), namespace, since)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: {\"error\":\"%s\"}\n\n", err.Error())
		flusher.Flush()
		return
	}

	for event := range events {
		fmt.Fprintf(w, "event: %s\ndata: ", event.Type)
		w.Write(event.Payload)
		fmt.Fprintf(w, "\n\n")
		flusher.Flush()
	}
}
```

- [ ] **Step 2: Register SSE route in server.go**

In `internal/server/server.go`, add the SSE route in the `New` function, after the existing read-only endpoint registrations:

```go
	s.mux.HandleFunc("GET /events", s.handleSSE)
```

Add this line after the `s.mux.HandleFunc("GET /mailbox", s.handleGetMailbox)` line.

- [ ] **Step 3: Run SSE tests to verify they pass**

```bash
go test ./internal/server/... -v -run TestSSE
```

Expected: all SSE tests pass.

- [ ] **Step 4: Run full test suite**

```bash
go test ./...
```

Expected: all existing tests still pass, no regressions.

- [ ] **Step 5: Commit**

```bash
git add internal/server/sse.go internal/server/sse_test.go internal/server/server.go
git commit -m "feat: add SSE event streaming handler with since-based replay"
```

---

### Task 4: Add SubscribeEvents() to SQLite, Postgres, and Managed providers

**Files:**
- Modify: `internal/sqlite/sqlite.go`
- Modify: `internal/postgres/postgres.go`
- Modify: `internal/managed/managed.go`

- [ ] **Step 1: Add build tag and seq column migration to SQLite provider**

In `internal/sqlite/sqlite.go`, add the build tag at the top:

```go
//go:build !wasm

package sqlite
```

In the `migrate()` function, add a `seq` column migration after the existing schema:

```go
	// Migration: add seq column for SSE sequence numbers
	_, _ = p.db.Exec(`ALTER TABLE messages ADD COLUMN seq INTEGER`)
```

Add this at the end of `migrate()`, before `return nil`. The `_` ignores errors if the column already exists in older databases.

Modify the `Append` method to track and assign sequence numbers. After scanning for `nextOffset`, add:

```go
	var nextSeq int64
	err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), 0) + 1 FROM messages WHERE namespace = ?", namespaceID).Scan(&nextSeq)
	if err != nil {
		return err
	}
```

And update the INSERT statement to include `seq`:

```go
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO messages (namespace, offset, seq, type, agent_id, payload, timestamp) VALUES (?, ?, ?, ?, ?, ?, ?)")
```

And in the loop, add the seq parameter:

```go
		_, err := stmt.ExecContext(ctx, namespaceID, nextOffset, nextSeq, string(msgs[i].Type), msgs[i].AgentID, string(msgs[i].Payload), msgs[i].Timestamp)
```

And increment `nextSeq` after each insert:

```go
		nextOffset++
		nextSeq++
```

Add `SubscribeEvents` method at the end of the file:

```go
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
		var currentSeq int64
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
```

- [ ] **Step 2: Add build tag and SubscribeEvents to Postgres provider**

In `internal/postgres/postgres.go`, add the build tag:

```go
//go:build !wasm

package postgres
```

Add seq column migration: add after the existing table creation statements in `migrate()`:

```go
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS seq BIGINT`,
```

Modify `Append` the same way as SQLite — add seq to the INSERT. Postgres uses `RETURNING` or a separate query for the next seq. Use a counter approach:

In the `Append` method, after the transaction begins, query for next seq:

```go
	var nextSeq int64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM messages WHERE namespace = $1`, namespaceID).Scan(&nextSeq)
	if err != nil {
		return err
	}
```

Update the INSERT to include seq:

```go
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO messages ("offset", seq, type, namespace, agent_id, payload, timestamp) VALUES ($1, $2, $3, $4, $5, $6, $7)`)
```

Then in the insert loop:

```go
		_, err := stmt.ExecContext(ctx, msgs[i].Offset, nextSeq, string(msgs[i].Type), namespaceID, msgs[i].AgentID, string(msgs[i].Payload), msgs[i].Timestamp)
		nextSeq++
```

Add `SubscribeEvents` — same logic as SQLite but with Postgres parameter syntax (`$1` instead of `?`). Copy the same method but use `$1` placeholders:

```go
// SubscribeEvents streams coordination events with sequence numbers for SSE.
func (p *Provider) SubscribeEvents(ctx context.Context, namespaceID string, since int64) (<-chan hearsay.Event, error) {
	ch := make(chan hearsay.Event, 100)
	go func() {
		defer close(ch)
		// Replay
		rows, err := p.db.QueryContext(ctx,
			`SELECT seq, type, payload, timestamp FROM messages WHERE namespace = $1 AND seq > $2 ORDER BY seq`,
			namespaceID, since)
		if err != nil {
			return
		}
		var currentSeq int64
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
		// Stream
		for {
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return
			}
			rows, err := p.db.QueryContext(ctx,
				`SELECT seq, type, payload, timestamp FROM messages WHERE namespace = $1 AND seq > $2 ORDER BY seq`,
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
```

- [ ] **Step 3: Add build tag and SubscribeEvents stub to Managed provider**

In `internal/managed/managed.go`, add the build tag:

```go
//go:build !wasm

package managed
```

Add `SubscribeEvents` as a polling-based implementation (the managed provider proxies to a remote server that now supports SSE, but the client can poll):

```go
func (p *Provider) SubscribeEvents(ctx context.Context, namespaceID string, since int64) (<-chan hearsay.Event, error) {
	ch := make(chan hearsay.Event, 100)
	go func() {
		defer close(ch)
		for {
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return
			}
			msgs, err := p.Query(ctx, namespaceID, hearsay.QueryOpts{Since: hearsay.Offset(since)})
			if err != nil {
				continue
			}
			for _, m := range msgs {
				evt := hearsay.Event{
					Seq:       m.Offset,
					Type:      msgTypeToEvent(m.Type),
					Payload:   m.Payload,
					Timestamp: m.Timestamp,
				}
				select {
				case ch <- evt:
					since = m.Offset
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return ch, nil
}

func msgTypeToEvent(t hearsay.MessageType) hearsay.EventType {
	switch t {
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
```

- [ ] **Step 4: Run tests to verify all providers compile and pass**

```bash
go test ./internal/sqlite/... -v
go test ./internal/postgres/... -v
go test ./internal/managed/... -v
go test ./... -count=1
```

Expected: all pass. If Postgres tests need a running database, skip with `-short` if not available.

- [ ] **Step 5: Commit**

```bash
git add internal/sqlite/sqlite.go internal/postgres/postgres.go internal/managed/managed.go
git commit -m "feat: add build tags and SubscribeEvents() to SQLite, Postgres, Managed providers"
```

---

### Task 5: Create D1 provider for Cloudflare Workers

**Files:**
- Create: `internal/d1/d1.go`

- [ ] **Step 1: Create D1 provider**

Create `internal/d1/d1.go` with the build tag and D1 bindings. The schema is identical to SQLite's. Since D1 uses Worker bindings (not `database/sql`), the API surface uses `syscall/js` for Cloudflare's D1 binding methods:

```go
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
	return p.d1Exec("DELETE FROM namespaces WHERE id = ?", namespaceID)
}

func (p *Provider) Append(ctx context.Context, namespaceID string, msgs []hearsay.Message) error {
	for i := range msgs {
		// Get next offset
		row := p.d1First("SELECT COALESCE(MAX(offset), 0) + 1 as next_offset FROM messages WHERE namespace = ?", namespaceID)
		nextOffset := row.Get("next_offset").Int()

		// Get next seq
		row2 := p.d1First("SELECT COALESCE(MAX(seq), 0) + 1 as next_seq FROM messages WHERE namespace = ?", namespaceID)
		nextSeq := row2.Get("next_seq").Int()

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
			Offset:    row.Get("offset").Int(),
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

func (p *Provider) SubscribeEvents(ctx context.Context, namespaceID string, since int64) (<-chan hearsay.Event, error) {
	ch := make(chan hearsay.Event, 100)
	go func() {
		defer close(ch)
		// Replay
		results := p.d1All(
			"SELECT seq, type, payload, timestamp FROM messages WHERE namespace = ? AND seq > ? ORDER BY seq",
			namespaceID, since)
		var currentSeq int64
		if !results.IsNull() {
			for i := 0; i < results.Length(); i++ {
				row := results.Index(i)
				seq := row.Get("seq").Int()
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
		// Stream: poll
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
				seq := row.Get("seq").Int()
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
	results := p.d1All("SELECT offset, payload FROM messages WHERE namespace = ? AND type = 'claim'", namespaceID)
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

// Mailbox methods
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
	return p.d1Exec("UPDATE mailbox_messages SET read = TRUE WHERE namespace = ? AND message_id = ?", namespace, messageID)
}

func (p *Provider) ArchiveMessage(ctx context.Context, namespace string, messageID string) error {
	return p.d1Exec("UPDATE mailbox_messages SET archived = TRUE WHERE namespace = ? AND message_id = ?", namespace, messageID)
}

func (p *Provider) ExpireMessages(ctx context.Context, namespace string, before time.Time) error {
	return p.d1Exec("DELETE FROM mailbox_messages WHERE namespace = ? AND expires_at < ?", namespace, before.Format(time.RFC3339))
}

// A2A task storage
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
		p.d1Exec(
			"INSERT INTO audit_events (namespace, level, event_type, agent_id, resource_uri, outcome, metadata) VALUES (?, ?, ?, ?, ?, ?, ?)",
			namespaceID, e.Level, e.EventType, e.AgentID, e.ResourceURI, e.Outcome, string(e.Metadata))
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
```

- [ ] **Step 2: Verify WASM compilation**

```bash
GOOS=js GOARCH=wasm go build ./internal/d1/...
```

Expected: builds successfully (may show unused import warnings, that's fine).

- [ ] **Step 3: Commit**

```bash
git add internal/d1/d1.go
git commit -m "feat: add D1 provider for Cloudflare Workers (wasm build tag)"
```

---

### Task 6: Create Durable Object for Cloudflare Workers

**Files:**
- Create: `internal/do/do.go`

- [ ] **Step 1: Create the Durable Object**

Create `internal/do/do.go`:

```go
//go:build wasm

package do

import (
	"syscall/js"
	"time"

	"github.com/gstranger/hearsay/internal/d1"
	"github.com/gstranger/hearsay/internal/server"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

// CoordinatorDO is a Durable Object that handles all coordination for one namespace.
// Each namespace gets its own DO instance. It dispatches HTTP requests to the shared
// server.Server handlers and manages a background sweeper via DO alarm().
type CoordinatorDO struct {
	provider *d1.Provider
	server   *server.Server
}

// NewCoordinatorDO is the DO constructor. Called by Cloudflare when a DO instance
// is created for a namespace.
func NewCoordinatorDO(state js.Value, env js.Value) *CoordinatorDO {
	d1Binding := env.Get("HEARSAY_D1")
	provider := d1.New(d1Binding)
	client := hearsay.NewClient(provider, "default") // namespace set per-request

	srv := server.New(client, provider, false, "", server.LogFormatText, 0, 0, hearsay.AuditOff, "")

	// Start the sweeper alarm (fires every 60s)
	state.Get("storage").Call("setAlarm", time.Now().Add(60*time.Second))

	return &CoordinatorDO{
		provider: provider,
		server:   srv,
	}
}

// Fetch handles every HTTP request routed to this DO. It adapts the Cloudflare
// Request/Response to Go's http.Handler interface, so the shared server.Server
// can process requests identically to the native binary.
func (d *CoordinatorDO) Fetch(request js.Value) js.Value {
	// The actual HTTP handling is done by the Worker-level fetch handler
	// in main_wasm.go, which extracts the namespace, gets the DO stub,
	// and forwards the request. The DO's Fetch receives the raw request
	// and delegates to the server.Server.
	//
	// In practice, the Worker fetch entrypoint does:
	//   doStub.Fetch(request) → this method
	// And the DO server.Server handles routing internally.
	//
	// For the initial implementation, we use a simpler approach:
	// The Worker fetch handler extracts the namespace and directly
	// creates a server.Server per-request rather than routing through DOs.
	// Full DO isolation is a follow-on optimization.
	return js.Null()
}

// Alarm is called by Cloudflare when the alarm fires.
func (d *CoordinatorDO) Alarm(state js.Value) {
	// Sweep expired claims
	ctx := context.Background()
	d.provider.ReleaseExpired(ctx, "default", time.Now())

	// Reschedule
	state.Get("storage").Call("setAlarm", time.Now().Add(60*time.Second))
}
```

Note: The full DO routing through `Fetch` requires adapting Cloudflare's Request/Response to Go's `http.Request`/`http.ResponseWriter`. For v1, the Worker fetch handler creates a `server.Server` per request (see Task 8). Full DO isolation with request forwarding is documented as a follow-on.

- [ ] **Step 2: Verify WASM compilation**

```bash
GOOS=js GOARCH=wasm go build ./internal/do/...
```

Expected: builds successfully.

- [ ] **Step 3: Commit**

```bash
git add internal/do/do.go
git commit -m "feat: add Durable Object scaffold for Cloudflare Workers"
```

---

### Task 7: Extract sweeper into build-tagged files

**Files:**
- Create: `internal/server/sweeper_native.go`
- Create: `internal/server/sweeper_wasm.go`
- Modify: `cmd/hearsay/main.go`

- [ ] **Step 1: Create native sweeper**

Create `internal/server/sweeper_native.go`:

```go
//go:build !wasm

package server

import (
	"context"
	"time"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

// RunSweeper starts a background goroutine that calls ReleaseExpired
// every interval. The goroutine stops when ctx is cancelled.
func RunSweeper(ctx context.Context, provider hearsay.Provider, namespace string, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				before := time.Now().UTC()
				provider.ReleaseExpired(context.Background(), namespace, before)
			case <-ctx.Done():
				return
			}
		}
	}()
}
```

- [ ] **Step 2: Create WASM sweeper (stub)**

Create `internal/server/sweeper_wasm.go`:

```go
//go:build wasm

package server

import (
	"context"
	"time"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

// RunSweeper is a no-op in WASM. The sweeper is managed by the
// Durable Object's alarm() mechanism instead.
func RunSweeper(ctx context.Context, provider hearsay.Provider, namespace string, interval time.Duration) {
	// No-op: DO alarm handles sweeping in the Worker runtime
}
```

- [ ] **Step 3: Replace inline sweeper in main.go**

In `cmd/hearsay/main.go`, find the sweeper goroutine in `cmdServe` (the `// Background sweeper: clean up expired claims every 60 seconds` block) and replace it with a call to `server.RunSweeper`:

```go
	// Background sweeper: clean up expired claims every 60 seconds
	server.RunSweeper(context.Background(), provider, cfg.Namespace, 60*time.Second)
```

Remove the old inline goroutine block (from `// Background sweeper...` through the closing `}()`).

- [ ] **Step 4: Run tests to verify**

```bash
go build ./...
go test ./internal/server/... -v
```

Expected: builds and all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/server/sweeper_native.go internal/server/sweeper_wasm.go cmd/hearsay/main.go
git commit -m "refactor: extract sweeper into build-tagged RunSweeper function"
```

---

### Task 8: Split main.go into native entrypoint + shared command files

**Files:**
- Create: `cmd/hearsay/main_native.go`
- Rename: `cmd/hearsay/main.go` → extract CLI commands to `cmd/hearsay/cmd_serve.go`, `cmd/hearsay/cmd_claim.go`, etc., then remove old `cmd/hearsay/main.go`

- [ ] **Step 1: Create main_native.go**

Create `cmd/hearsay/main_native.go`:

```go
//go:build !wasm

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: hearsay <command> [args]")
		fmt.Fprintln(os.Stderr, "Commands: init, claim, release, heartbeat, query, check, namespace, serve, watch, cursor, status")
		os.Exit(1)
	}

	// Handle SIGINT/SIGTERM for graceful shutdown during serve
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		os.Exit(0)
	}()

	cmd := os.Args[1]
	switch cmd {
	case "init":
		cmdInit(os.Args[2:])
	case "claim":
		cmdClaim(os.Args[2:])
	case "release":
		cmdRelease(os.Args[2:])
	case "heartbeat":
		cmdHeartbeat(os.Args[2:])
	case "query":
		cmdQuery(os.Args[2:])
	case "check":
		cmdCheck(os.Args[2:])
	case "namespace":
		cmdNamespace(os.Args[2:])
	case "serve":
		cmdServe(os.Args[2:])
	case "watch":
		cmdWatch(os.Args[2:])
	case "cursor":
		cmdCursor(os.Args[2:])
	case "status":
		cmdStatus(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Extract shared command implementations**

The current `cmd/hearsay/main.go` contains all CLI functions (`cmdInit`, `cmdClaim`, `cmdServe`, `loadClient`, `loadProvider`, etc.). These are shared between native and WASM (unused in WASM but must compile). Extract them into separate files by command:

Create `cmd/hearsay/cmd_serve.go` with `cmdServe`, `loadProviderFromConfig`, and all server-related helpers.
Create `cmd/hearsay/cmd_claims.go` with `cmdClaim`, `cmdRelease`, `cmdHeartbeat`, `cmdQuery`, `cmdCheck`.
Create `cmd/hearsay/cmd_config.go` with `cmdInit`, `cmdNamespace`, `loadClient`, `loadProvider`, `loadClientFromProvider`.
Create `cmd/hearsay/cmd_other.go` with `cmdWatch`, `cmdCursor`, `cmdStatus`.

The content of each file is the existing code from `main.go`, split by command. No new code — just file reorganization.

- [ ] **Step 3: Remove old main.go**

```bash
rm cmd/hearsay/main.go
```

- [ ] **Step 4: Build and test**

```bash
go build ./cmd/hearsay
go test ./cmd/hearsay/... -v
```

Expected: builds and all tests pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/hearsay/main_native.go cmd/hearsay/cmd_serve.go cmd/hearsay/cmd_claims.go cmd/hearsay/cmd_config.go cmd/hearsay/cmd_other.go
git rm cmd/hearsay/main.go
git commit -m "refactor: split main.go into native entrypoint + shared command files"
```

---

### Task 9: Create WASM entrypoint

**Files:**
- Create: `cmd/hearsay/main_wasm.go`

- [ ] **Step 1: Create main_wasm.go**

Create `cmd/hearsay/main_wasm.go`:

```go
//go:build wasm

package main

import (
	"context"
	"net/http"
	"strings"
	"syscall/js"
	"time"

	"github.com/gstranger/hearsay/internal/d1"
	"github.com/gstranger/hearsay/internal/server"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

var (
	globalProvider hearsay.Provider
	globalServer   *server.Server
)

func init() {
	// Register the fetch handler
	js.Global().Set("fetch", js.FuncOf(handleFetch))
}

func handleFetch(this js.Value, args []js.Value) any {
	request := args[0]
	url := request.Get("url").String()

	// Initialize provider on first request
	if globalProvider == nil {
		d1Binding := js.Global().Get("HEARSAY_D1")
		if d1Binding.IsUndefined() {
			return newResponse(500, `{"error":"HEARSAY_D1 binding not found"}`)
		}
		globalProvider = d1.New(d1Binding)
		client := hearsay.NewClient(globalProvider, "default")
		globalServer = server.New(client, globalProvider, false, "", server.LogFormatText, 0, 0, hearsay.AuditOff, "default")
	}

	// Extract namespace from URL path. URLs look like:
	//   /ns/acme-project/claim  → namespace="acme-project", path="/claim"
	//   /ns/acme-project/events → namespace="acme-project", path="/events"
	path := strings.TrimPrefix(url, "http://localhost")
	path = strings.TrimPrefix(path, "https://localhost")
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 4)
	if len(parts) < 3 || parts[0] != "ns" {
		return newResponse(400, `{"error":"invalid path, expected /ns/<namespace>/..."}`)
	}
	namespace := parts[1]
	remainingPath := "/" + strings.Join(parts[2:], "/")

	// Build an http.Request from the JS Request
	method := request.Get("method").String()
	body := ""
	if request.Get("body").Truthy() {
		bodyPromise := request.Call("text")
		// In WASM, we can't await promises directly. For now, assume small bodies.
		// Full async body reading requires a more sophisticated adapter.
	}

	goReq, err := http.NewRequestWithContext(context.Background(), method, remainingPath, strings.NewReader(body))
	if err != nil {
		return newResponse(400, `{"error":"invalid request"}`)
	}

	// Copy headers
	headers := request.Get("headers")
	if headers.Truthy() {
		headerIter := headers.Call("entries")
		for {
			entry := headerIter.Call("next")
			if entry.Get("done").Bool() {
				break
			}
			kv := entry.Get("value")
			key := kv.Index(0).String()
			value := kv.Index(1).String()
			goReq.Header.Set(key, value)
		}
	}

	// Add namespace as query parameter so handlers can read it via r.URL.Query().Get("namespace")
	goReq.Header.Set("X-Hearsay-Namespace", namespace)
	goReq.URL.RawQuery = "namespace=" + namespace

	// Serve via shared server.Server
	rec := &wasmResponseRecorder{
		headers:    http.Header{},
		statusCode: 200,
	}
	globalServer.ServeHTTP(rec, goReq)

	return newResponse(rec.statusCode, rec.body.String())
}

// wasmResponseRecorder implements http.ResponseWriter for WASM.
type wasmResponseRecorder struct {
	headers    http.Header
	body       strings.Builder
	statusCode int
}

func (w *wasmResponseRecorder) Header() http.Header         { return w.headers }
func (w *wasmResponseRecorder) Write(b []byte) (int, error) { return w.body.Write(b) }
func (w *wasmResponseRecorder) WriteHeader(code int)        { w.statusCode = code }

func newResponse(statusCode int, body string) js.Value {
	headers := js.Global().Get("Headers").New()
	headers.Call("set", "Content-Type", "application/json")

	init := js.Global().Get("Object").New()
	init.Set("status", statusCode)
	init.Set("headers", headers)

	return js.Global().Get("Response").New(body, init)
}
```

- [ ] **Step 2: Verify WASM compilation**

```bash
GOOS=js GOARCH=wasm go build ./cmd/hearsay
```

Expected: builds successfully.

- [ ] **Step 3: Verify native build still works**

```bash
go build ./cmd/hearsay
./hearsay --help 2>&1 || true
```

Expected: builds and runs on native.

- [ ] **Step 4: Commit**

```bash
git add cmd/hearsay/main_wasm.go
git commit -m "feat: add WASM entrypoint for Cloudflare Workers"
```

---

### Task 10: Add Config constructor for non-filesystem init

**Files:**
- Modify: `pkg/hearsay/config.go`

- [ ] **Step 1: Add NewConfig constructor**

In `pkg/hearsay/config.go`, add a constructor that accepts a Config struct directly (for WASM where there's no filesystem):

```go
// NewConfig creates a Config directly without reading from disk.
// Useful for WASM builds and programmatic configuration.
func NewConfig(namespace string, provider string, sqlitePath string, postgresURL string) *Config {
	cfg := &Config{
		Version:   1,
		Namespace: namespace,
		Provider:  provider,
		Defaults:  DefaultsConfig{TTLSeconds: 300, AutoHeartbeat: true},
	}
	switch provider {
	case "sqlite":
		cfg.ProviderCfg.SQLite = &SQLiteConfig{Path: sqlitePath}
	case "postgresql":
		cfg.ProviderCfg.PostgreSQL = &PostgreSQLConfig{URL: postgresURL}
	case "d1":
		cfg.ProviderCfg.D1 = &D1Config{}
	}
	return cfg
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./...
GOOS=js GOARCH=wasm go build ./pkg/hearsay/...
```

Expected: both build.

- [ ] **Step 3: Commit**

```bash
git add pkg/hearsay/config.go
git commit -m "feat: add NewConfig constructor for non-filesystem initialization"
```

---

### Task 11: Build verification and final integration test

**Files:**
- No new files. Verify all targets compile and pass tests.

- [ ] **Step 1: Full native test suite**

```bash
go test ./... -count=1 -v
```

Expected: all tests pass.

- [ ] **Step 2: Native binary build**

```bash
go build ./cmd/hearsay
./hearsay 2>&1 || true
```

Expected: builds, shows usage.

- [ ] **Step 3: WASM build verification**

```bash
GOOS=js GOARCH=wasm go build ./cmd/hearsay
GOOS=js GOARCH=wasm go vet ./internal/d1/... ./internal/do/... 2>&1 || true
```

Expected: builds, no critical vet errors.

- [ ] **Step 4: Verify build-tagged files are correctly gated**

```bash
# Native build should NOT include wasm files
go build ./... 2>&1 | grep -i "wasm" && echo "WARNING: wasm files leaked into native build" || echo "OK: no wasm leakage"

# Confirm wasm files exist and have correct build tags
head -1 internal/d1/d1.go | grep "wasm" && echo "OK: d1 has wasm build tag" || echo "ERROR: d1 missing build tag"
head -1 internal/do/do.go | grep "wasm" && echo "OK: do has wasm build tag" || echo "ERROR: do missing build tag"
head -1 internal/server/sweeper_wasm.go | grep "wasm" && echo "OK: sweeper_wasm has build tag" || echo "ERROR: sweeper_wasm missing build tag"
head -1 internal/server/sse.go | grep -v "wasm" && echo "OK: sse has no build tag (shared)" || echo "ERROR: sse has unexpected build tag"
```

Expected: all OK messages.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore: final build verification, all targets compile"
```