package server

import (
	"testing"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

func TestShouldLog_Off(t *testing.T) {
	if hearsay.ShouldLog(hearsay.AuditOff, hearsay.AuditEventClaim) {
		t.Fatal("should not log at off level")
	}
}

func TestShouldLog_Coordination(t *testing.T) {
	if !hearsay.ShouldLog(hearsay.AuditCoordination, hearsay.AuditEventClaim) {
		t.Fatal("should log claim at coordination level")
	}
	if !hearsay.ShouldLog(hearsay.AuditCoordination, hearsay.AuditEventRelease) {
		t.Fatal("should log release at coordination level")
	}
	if hearsay.ShouldLog(hearsay.AuditCoordination, hearsay.AuditEventAuthFailure) {
		t.Fatal("should NOT log auth_failure at coordination level")
	}
}

func TestShouldLog_Security(t *testing.T) {
	if !hearsay.ShouldLog(hearsay.AuditSecurity, hearsay.AuditEventClaim) {
		t.Fatal("should log claim at security level")
	}
	if !hearsay.ShouldLog(hearsay.AuditSecurity, hearsay.AuditEventAuthFailure) {
		t.Fatal("should log auth_failure at security level")
	}
	if !hearsay.ShouldLog(hearsay.AuditSecurity, hearsay.AuditEventRateLimit) {
		t.Fatal("should log rate_limit at security level")
	}
	if hearsay.ShouldLog(hearsay.AuditSecurity, hearsay.AuditEventHeartbeat) {
		t.Fatal("should NOT log heartbeat at security level")
	}
}

func TestShouldLog_Full(t *testing.T) {
	if !hearsay.ShouldLog(hearsay.AuditFull, hearsay.AuditEventHeartbeat) {
		t.Fatal("should log heartbeat at full level")
	}
}