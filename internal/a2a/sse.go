package a2a

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// SSEWriter writes A2A SSE (Server-Sent Events) frames to an HTTP response.
// Each method wraps the payload in a JSON-RPC 2.0 response and writes it as
// an SSE event:line followed by a data:line, then flushes.
type SSEWriter struct {
	w         http.ResponseWriter
	flusher   http.Flusher
	requestID any
}

// WriteStatusUpdate sends a task-status-update event.
func (s *SSEWriter) WriteStatusUpdate(taskJSON map[string]any) error {
	return s.writeEvent("task-status-update", taskJSON)
}

// WriteArtifactUpdate sends a task-artifact-update event.
func (s *SSEWriter) WriteArtifactUpdate(taskJSON map[string]any) error {
	return s.writeEvent("task-artifact-update", taskJSON)
}

// WriteClose sends the terminal close event and flushes.
func (s *SSEWriter) WriteClose(taskJSON map[string]any) error {
	return s.writeEvent("close", taskJSON)
}

// WriteError sends an error as a raw SSE data frame (no event: prefix, per A2A convention).
func (s *SSEWriter) WriteError(e *JSONRPCError) error {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      s.requestID,
		Error:   e,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal error response: %w", err)
	}
	return s.writeFrame(data)
}

func (s *SSEWriter) writeEvent(eventType string, result any) error {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      s.requestID,
		Result:  result,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal event response: %w", err)
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\n", eventType); err != nil {
		return err
	}
	return s.writeFrame(data)
}

func (s *SSEWriter) writeFrame(data []byte) error {
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", data); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}