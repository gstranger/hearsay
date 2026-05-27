package hearsay

import "time"

type AuditLevel int

const (
	AuditOff          AuditLevel = 0
	AuditCoordination AuditLevel = 1
	AuditSecurity     AuditLevel = 2
	AuditFull         AuditLevel = 3
)

func ParseAuditLevel(s string) AuditLevel {
	switch s {
	case "coordination":
		return AuditCoordination
	case "security":
		return AuditSecurity
	case "full":
		return AuditFull
	default:
		return AuditOff
	}
}

type AuditEvent struct {
	Namespace   string    `json:"namespace"`
	Timestamp   time.Time `json:"timestamp"`
	Level       string    `json:"level"`
	EventType   string    `json:"event_type"`
	AgentID     string    `json:"agent_id,omitempty"`
	ResourceURI string    `json:"resource_uri,omitempty"`
	Outcome     string    `json:"outcome,omitempty"`
	Metadata    string    `json:"metadata,omitempty"`
}

const (
	AuditEventClaim           = "claim"
	AuditEventRelease         = "release"
	AuditEventExpire          = "expire"
	AuditEventDeath           = "death_release"
	AuditEventAuthFailure     = "auth_failure"
	AuditEventRateLimit       = "rate_limit"
	AuditEventHeartbeat       = "heartbeat"
	AuditEventMessageSend     = "message_send"
	AuditEventNamespaceCreate = "namespace_create"
	AuditEventNamespaceDelete = "namespace_delete"
	AuditEventTaskCreate      = "task_create"
	AuditEventTaskComplete    = "task_complete"
	AuditEventTaskCancel      = "task_cancel"
)

func ShouldLog(level AuditLevel, eventType string) bool {
	switch {
	case level >= AuditFull:
		return true
	case level >= AuditSecurity:
		return eventType == AuditEventClaim ||
			eventType == AuditEventRelease ||
			eventType == AuditEventExpire ||
			eventType == AuditEventDeath ||
			eventType == AuditEventAuthFailure ||
			eventType == AuditEventRateLimit
	case level >= AuditCoordination:
		return eventType == AuditEventClaim ||
			eventType == AuditEventRelease ||
			eventType == AuditEventExpire ||
			eventType == AuditEventDeath
	default:
		return false
	}
}