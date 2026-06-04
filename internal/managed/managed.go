package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

// Provider delegates all Provider operations to a remote hearsay HTTP API.
// It is useful when hearsay is run as a hosted service.
type Provider struct {
	endpoint string
	token    string
	client   *http.Client
}

// New creates a managed provider pointing at the given endpoint.
// If token is non-empty it is sent as a Bearer Authorization header.
func New(endpoint, token string) *Provider {
	return &Provider{
		endpoint: endpoint,
		token:    token,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *Provider) CreateNamespace(ctx context.Context, ns hearsay.Namespace) error {
	body, _ := json.Marshal(ns)
	return p.post(ctx, "/namespaces/create", body)
}

func (p *Provider) DeleteNamespace(ctx context.Context, namespaceID string) error {
	body, _ := json.Marshal(map[string]string{"id": namespaceID})
	return p.post(ctx, "/namespaces/delete", body)
}

func (p *Provider) Append(ctx context.Context, namespaceID string, msgs []hearsay.Message) error {
	body, _ := json.Marshal(map[string]any{
		"namespace": namespaceID,
		"messages":  msgs,
	})
	return p.post(ctx, "/append", body)
}

func (p *Provider) Query(ctx context.Context, namespaceID string, opts hearsay.QueryOpts) ([]hearsay.Message, error) {
	q := fmt.Sprintf("%s/query?namespace=%s&since=%d", p.endpoint, namespaceID, opts.Since)
	if opts.AgentID != "" {
		q += "&agent=" + opts.AgentID
	}
	if opts.Limit > 0 {
		q += fmt.Sprintf("&limit=%d", opts.Limit)
	}
	var resp *http.Response
	var err error
	if len(opts.Types) > 0 {
		// POST with types in body because URL encoding arrays is messy
		body, _ := json.Marshal(map[string]any{
			"namespace": namespaceID,
			"since":     opts.Since,
			"agent_id":  opts.AgentID,
			"limit":     opts.Limit,
			"types":     opts.Types,
		})
		resp, err = p.do(ctx, "POST", p.endpoint+"/query", body)
	} else {
		resp, err = p.do(ctx, "GET", q, nil)
	}
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("managed query: %s: %s", resp.Status, body)
	}
	var result []hearsay.Message
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
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

// SubscribeEvents streams coordination events using polling against the remote server.
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

func (p *Provider) ActiveClaims(ctx context.Context, namespaceID string, resourcePattern string) ([]hearsay.Claim, error) {
	url := fmt.Sprintf("%s/claims?namespace=%s&resource=%s", p.endpoint, namespaceID, resourcePattern)
	resp, err := p.do(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("managed claims: %s: %s", resp.Status, body)
	}
	var result []hearsay.Claim
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func (p *Provider) AgentState(ctx context.Context, namespaceID string, agentID string) (hearsay.AgentState, error) {
	url := fmt.Sprintf("%s/agents?namespace=%s&agent=%s", p.endpoint, namespaceID, agentID)
	resp, err := p.do(ctx, "GET", url, nil)
	if err != nil {
		return hearsay.AgentState{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return hearsay.AgentState{}, fmt.Errorf("managed hearsay: %s: %s", resp.Status, body)
	}
	var result hearsay.AgentState
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return hearsay.AgentState{}, err
	}
	return result, nil
}

func (p *Provider) ReleaseExpired(ctx context.Context, namespaceID string, before time.Time) error {
	body, _ := json.Marshal(map[string]any{"namespace": namespaceID, "before": before})
	return p.post(ctx, "/expire", body)
}

func (p *Provider) SendMessage(ctx context.Context, namespace string, msg hearsay.MailboxMessage) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", p.endpoint+"/message", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Namespace", namespace)
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("send message failed: %d", resp.StatusCode)
	}
	return nil
}

