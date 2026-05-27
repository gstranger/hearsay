package server

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

type AuditLogger struct {
	provider  hearsay.Provider
	level     hearsay.AuditLevel
	namespace string
	ch        chan hearsay.AuditEvent
}

func NewAuditLogger(provider hearsay.Provider, level hearsay.AuditLevel, namespace string) *AuditLogger {
	a := &AuditLogger{
		provider:  provider,
		level:     level,
		namespace: namespace,
		ch:        make(chan hearsay.AuditEvent, 256),
	}
	if level > hearsay.AuditOff {
		go a.worker()
	}
	return a
}

func (a *AuditLogger) Log(eventType, agentID, resourceURI, outcome string, metadata map[string]string) {
	if a.level == hearsay.AuditOff {
		return
	}
	if !hearsay.ShouldLog(a.level, eventType) {
		return
	}

	meta := ""
	if len(metadata) > 0 {
		b, _ := json.Marshal(metadata)
		meta = string(b)
	}

	event := hearsay.AuditEvent{
		Namespace:   a.namespace,
		Timestamp:   time.Now().UTC(),
		Level:       levelName(a.level),
		EventType:   eventType,
		AgentID:     agentID,
		ResourceURI: resourceURI,
		Outcome:     outcome,
		Metadata:    meta,
	}

	select {
	case a.ch <- event:
	default:
		log.Printf("audit: dropping event (channel full)")
	}
}

func (a *AuditLogger) worker() {
	ctx := context.Background()
	batch := make([]hearsay.AuditEvent, 0, 32)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case event := <-a.ch:
			batch = append(batch, event)
			if len(batch) >= 32 {
				a.flush(ctx, batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				a.flush(ctx, batch)
				batch = batch[:0]
			}
		}
	}
}

func (a *AuditLogger) flush(ctx context.Context, batch []hearsay.AuditEvent) {
	if err := a.provider.AppendAudit(ctx, a.namespace, batch); err != nil {
		log.Printf("audit: flush error: %v", err)
	}
}

func levelName(level hearsay.AuditLevel) string {
	switch level {
	case hearsay.AuditCoordination:
		return "coordination"
	case hearsay.AuditSecurity:
		return "security"
	case hearsay.AuditFull:
		return "full"
	default:
		return "off"
	}
}