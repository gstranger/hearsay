package a2a

import (
	"encoding/json"
	"fmt"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

type SkillParams struct {
	SkillID        string `json:"skill_id"`
	ResourceURI    string `json:"resource_uri"`
	Operation      string `json:"operation"`
	Intent         string `json:"intent"`
	AgentID        string `json:"agent_id"`
	TTLSeconds     int    `json:"ttl_seconds"`
	ClaimID        string `json:"claim_id"`
	Outcome        string `json:"outcome"`
	To             string `json:"to"`
	MsgType        string `json:"type"`
	Content        string `json:"content"`
	RelatedClaimID string `json:"related_claim_id"`
	UnreadOnly     bool   `json:"unread_only"`
}

func extractParams(parts []Part) (*SkillParams, error) {
	var params SkillParams
	for _, p := range parts {
		if p.Type == "data" && len(p.Data) > 0 {
			if err := json.Unmarshal(p.Data, &params); err != nil {
				return nil, fmt.Errorf("invalid data part: %w", err)
			}
			return &params, nil
		}
	}
	return nil, fmt.Errorf("no data part found in message")
}

func mapToClaimRequest(params *SkillParams) hearsay.ClaimRequest {
	return hearsay.ClaimRequest{
		ResourceURI: params.ResourceURI,
		AgentID:     params.AgentID,
		Operation:   hearsay.Operation(params.Operation),
		Intent:      params.Intent,
		TTLSeconds:  params.TTLSeconds,
	}
}

func mapToMailboxMessage(params *SkillParams) hearsay.MailboxMessage {
	return hearsay.MailboxMessage{
		From:           "", // set by caller
		To:             params.To,
		Type:           hearsay.MailboxType(params.MsgType),
		Content:        params.Content,
		RelatedClaimID: params.RelatedClaimID,
	}
}
