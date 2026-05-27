package a2a

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

type Task struct {
	ID        string                 `json:"id"`
	SessionID string                 `json:"sessionId,omitempty"`
	Status    TaskStatus             `json:"status"`
	History   []Message              `json:"history,omitempty"`
	Artifacts []Artifact             `json:"artifacts,omitempty"`
	Metadata  map[string]any         `json:"metadata,omitempty"`

	// Internal: maps to hearsay
	ClaimID   string `json:"-"`
	Namespace string `json:"-"`
}

type TaskStatus struct {
	State     TaskState  `json:"state"`
	Message   *Message   `json:"message,omitempty"`
	Timestamp time.Time  `json:"timestamp"`
}

type TaskState string

const (
	TaskSubmitted     TaskState = "submitted"
	TaskWorking       TaskState = "working"
	TaskInputRequired TaskState = "input-required"
	TaskCompleted     TaskState = "completed"
	TaskFailed        TaskState = "failed"
	TaskCanceled      TaskState = "canceled"
	TaskUnknown       TaskState = "unknown"
)

type Message struct {
	Role     string          `json:"role"`
	Parts    []Part          `json:"parts"`
	Metadata map[string]any  `json:"metadata,omitempty"`
}

type Part struct {
	Type string          `json:"type"`
	Text string          `json:"text,omitempty"`
	File *FileData       `json:"file,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

type FileData struct {
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Bytes    string `json:"bytes,omitempty"`
	URI      string `json:"uri,omitempty"`
}

type Artifact struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parts       []Part         `json:"parts"`
	Index       int            `json:"index,omitempty"`
	Append      bool           `json:"append,omitempty"`
	LastChunk   bool           `json:"lastChunk,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

func (t *Task) Transition(to TaskState) error {
	valid := map[TaskState][]TaskState{
		TaskSubmitted:     {TaskWorking, TaskInputRequired, TaskFailed, TaskCanceled},
		TaskWorking:       {TaskInputRequired, TaskCompleted, TaskFailed, TaskCanceled},
		TaskInputRequired: {TaskWorking, TaskCanceled},
	}
	allowed, ok := valid[t.Status.State]
	if !ok { return fmt.Errorf("invalid current state: %s", t.Status.State) }
	for _, s := range allowed {
		if s == to {
			t.Status.State = to
			t.Status.Timestamp = time.Now()
			return nil
		}
	}
	return fmt.Errorf("invalid transition: %s -> %s", t.Status.State, to)
}

func NewTask(id, sessionID, namespace string) *Task {
	return &Task{
		ID:        id,
		SessionID: sessionID,
		Namespace: namespace,
		Status: TaskStatus{
			State:     TaskSubmitted,
			Timestamp: time.Now(),
		},
		Metadata: map[string]any{},
	}
}

func taskToStorage(t *Task) *hearsay.A2ATask {
	statusMsg, _ := json.Marshal(t.Status.Message)
	metadata, _ := json.Marshal(t.Metadata)
	return &hearsay.A2ATask{
		ID: t.ID, SessionID: t.SessionID, State: string(t.Status.State),
		StatusMsg: statusMsg, StatusTime: t.Status.Timestamp,
		ClaimID: t.ClaimID, Namespace: t.Namespace, Metadata: metadata,
		CreatedAt: time.Now(),
	}
}

func storageToTask(st *hearsay.A2ATask) *Task {
	var msg *Message
	if len(st.StatusMsg) > 0 {
		json.Unmarshal(st.StatusMsg, &msg)
	}
	var metadata map[string]any
	if len(st.Metadata) > 0 {
		json.Unmarshal(st.Metadata, &metadata)
	}
	return &Task{
		ID: st.ID, SessionID: st.SessionID,
		Status:    TaskStatus{State: TaskState(st.State), Message: msg, Timestamp: st.StatusTime},
		ClaimID:   st.ClaimID,
		Namespace: st.Namespace,
		Metadata:  metadata,
	}
}

func taskToJSON(t *Task) map[string]any {
	data, _ := json.Marshal(t)
	var out map[string]any
	json.Unmarshal(data, &out)
	return out
}
