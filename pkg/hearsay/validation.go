package hearsay

import (
	"fmt"
	"strings"
)

const (
	MaxAgentIDLen     = 128
	MaxResourceURILen = 2048
	MaxIntentLen      = 512
	MaxNamespaceLen   = 64
)

type ValidationError struct {
	Field   string
	Message string
}

func (v *ValidationError) Error() string {
	return fmt.Sprintf("validation error on %s: %s", v.Field, v.Message)
}

func ValidateAgentID(id string) error {
	if id == "" {
		return &ValidationError{Field: "agent_id", Message: "required"}
	}
	if len(id) > MaxAgentIDLen {
		return &ValidationError{Field: "agent_id", Message: fmt.Sprintf("max length %d", MaxAgentIDLen)}
	}
	return nil
}

func ValidateResourceURI(uri string) error {
	if uri == "" {
		return &ValidationError{Field: "resource_uri", Message: "required"}
	}
	if len(uri) > MaxResourceURILen {
		return &ValidationError{Field: "resource_uri", Message: fmt.Sprintf("max length %d", MaxResourceURILen)}
	}
	if !strings.Contains(uri, "://") {
		return &ValidationError{Field: "resource_uri", Message: "must contain scheme (e.g. file://)"}
	}
	return nil
}

func ValidateIntent(intent string) error {
	if len(intent) > MaxIntentLen {
		return &ValidationError{Field: "intent", Message: fmt.Sprintf("max length %d", MaxIntentLen)}
	}
	return nil
}

func ValidateNamespace(ns string) error {
	if ns == "" {
		return &ValidationError{Field: "namespace", Message: "required"}
	}
	if len(ns) > MaxNamespaceLen {
		return &ValidationError{Field: "namespace", Message: fmt.Sprintf("max length %d", MaxNamespaceLen)}
	}
	return nil
}
