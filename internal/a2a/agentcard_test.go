package a2a

import (
	"testing"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

func TestGenerateAgentCard(t *testing.T) {
	cfg := &hearsay.A2AConfig{Addr: "localhost:8081", APIKey: "secret"}
	card := GenerateAgentCard(cfg, "1.0.0")
	if card.Name != "hearsay-coordinator" { t.Fatal("name mismatch") }
	if card.URL != "http://localhost:8081/a2a" { t.Fatalf("url mismatch: got %s", card.URL) }
	if len(card.Skills) != 5 { t.Fatalf("expected 5 skills, got %d", len(card.Skills)) }
	if !card.Capabilities.Streaming { t.Fatal("streaming should be true") }
	if card.Authentication == nil { t.Fatal("expected auth") }
	if len(card.Authentication.Schemes) != 1 || card.Authentication.Schemes[0] != "api-key" {
		t.Fatalf("expected api-key scheme, got %v", card.Authentication.Schemes)
	}
}

func TestGenerateAgentCardBearerOnly(t *testing.T) {
	cfg := &hearsay.A2AConfig{Addr: "localhost:8081", BearerJWKSURL: "https://auth.example.com/jwks"}
	card := GenerateAgentCard(cfg, "1.0.0")
	if len(card.Authentication.Schemes) != 1 || card.Authentication.Schemes[0] != "Bearer" {
		t.Fatalf("expected Bearer scheme, got %v", card.Authentication.Schemes)
	}
}

func TestGenerateAgentCardBothAuth(t *testing.T) {
	cfg := &hearsay.A2AConfig{Addr: "localhost:8081", APIKey: "secret", BearerValidatorURL: "https://auth.example.com/verify"}
	card := GenerateAgentCard(cfg, "1.0.0")
	if len(card.Authentication.Schemes) != 2 {
		t.Fatalf("expected 2 schemes, got %d", len(card.Authentication.Schemes))
	}
}
