package agentstate

import (
	"fmt"
	"path"
	"strings"
	"time"
)

type Claim struct {
	ClaimID       string    `json:"claim_id"`
	AgentID       string    `json:"agent_id"`
	ResourceURI   string    `json:"resource_uri"`
	Operation     Operation `json:"operation"`
	Intent        string    `json:"intent"`
	ProjectedEnd  string    `json:"projected_end,omitempty"`
	TTLSeconds    int       `json:"ttl_seconds"`
	ParentClaimID string    `json:"parent_claim_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type Operation string

const (
	OpRead     Operation = "read"
	OpWrite    Operation = "write"
	OpDelete   Operation = "delete"
	OpRename   Operation = "rename"
	OpRefactor Operation = "refactor"
)

type Outcome string

const (
	OutcomeSucceeded  Outcome = "succeeded"
	OutcomeAbandoned  Outcome = "abandoned"
	OutcomeConflicted Outcome = "conflicted"
)

type Conflict struct {
	ClaimID   string    `json:"claim_id"`
	AgentID   string    `json:"agent_id"`
	Resource  string    `json:"resource"`
	Operation Operation `json:"operation"`
	Intent    string    `json:"intent"`
	Since     time.Time `json:"since"`
}

type ConflictReport struct {
	HasConflict bool       `json:"has_conflict"`
	Conflicts   []Conflict `json:"conflicts,omitempty"`
}

func CheckConflict(claim Claim, active []Claim) *ConflictReport {
	var conflicts []Conflict
	for _, a := range active {
		if a.AgentID == claim.AgentID {
			continue
		}
		if isConflicting(claim.ResourceURI, claim.Operation, a) {
			conflicts = append(conflicts, Conflict{
				ClaimID:   a.ClaimID,
				AgentID:   a.AgentID,
				Resource:  a.ResourceURI,
				Operation: a.Operation,
				Intent:    a.Intent,
				Since:     a.CreatedAt,
			})
		}
	}
	return &ConflictReport{
		HasConflict: len(conflicts) > 0,
		Conflicts:   conflicts,
	}
}

func isConflicting(resource string, op Operation, other Claim) bool {
	if !resourceMatches(resource, other.ResourceURI) {
		return false
	}

	switch op {
	case OpWrite:
		return other.Operation == OpWrite || other.Operation == OpDelete ||
			other.Operation == OpRename || other.Operation == OpRefactor
	case OpDelete:
		return other.Operation == OpRead || other.Operation == OpWrite ||
			other.Operation == OpDelete || other.Operation == OpRename || other.Operation == OpRefactor
	case OpRename:
		return true
	case OpRefactor:
		return true
	case OpRead:
		return other.Operation == OpDelete || other.Operation == OpRename
	}
	return false
}

func resourceMatches(a, b string) bool {
	if a == b {
		return true
	}
	// Check if a is a prefix pattern that covers b
	if strings.HasSuffix(a, "/**") {
		prefix := strings.TrimSuffix(a, "/**")
		return strings.HasPrefix(b, prefix+"/")
	}
	// Check if b is a prefix pattern that covers a
	if strings.HasSuffix(b, "/**") {
		prefix := strings.TrimSuffix(b, "/**")
		return strings.HasPrefix(a, prefix+"/")
	}
	// Check glob patterns
	if strings.Contains(a, "*") {
		ok, _ := path.Match(a, b)
		return ok
	}
	if strings.Contains(b, "*") {
		ok, _ := path.Match(b, a)
		return ok
	}
	return false
}

// ResourceMatchesPattern checks if resourceURI matches the given pattern.
// Empty pattern or "*" matches everything.
func ResourceMatchesPattern(pattern, resourceURI string) bool {
	if pattern == "" || pattern == "*" || resourceURI == "" || resourceURI == "*" {
		return true
	}
	return resourceMatches(pattern, resourceURI)
}

// Error types

type ConflictError struct {
	Report ConflictReport
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("conflict detected: %d active claims", len(e.Report.Conflicts))
}

type ExpiredError struct {
	ClaimID string
}

func (e *ExpiredError) Error() string {
	return fmt.Sprintf("claim %s expired", e.ClaimID)
}

type NotFoundError struct {
	Resource string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("not found: %s", e.Resource)
}

type ProviderError struct {
	Cause error
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("provider error: %v", e.Cause)
}
