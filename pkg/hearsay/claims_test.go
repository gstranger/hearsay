package hearsay

import (
	"testing"
	"time"
)

func TestCheckConflict_NoConflict(t *testing.T) {
	claim := Claim{AgentID: "a1", ResourceURI: "file://x.ts", Operation: OpRead}
	active := []Claim{
		{AgentID: "a2", ResourceURI: "file://y.ts", Operation: OpWrite, CreatedAt: time.Now()},
	}
	report := CheckConflict(claim, active)
	if report.HasConflict {
		t.Fatalf("expected no conflict")
	}
}

func TestCheckConflict_ReadVsWrite_NoConflict(t *testing.T) {
	claim := Claim{AgentID: "a1", ResourceURI: "file://x.ts", Operation: OpRead}
	active := []Claim{
		{AgentID: "a2", ResourceURI: "file://x.ts", Operation: OpWrite, CreatedAt: time.Now()},
	}
	report := CheckConflict(claim, active)
	if report.HasConflict {
		t.Fatalf("read should NOT conflict with write (optimistic)")
	}
}

func TestCheckConflict_SameAgentIgnored(t *testing.T) {
	claim := Claim{AgentID: "a1", ResourceURI: "file://x.ts", Operation: OpWrite}
	active := []Claim{
		{AgentID: "a1", ResourceURI: "file://x.ts", Operation: OpWrite, CreatedAt: time.Now()},
	}
	report := CheckConflict(claim, active)
	if report.HasConflict {
		t.Fatalf("expected no conflict with same agent")
	}
}

func TestCheckConflict_RefactorConflictsWithEverything(t *testing.T) {
	claim := Claim{AgentID: "a1", ResourceURI: "file://x.ts", Operation: OpRefactor}
	active := []Claim{
		{AgentID: "a2", ResourceURI: "file://x.ts", Operation: OpRead, CreatedAt: time.Now()},
	}
	report := CheckConflict(claim, active)
	if !report.HasConflict {
		t.Fatalf("expected refactor to conflict with read")
	}
}

func TestCheckConflict_ReadVsDelete_Conflict(t *testing.T) {
	claim := Claim{AgentID: "a1", ResourceURI: "file://x.ts", Operation: OpRead}
	active := []Claim{
		{AgentID: "a2", ResourceURI: "file://x.ts", Operation: OpDelete, CreatedAt: time.Now()},
	}
	report := CheckConflict(claim, active)
	if !report.HasConflict {
		t.Fatalf("read SHOULD conflict with delete")
	}
}

func TestResourceMatches_Exact(t *testing.T) {
	if !resourceMatches("file://x.ts", "file://x.ts") {
		t.Fatal("expected exact match")
	}
}

func TestResourceMatches_Prefix(t *testing.T) {
	if !resourceMatches("file://dir/**", "file://dir/sub/x.ts") {
		t.Fatal("expected prefix match")
	}
}

func TestResourceMatches_NoPrefix(t *testing.T) {
	if resourceMatches("file://dir/**", "file://other/x.ts") {
		t.Fatal("expected no match")
	}
}
