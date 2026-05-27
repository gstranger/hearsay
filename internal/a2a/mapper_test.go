package a2a

import (
	"encoding/json"
	"testing"
)

func TestExtractParams(t *testing.T) {
	parts := []Part{
		{Type: "data", Data: json.RawMessage(`{"resource_uri":"file://x.ts","operation":"write","agent_id":"a1"}`)},
	}
	params, err := extractParams(parts)
	if err != nil {
		t.Fatal(err)
	}
	if params.ResourceURI != "file://x.ts" {
		t.Fatal("uri mismatch")
	}
	if params.AgentID != "a1" {
		t.Fatal("agent_id mismatch")
	}
}

func TestMapToClaimRequest(t *testing.T) {
	params := &SkillParams{ResourceURI: "file://x.ts", Operation: "write", AgentID: "a1", Intent: "test", TTLSeconds: 60}
	req := mapToClaimRequest(params)
	if req.ResourceURI != "file://x.ts" {
		t.Fatal("uri mismatch")
	}
	if req.Operation != "write" {
		t.Fatal("op mismatch")
	}
	if req.TTLSeconds != 60 {
		t.Fatal("ttl mismatch")
	}
}
