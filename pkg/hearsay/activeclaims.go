package hearsay

import (
	"encoding/json"
	"time"
)

// IsClaimActive checks whether a claim is still active given all messages in the namespace.
// It scans msgs for matching release/transfer after the claim offset and checks TTL.
// Heartbeats extend the effective TTL: lastHeartbeat + TTLSeconds.
func IsClaimActive(msgs []Message, claimOffset int64, claim Claim, maxAgeSince time.Time) bool {
	for _, m := range msgs {
		if m.Offset <= claimOffset {
			continue
		}
		if m.Type != MsgRelease && m.Type != MsgTransfer {
			continue
		}
		var r struct{ ClaimID string `json:"claim_id"` }
		if err := json.Unmarshal(m.Payload, &r); err != nil {
			continue
		}
		if r.ClaimID == claim.ClaimID {
			return false
		}
	}
	if maxAgeSince.IsZero() {
		maxAgeSince = time.Now()
	}
	deadline := claim.CreatedAt.Add(time.Duration(claim.TTLSeconds) * time.Second)
	for _, m := range msgs {
		if m.Offset <= claimOffset {
			continue
		}
		if m.Type != MsgHeartbeat {
			continue
		}
		var h struct{ ClaimID string `json:"claim_id"` }
		if err := json.Unmarshal(m.Payload, &h); err != nil {
			continue
		}
		if h.ClaimID == claim.ClaimID {
			hbDeadline := m.Timestamp.Add(time.Duration(claim.TTLSeconds) * time.Second)
			if hbDeadline.After(deadline) {
				deadline = hbDeadline
			}
		}
	}
	return !maxAgeSince.After(deadline)
}

// FilterActiveClaims returns claims from msgs that are still active (not released/transferred/expired).
// It runs in O(M) where M is the number of messages. Heartbeats extend effective TTL.
func FilterActiveClaims(msgs []Message) []Claim {
	now := time.Now()

	// First pass: collect release offsets and last heartbeat timestamps per claim
	released := make(map[string]int64)
	lastHeartbeats := make(map[string]time.Time)
	for _, m := range msgs {
		if m.Type == MsgRelease || m.Type == MsgTransfer {
			var r struct{ ClaimID string `json:"claim_id"` }
			if err := json.Unmarshal(m.Payload, &r); err == nil && r.ClaimID != "" {
				if existing, ok := released[r.ClaimID]; !ok || m.Offset < existing {
					released[r.ClaimID] = m.Offset
				}
			}
		}
		if m.Type == MsgHeartbeat {
			var h struct{ ClaimID string `json:"claim_id"` }
			if err := json.Unmarshal(m.Payload, &h); err == nil && h.ClaimID != "" {
				if m.Timestamp.After(lastHeartbeats[h.ClaimID]) {
					lastHeartbeats[h.ClaimID] = m.Timestamp
				}
			}
		}
	}

	// Second pass: find active claims
	var result []Claim
	for _, m := range msgs {
		if m.Type != MsgClaim {
			continue
		}
		var c Claim
		if err := json.Unmarshal(m.Payload, &c); err != nil {
			continue
		}
		if releaseOffset, ok := released[c.ClaimID]; ok && releaseOffset > m.Offset {
			continue
		}
		deadline := c.CreatedAt.Add(time.Duration(c.TTLSeconds) * time.Second)
		if hb, ok := lastHeartbeats[c.ClaimID]; ok && hb.After(c.CreatedAt) {
			hbDeadline := hb.Add(time.Duration(c.TTLSeconds) * time.Second)
			if hbDeadline.After(deadline) {
				deadline = hbDeadline
			}
		}
		if now.After(deadline) {
			continue
		}
		result = append(result, c)
	}
	return result
}
