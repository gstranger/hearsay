package a2a

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

func (s *Server) executeSkill(ctx context.Context, task *Task, msg Message) (*Task, error) {
	params, err := extractParams(msg.Parts)
	if err != nil {
		return nil, err
	}

	// Use agent_id from auth context if not provided in params
	if params.AgentID == "" {
		params.AgentID = AgentIDFromContext(ctx)
	}

	switch params.SkillID {
	case "claim_resource":
		return s.skillClaimResource(ctx, task, params)
	case "release_resource":
		return s.skillReleaseResource(ctx, task, params)
	case "check_conflict":
		return s.skillCheckConflict(ctx, task, params)
	case "query_mailbox":
		return s.skillQueryMailbox(ctx, task, params)
	case "send_mailbox":
		return s.skillSendMailbox(ctx, task, params)
	default:
		return nil, fmt.Errorf("unknown skill: %s", params.SkillID)
	}
}

func (s *Server) skillClaimResource(ctx context.Context, task *Task, params *SkillParams) (*Task, error) {
	req := mapToClaimRequest(params)
	resp, err := s.client.Claim(ctx, req)
	if err != nil {
		if _, ok := err.(*hearsay.ConflictError); ok {
			// Do NOT set task.ClaimID — the claim was not granted
			reportJSON, _ := json.Marshal(resp.Conflict)
			task.Artifacts = []Artifact{{
				Name: "conflict-report",
				Parts: []Part{{Type: "data", Data: reportJSON}},
			}}
			return task, NewError(-32002, "Conflict detected", resp.Conflict)
		}
		return nil, err
	}
	task.ClaimID = resp.ClaimID
	task.Artifacts = []Artifact{{
		Name: "claim-result",
		Parts: []Part{{Type: "data", Data: mustJSON(map[string]string{"claim_id": resp.ClaimID, "status": "granted"})}},
	}}
	return task, nil
}

func (s *Server) skillReleaseResource(ctx context.Context, task *Task, params *SkillParams) (*Task, error) {
	outcome := hearsay.Outcome(params.Outcome)
	if outcome == "" {
		outcome = hearsay.OutcomeSucceeded
	}
	if err := s.client.Release(ctx, params.ClaimID, outcome); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *Server) skillCheckConflict(ctx context.Context, task *Task, params *SkillParams) (*Task, error) {
	report, err := s.client.CheckConflict(ctx, params.ResourceURI, hearsay.Operation(params.Operation))
	if err != nil {
		return nil, err
	}
	reportJSON, _ := json.Marshal(report)
	task.Artifacts = []Artifact{{
		Name: "conflict-report",
		Parts: []Part{{Type: "data", Data: reportJSON}},
	}}
	return task, nil
}

func (s *Server) skillQueryMailbox(ctx context.Context, task *Task, params *SkillParams) (*Task, error) {
	msgs, err := s.provider.GetMailbox(ctx, s.namespace, params.AgentID, hearsay.MailboxQueryOpts{Unread: params.UnreadOnly})
	if err != nil {
		return nil, err
	}
	msgsJSON, _ := json.Marshal(msgs)
	task.Artifacts = []Artifact{{
		Name: "mailbox",
		Parts: []Part{{Type: "data", Data: msgsJSON}},
	}}
	return task, nil
}

func (s *Server) skillSendMailbox(ctx context.Context, task *Task, params *SkillParams) (*Task, error) {
	msg := mapToMailboxMessage(params)
	msg.From = params.AgentID
	if err := s.provider.SendMessage(ctx, s.namespace, msg); err != nil {
		return nil, err
	}
	resultJSON, _ := json.Marshal(map[string]string{"message_id": msg.MessageID})
	task.Artifacts = []Artifact{{
		Name: "send-result",
		Parts: []Part{{Type: "data", Data: resultJSON}},
	}}
	return task, nil
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
