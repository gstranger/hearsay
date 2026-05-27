package hearsay

import (
	"strings"
	"testing"
)

func TestValidateAgentID(t *testing.T) {
	if err := ValidateAgentID("agent-1"); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	if err := ValidateAgentID(""); err == nil {
		t.Fatal("expected error for empty agent_id")
	}
	if err := ValidateAgentID(strings.Repeat("a", MaxAgentIDLen+1)); err == nil {
		t.Fatal("expected error for too-long agent_id")
	}
}

func TestValidateResourceURI(t *testing.T) {
	if err := ValidateResourceURI("file://src/main.go"); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	if err := ValidateResourceURI(""); err == nil {
		t.Fatal("expected error for empty uri")
	}
	if err := ValidateResourceURI("no-scheme"); err == nil {
		t.Fatal("expected error for missing scheme")
	}
	if err := ValidateResourceURI(strings.Repeat("a", MaxResourceURILen+1)); err == nil {
		t.Fatal("expected error for too-long uri")
	}
}

func TestValidateIntent(t *testing.T) {
	if err := ValidateIntent("refactoring"); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	if err := ValidateIntent(strings.Repeat("a", MaxIntentLen+1)); err == nil {
		t.Fatal("expected error for too-long intent")
	}
}

func TestValidateNamespace(t *testing.T) {
	if err := ValidateNamespace("default"); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	if err := ValidateNamespace(""); err == nil {
		t.Fatal("expected error for empty namespace")
	}
	if err := ValidateNamespace(strings.Repeat("a", MaxNamespaceLen+1)); err == nil {
		t.Fatal("expected error for too-long namespace")
	}
}
