package agentstate

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFilterActiveClaims(t *testing.T) {
	claim := Claim{ClaimID: "c1", AgentID: "a1", ResourceURI: "file://x.ts", Operation: OpWrite, TTLSeconds: 300, CreatedAt: time.Now()}
	payload, _ := json.Marshal(claim)
	msgs := []Message{{Offset: 1, Type: MsgClaim, Payload: payload}}

	active := FilterActiveClaims(msgs)
	if len(active) != 1 || active[0].ClaimID != "c1" {
		t.Fatalf("expected 1 active claim c1, got %+v", active)
	}

	// Release it
	relPayload, _ := json.Marshal(map[string]string{"claim_id": "c1", "outcome": "succeeded"})
	msgs = append(msgs, Message{Offset: 2, Type: MsgRelease, Payload: relPayload})
	active = FilterActiveClaims(msgs)
	if len(active) != 0 {
		t.Fatalf("expected 0 active after release, got %d", len(active))
	}
}

func TestFilterActiveClaimsExpired(t *testing.T) {
	oldClaim := Claim{ClaimID: "c2", AgentID: "a1", ResourceURI: "file://y.ts", Operation: OpWrite, TTLSeconds: 1, CreatedAt: time.Now().Add(-2 * time.Second)}
	payload, _ := json.Marshal(oldClaim)
	msgs := []Message{{Offset: 1, Type: MsgClaim, Payload: payload}}

	active := FilterActiveClaims(msgs)
	if len(active) != 0 {
		t.Fatalf("expected 0 active (expired), got %d", len(active))
	}
}

func TestFilterActiveClaimsTransfer(t *testing.T) {
	claim := Claim{ClaimID: "c3", AgentID: "a1", ResourceURI: "file://z.ts", Operation: OpWrite, TTLSeconds: 300, CreatedAt: time.Now()}
	payload, _ := json.Marshal(claim)
	msgs := []Message{{Offset: 1, Type: MsgClaim, Payload: payload}}

	// Transfer it
	trPayload, _ := json.Marshal(map[string]string{"claim_id": "c3", "to_agent_id": "a2"})
	msgs = append(msgs, Message{Offset: 2, Type: MsgTransfer, Payload: trPayload})
	active := FilterActiveClaims(msgs)
	if len(active) != 0 {
		t.Fatalf("expected 0 active after transfer, got %d", len(active))
	}
}

func TestFilterActiveClaimsCrossAgentRelease(t *testing.T) {
	c1 := Claim{ClaimID: "c1", AgentID: "a1", ResourceURI: "file://x.ts", Operation: OpWrite, TTLSeconds: 300, CreatedAt: time.Now()}
	c2 := Claim{ClaimID: "c2", AgentID: "a2", ResourceURI: "file://y.ts", Operation: OpWrite, TTLSeconds: 300, CreatedAt: time.Now()}
	p1, _ := json.Marshal(c1)
	p2, _ := json.Marshal(c2)
	rel, _ := json.Marshal(map[string]string{"claim_id": "c1"})
	msgs := []Message{
		{Offset: 1, Type: MsgClaim, Payload: p1},
		{Offset: 2, Type: MsgClaim, Payload: p2},
		{Offset: 3, Type: MsgRelease, Payload: rel},
	}
	active := FilterActiveClaims(msgs)
	if len(active) != 1 || active[0].ClaimID != "c2" {
		t.Fatalf("expected only c2 active, got %+v", active)
	}
}

func TestFilterActiveClaimsEarlyReleaseIgnored(t *testing.T) {
	claim := Claim{ClaimID: "c1", AgentID: "a1", ResourceURI: "file://x.ts", Operation: OpWrite, TTLSeconds: 300, CreatedAt: time.Now()}
	rel, _ := json.Marshal(map[string]string{"claim_id": "c1"})
	p1, _ := json.Marshal(claim)
	msgs := []Message{
		{Offset: 1, Type: MsgRelease, Payload: rel},
		{Offset: 2, Type: MsgClaim, Payload: p1},
	}
	active := FilterActiveClaims(msgs)
	if len(active) != 1 || active[0].ClaimID != "c1" {
		t.Fatalf("expected c1 active (release before claim ignored), got %+v", active)
	}
}

func TestIsClaimActiveDirectly(t *testing.T) {
	claim := Claim{ClaimID: "c1", AgentID: "a1", ResourceURI: "file://x.ts", Operation: OpWrite, TTLSeconds: 300, CreatedAt: time.Now()}
	p1, _ := json.Marshal(claim)
	msgs := []Message{{Offset: 1, Type: MsgClaim, Payload: p1}}
	if !IsClaimActive(msgs, 1, claim, time.Time{}) {
		t.Fatal("expected claim to be active")
	}

	rel, _ := json.Marshal(map[string]string{"claim_id": "c1"})
	msgs = append(msgs, Message{Offset: 2, Type: MsgRelease, Payload: rel})
	if IsClaimActive(msgs, 1, claim, time.Time{}) {
		t.Fatal("expected claim to be inactive after release")
	}
}

func TestFilterActiveClaimsHeartbeatExtendsTTL(t *testing.T) {
	// Claim created 10 seconds ago with 5-second TTL — would normally be expired
	claim := Claim{ClaimID: "c1", AgentID: "a1", ResourceURI: "file://x.ts", Operation: OpWrite, TTLSeconds: 5, CreatedAt: time.Now().Add(-10 * time.Second)}
	p1, _ := json.Marshal(claim)
	// Heartbeat 2 seconds ago — extends deadline to now+3s
	hbPayload, _ := json.Marshal(map[string]string{"claim_id": "c1"})
	msgs := []Message{
		{Offset: 1, Type: MsgClaim, Payload: p1, Timestamp: claim.CreatedAt},
		{Offset: 2, Type: MsgHeartbeat, Payload: hbPayload, Timestamp: time.Now().Add(-2 * time.Second)},
	}
	active := FilterActiveClaims(msgs)
	if len(active) != 1 || active[0].ClaimID != "c1" {
		t.Fatalf("expected c1 active (heartbeat extended TTL), got %+v", active)
	}
}

func TestIsClaimActiveHeartbeatExtendsTTL(t *testing.T) {
	claim := Claim{ClaimID: "c1", AgentID: "a1", ResourceURI: "file://x.ts", Operation: OpWrite, TTLSeconds: 5, CreatedAt: time.Now().Add(-10 * time.Second)}
	p1, _ := json.Marshal(claim)
	hbPayload, _ := json.Marshal(map[string]string{"claim_id": "c1"})
	msgs := []Message{
		{Offset: 1, Type: MsgClaim, Payload: p1, Timestamp: claim.CreatedAt},
		{Offset: 2, Type: MsgHeartbeat, Payload: hbPayload, Timestamp: time.Now().Add(-2 * time.Second)},
	}
	if !IsClaimActive(msgs, 1, claim, time.Time{}) {
		t.Fatal("expected claim active because heartbeat extended TTL")
	}
}
