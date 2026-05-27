package agentstate

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type Client struct {
	provider  Provider
	namespace string
}

type ClaimRequest struct {
	ResourceURI   string
	AgentID       string
	Operation     Operation
	Intent        string
	ProjectedEnd  string
	TTLSeconds    int
	ParentClaimID string
}

type ClaimResponse struct {
	ClaimID   string          `json:"claim_id"`
	Conflict  *ConflictReport `json:"conflicts,omitempty"`
}

func NewClient(provider Provider, namespace string) *Client {
	return &Client{provider: provider, namespace: namespace}
}

func (c *Client) Claim(ctx context.Context, req ClaimRequest) (*ClaimResponse, error) {
	if req.TTLSeconds == 0 {
		req.TTLSeconds = 300
	}

	active, err := c.provider.ActiveClaims(ctx, c.namespace, req.ResourceURI)
	if err != nil {
		return nil, &ProviderError{Cause: err}
	}

	claim := Claim{
		ClaimID:       generateClaimID(),
		AgentID:       req.AgentID,
		ResourceURI:   req.ResourceURI,
		Operation:     req.Operation,
		Intent:        req.Intent,
		ProjectedEnd:  req.ProjectedEnd,
		TTLSeconds:    req.TTLSeconds,
		ParentClaimID: req.ParentClaimID,
		CreatedAt:     time.Now(),
	}

	report := CheckConflict(claim, active)
	if report.HasConflict {
		return &ClaimResponse{ClaimID: claim.ClaimID, Conflict: report}, &ConflictError{Report: *report}
	}

	payload, _ := json.Marshal(claim)
	msg := Message{Type: MsgClaim, AgentID: req.AgentID, Payload: payload}
	if err := c.provider.Append(ctx, c.namespace, []Message{msg}); err != nil {
		return nil, &ProviderError{Cause: err}
	}

	return &ClaimResponse{ClaimID: claim.ClaimID}, nil
}

func (c *Client) Release(ctx context.Context, claimID string, outcome Outcome) error {
	payload, _ := json.Marshal(map[string]any{
		"claim_id":    claimID,
		"outcome":     outcome,
		"released_at": time.Now(),
	})
	msg := Message{Type: MsgRelease, Payload: payload}
	return c.provider.Append(ctx, c.namespace, []Message{msg})
}

func (c *Client) Heartbeat(ctx context.Context, claimID string) error {
	payload, _ := json.Marshal(map[string]any{
		"claim_id":     claimID,
		"heartbeat_at": time.Now(),
	})
	msg := Message{Type: MsgHeartbeat, Payload: payload}
	return c.provider.Append(ctx, c.namespace, []Message{msg})
}

func (c *Client) Transfer(ctx context.Context, claimID string, toAgentID string) error {
	payload, _ := json.Marshal(map[string]any{
		"claim_id":       claimID,
		"to_agent_id":    toAgentID,
		"transferred_at": time.Now(),
	})
	msg := Message{Type: MsgTransfer, Payload: payload}
	return c.provider.Append(ctx, c.namespace, []Message{msg})
}

func (c *Client) QueryClaims(ctx context.Context, opts QueryOpts) ([]Claim, error) {
	msgs, err := c.provider.Query(ctx, c.namespace, opts)
	if err != nil {
		return nil, err
	}
	var claims []Claim
	for _, m := range msgs {
		if m.Type == MsgClaim {
			var c Claim
			if err := json.Unmarshal(m.Payload, &c); err == nil {
				claims = append(claims, c)
			}
		}
	}
	return claims, nil
}

func (c *Client) CheckConflict(ctx context.Context, resourceURI string, op Operation) (*ConflictReport, error) {
	active, err := c.provider.ActiveClaims(ctx, c.namespace, resourceURI)
	if err != nil {
		return nil, err
	}
	claim := Claim{ResourceURI: resourceURI, Operation: op, AgentID: "_check_"}
	return CheckConflict(claim, active), nil
}

func (c *Client) ActiveClaims(ctx context.Context, pattern string) ([]Claim, error) {
	return c.provider.ActiveClaims(ctx, c.namespace, pattern)
}

func (c *Client) UpdateIntent(ctx context.Context, claimID string, intent string) error {
	payload, _ := json.Marshal(map[string]any{
		"claim_id":   claimID,
		"intent":     intent,
		"updated_at": time.Now(),
	})
	msg := Message{Type: MsgIntent, Payload: payload}
	return c.provider.Append(ctx, c.namespace, []Message{msg})
}

func (c *Client) Close() error {
	return nil
}

func generateClaimID() string {
	return fmt.Sprintf("claim_%d", time.Now().UnixNano())
}