func (p *Provider) GetMailbox(ctx context.Context, namespace string, agentID string, opts hearsay.MailboxQueryOpts) ([]hearsay.MailboxMessage, error) {
	url := fmt.Sprintf("%s/mailbox?namespace=%s&agent_id=%s", p.endpoint, namespace, agentID)
	if opts.Unread {
		url += "&unread=true"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get mailbox failed: %d", resp.StatusCode)
	}
	var msgs []hearsay.MailboxMessage
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

func (p *Provider) MarkRead(ctx context.Context, namespace string, messageID string) error {
	payload, _ := json.Marshal(map[string]string{"namespace": namespace, "message_id": messageID})
	req, err := http.NewRequestWithContext(ctx, "POST", p.endpoint+"/message/read", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mark read failed: %d", resp.StatusCode)
	}
	return nil
}

func (p *Provider) ArchiveMessage(ctx context.Context, namespace string, messageID string) error {
	payload, _ := json.Marshal(map[string]string{"namespace": namespace, "message_id": messageID})
	req, err := http.NewRequestWithContext(ctx, "POST", p.endpoint+"/message/archive", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("archive failed: %d", resp.StatusCode)
	}
	return nil
}

func (p *Provider) ExpireMessages(ctx context.Context, namespace string, before time.Time) error {
	// Managed provider delegates expiration to the remote service
	return nil
}

func (p *Provider) CreateTask(ctx context.Context, namespace string, task *hearsay.A2ATask) error {
	body, _ := json.Marshal(map[string]any{"namespace": namespace, "task": task})
	return p.post(ctx, "/tasks/create", body)
}

func (p *Provider) GetTask(ctx context.Context, namespace string, taskID string) (*hearsay.A2ATask, error) {
	url := fmt.Sprintf("%s/tasks/get?namespace=%s&id=%s", p.endpoint, namespace, taskID)
	resp, err := p.do(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("managed get task: %s: %s", resp.Status, b)
	}
	var result hearsay.A2ATask
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (p *Provider) UpdateTask(ctx context.Context, namespace string, task *hearsay.A2ATask) error {
	body, _ := json.Marshal(map[string]any{"namespace": namespace, "task": task})
	return p.post(ctx, "/tasks/update", body)
}

func (p *Provider) AppendTaskHistory(ctx context.Context, namespace string, taskID string, seq int, msg hearsay.A2AMessage) error {
	body, _ := json.Marshal(map[string]any{"namespace": namespace, "task_id": taskID, "seq": seq, "message": msg})
	return p.post(ctx, "/tasks/history", body)
}

func (p *Provider) GetTaskHistory(ctx context.Context, namespace string, taskID string, limit int) ([]hearsay.A2AMessage, error) {
	url := fmt.Sprintf("%s/tasks/history?namespace=%s&id=%s&limit=%d", p.endpoint, namespace, taskID, limit)
	resp, err := p.do(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("managed get task history: %s: %s", resp.Status, b)
	}
	var result []hearsay.A2AMessage
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func (p *Provider) CreateArtifact(ctx context.Context, namespace string, taskID string, art hearsay.A2AArtifact) error {
	body, _ := json.Marshal(map[string]any{"namespace": namespace, "task_id": taskID, "artifact": art})
	return p.post(ctx, "/tasks/artifact", body)
}

func (p *Provider) GetArtifacts(ctx context.Context, namespace string, taskID string) ([]hearsay.A2AArtifact, error) {
	url := fmt.Sprintf("%s/tasks/artifacts?namespace=%s&id=%s", p.endpoint, namespace, taskID)
	resp, err := p.do(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("managed get artifacts: %s: %s", resp.Status, b)
	}
	var result []hearsay.A2AArtifact
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func (p *Provider) AppendAudit(ctx context.Context, namespaceID string, events []hearsay.AuditEvent) error {
	body, _ := json.Marshal(events)
	req, err := http.NewRequestWithContext(ctx, "POST", p.endpoint+"/audit?namespace="+namespaceID, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("managed AppendAudit: %s", resp.Status)
	}
	return nil
}

func (p *Provider) post(ctx context.Context, path string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, "POST", p.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("managed %s: %s: %s", path, resp.Status, body)
	}
	return nil
}

func (p *Provider) do(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	return p.client.Do(req)
}
