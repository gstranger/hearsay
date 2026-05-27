package a2a

import (
	"bufio"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeFlusher wraps httptest.ResponseRecorder to implement http.Flusher
type fakeFlusher struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *fakeFlusher) Flush() { f.flushed = true }

func TestSSEWriterStatusUpdate(t *testing.T) {
	rec := httptest.NewRecorder()
	fw := &fakeFlusher{ResponseRecorder: rec}
	sse := &SSEWriter{w: fw, flusher: fw, requestID: 42}

	err := sse.WriteStatusUpdate(map[string]any{"id": "task-1", "status": map[string]any{"state": "working"}})
	if err != nil {
		t.Fatal(err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: task-status-update") {
		t.Fatalf("missing event type line, got: %s", body)
	}
	if !strings.Contains(body, `"jsonrpc":"2.0"`) {
		t.Fatalf("missing jsonrpc field, got: %s", body)
	}
	if !strings.Contains(body, `"id":42`) {
		t.Fatalf("missing request id, got: %s", body)
	}
	if !fw.flushed {
		t.Fatal("expected Flush() to be called")
	}
}

func TestSSEWriterArtifactUpdate(t *testing.T) {
	rec := httptest.NewRecorder()
	fw := &fakeFlusher{ResponseRecorder: rec}
	sse := &SSEWriter{w: fw, flusher: fw, requestID: "req-abc"}

	err := sse.WriteArtifactUpdate(map[string]any{"id": "task-1", "artifacts": []map[string]any{{"name": "result"}}})
	if err != nil {
		t.Fatal(err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: task-artifact-update") {
		t.Fatalf("missing event type line, got: %s", body)
	}
	if !strings.Contains(body, `"id":"req-abc"`) {
		t.Fatalf("missing request id, got: %s", body)
	}
}

func TestSSEWriterClose(t *testing.T) {
	rec := httptest.NewRecorder()
	fw := &fakeFlusher{ResponseRecorder: rec}
	sse := &SSEWriter{w: fw, flusher: fw, requestID: 1}

	err := sse.WriteClose(map[string]any{"id": "task-1", "status": map[string]any{"state": "completed"}})
	if err != nil {
		t.Fatal(err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: close") {
		t.Fatalf("missing close event line, got: %s", body)
	}
	if !strings.Contains(body, `"state":"completed"`) {
		t.Fatalf("missing completed state, got: %s", body)
	}
}

func TestSSEWriterError(t *testing.T) {
	rec := httptest.NewRecorder()
	fw := &fakeFlusher{ResponseRecorder: rec}
	sse := &SSEWriter{w: fw, flusher: fw, requestID: 1}

	err := sse.WriteError(NewError(-32002, "Conflict detected"))
	if err != nil {
		t.Fatal(err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `"code":-32002`) {
		t.Fatalf("missing error code, got: %s", body)
	}
	if !strings.Contains(body, `"message":"Conflict detected"`) {
		t.Fatalf("missing error message, got: %s", body)
	}
}

func TestSSEWriterMultipleEvents(t *testing.T) {
	rec := httptest.NewRecorder()
	fw := &fakeFlusher{ResponseRecorder: rec}
	sse := &SSEWriter{w: fw, flusher: fw, requestID: "x"}

	sse.WriteStatusUpdate(map[string]any{"status": map[string]any{"state": "working"}})
	sse.WriteArtifactUpdate(map[string]any{"artifacts": []map[string]any{{"name": "a"}}})
	sse.WriteClose(map[string]any{"status": map[string]any{"state": "completed"}})

	body := rec.Body.String()
	lines := strings.Split(body, "\n")
	eventCount := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "event: ") {
			eventCount++
		}
	}
	if eventCount != 3 {
		t.Fatalf("expected 3 event lines, got %d in body:\n%s", eventCount, body)
	}
}

func TestSSEWriterBodyFormat(t *testing.T) {
	rec := httptest.NewRecorder()
	fw := &fakeFlusher{ResponseRecorder: rec}
	sse := &SSEWriter{w: fw, flusher: fw, requestID: 1}

	sse.WriteStatusUpdate(map[string]any{"id": "t1"})

	body := rec.Body.String()
	scanner := bufio.NewScanner(strings.NewReader(body))
	hasData := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			hasData = true
			jsonStr := strings.TrimPrefix(line, "data: ")
			if !strings.HasPrefix(jsonStr, "{") || !strings.HasSuffix(jsonStr, "}") {
				t.Fatalf("data line is not valid JSON object: %s", jsonStr)
			}
		}
	}
	if !hasData {
		t.Fatal("no data: line found in SSE output")
	}
}