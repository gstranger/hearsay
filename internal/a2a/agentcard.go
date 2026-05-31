package a2a

import (
	"encoding/json"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

type AgentCard struct {
	Name               string          `json:"name"`
	Description        string          `json:"description"`
	URL                string          `json:"url"`
	Provider           *ProviderInfo   `json:"provider,omitempty"`
	Version            string          `json:"version"`
	DocumentationURL   string          `json:"documentationUrl,omitempty"`
	Capabilities       Capabilities    `json:"capabilities"`
	Authentication     *Authentication `json:"authentication,omitempty"`
	DefaultInputModes  []string        `json:"defaultInputModes,omitempty"`
	DefaultOutputModes []string        `json:"defaultOutputModes,omitempty"`
	Skills             []Skill         `json:"skills"`
}

type ProviderInfo struct {
	Organization string `json:"organization"`
	URL          string `json:"url,omitempty"`
}

type Capabilities struct {
	Streaming              bool `json:"streaming"`
	PushNotifications      bool `json:"pushNotifications"`
	StateTransitionHistory bool `json:"stateTransitionHistory"`
}

type Authentication struct {
	Schemes     []string `json:"schemes"`
	Credentials string   `json:"credentials,omitempty"`
}

type Skill struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Tags        []string        `json:"tags,omitempty"`
	Examples    []string        `json:"examples,omitempty"`
	InputModes  []string        `json:"inputModes,omitempty"`
	OutputModes []string        `json:"outputModes,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

func GenerateAgentCard(cfg *hearsay.A2AConfig, version string) *AgentCard {
	addr := ""
	if cfg != nil {
		addr = "http://" + cfg.Addr + "/a2a"
	}
	card := &AgentCard{
		Name:             "hearsay-coordinator",
		Description:      "Resource locking, conflict detection, and mailbox for agent teams",
		URL:              addr,
		Version:          version,
		Capabilities:     Capabilities{Streaming: true, PushNotifications: false, StateTransitionHistory: false},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text", "data"},
		Skills: []Skill{
			claimResourceSkill(),
			releaseResourceSkill(),
			checkConflictSkill(),
			queryMailboxSkill(),
			sendMailboxSkill(),
		},
	}

	var schemes []string
	if cfg != nil {
		if cfg.APIKey != "" { schemes = append(schemes, "api-key") }
		if cfg.BearerValidatorURL != "" || cfg.BearerJWKSURL != "" { schemes = append(schemes, "Bearer") }
	}
	if len(schemes) > 0 {
		card.Authentication = &Authentication{Schemes: schemes}
	}

	return card
}

func claimResourceSkill() Skill {
	return Skill{
		ID: "claim_resource", Name: "Claim Resource",
		Description: "Lock a resource for an operation. Returns claim ID or conflict report.",
		Tags: []string{"coordination", "locking"},
		Examples: []string{"Claim file://src/api.go for write"},
		InputModes: []string{"text", "data"}, OutputModes: []string{"text", "data"},
		Parameters: json.RawMessage(`{"type":"object","properties":{"resource_uri":{"type":"string"},"operation":{"type":"string","enum":["read","write","delete","rename","refactor"]},"intent":{"type":"string"},"agent_id":{"type":"string"},"ttl_seconds":{"type":"integer","default":300}},"required":["resource_uri","operation","agent_id"]}`),
	}
}

func releaseResourceSkill() Skill {
	return Skill{
		ID: "release_resource", Name: "Release Resource",
		Description: "Release a previously granted claim.",
		Tags: []string{"coordination", "locking"},
		Parameters: json.RawMessage(`{"type":"object","properties":{"claim_id":{"type":"string"},"outcome":{"type":"string","enum":["succeeded","abandoned","conflicted"]}},"required":["claim_id"]}`),
	}
}

func checkConflictSkill() Skill {
	return Skill{
		ID: "check_conflict", Name: "Check Conflict",
		Description: "Check if an operation would conflict with active claims, without actually claiming.",
		Tags: []string{"coordination", "query"},
		Parameters: json.RawMessage(`{"type":"object","properties":{"resource_uri":{"type":"string"},"operation":{"type":"string","enum":["read","write","delete","rename","refactor"]}},"required":["resource_uri","operation"]}`),
	}
}

func queryMailboxSkill() Skill {
	return Skill{
		ID: "query_mailbox", Name: "Query Mailbox",
		Description: "Read messages from your mailbox.",
		Tags: []string{"messaging", "query"},
		Parameters: json.RawMessage(`{"type":"object","properties":{"agent_id":{"type":"string"},"unread_only":{"type":"boolean","default":false}},"required":["agent_id"]}`),
	}
}

func sendMailboxSkill() Skill {
	return Skill{
		ID: "send_mailbox", Name: "Send Mailbox Message",
		Description: "Send a message to another agent's mailbox.",
		Tags: []string{"messaging"},
		Parameters: json.RawMessage(`{"type":"object","properties":{"to":{"type":"string"},"type":{"type":"string","enum":["yield_request","yield_ack","escalation","all_clear","note","ping"]},"content":{"type":"string"},"related_claim_id":{"type":"string"}},"required":["to","type","content"]}`),
	}
}
