package a2a

import (
	"context"
	"encoding/json"

	"github.com/thunder/agentstate/pkg/agentstate"
)

type taskParams struct {
	ID        string         `json:"id"`
	SessionID string         `json:"sessionId,omitempty"`
	Message   Message        `json:"message"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

func (s *Server) handleTasksSend(ctx context.Context, params json.RawMessage) (*JSONRPCResponse, error) {
	var req taskParams
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewError(-32602, "Invalid params: "+err.Error())
	}
	if req.ID == "" {
		return nil, NewError(-32602, "Missing required param: id")
	}

	task := NewTask(req.ID, req.SessionID, s.namespace)

	// Store task
	if err := s.provider.CreateTask(ctx, s.namespace, taskToStorage(task)); err != nil {
		return nil, NewError(-32003, "Failed to create task: "+err.Error())
	}

	// Append user message to history
	msgJSON, _ := json.Marshal(req.Message)
	if err := s.provider.AppendTaskHistory(ctx, s.namespace, task.ID, 0, agentstate.A2AMessage{
		Role:  "user",
		Parts: msgJSON,
	}); err != nil {
		return nil, NewError(-32003, "Failed to store history: "+err.Error())
	}

	// Transition to working
	if err := task.Transition(TaskWorking); err != nil {
		return nil, NewError(-32003, "State transition failed: "+err.Error())
	}

	// Execute skill based on message content
	result, err := s.executeSkill(ctx, task, req.Message)
	if err != nil {
		_ = task.Transition(TaskFailed)
		_ = s.provider.UpdateTask(ctx, s.namespace, taskToStorage(task))
		return NewResponse(req.ID, taskToJSON(task)), nil
	}

	// Transition to completed
	if err := task.Transition(TaskCompleted); err != nil {
		return nil, NewError(-32003, "State transition failed: "+err.Error())
	}

	// Store artifacts
	for _, art := range result.Artifacts {
		partsJSON, err := json.Marshal(art.Parts)
		if err != nil {
			return nil, NewError(-32003, "Failed to marshal artifact: "+err.Error())
		}
		if err := s.provider.CreateArtifact(ctx, s.namespace, task.ID, agentstate.A2AArtifact{
			Name: art.Name, Description: art.Description, Parts: partsJSON,
			Index: art.Index, Append: art.Append, LastChunk: art.LastChunk,
		}); err != nil {
			return nil, NewError(-32003, "Failed to store artifact: "+err.Error())
		}
	}

	// Update task in storage
	if err := s.provider.UpdateTask(ctx, s.namespace, taskToStorage(task)); err != nil {
		return nil, NewError(-32003, "Failed to update task: "+err.Error())
	}

	// Populate task with artifacts for response
	task.Artifacts = result.Artifacts

	return NewResponse(req.ID, taskToJSON(task)), nil
}

func (s *Server) handleTasksGet(ctx context.Context, params json.RawMessage) (*JSONRPCResponse, error) {
	var req struct{ ID string `json:"id"` }
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewError(-32602, "Invalid params")
	}
	if req.ID == "" {
		return nil, NewError(-32602, "Missing required param: id")
	}

	st, err := s.provider.GetTask(ctx, s.namespace, req.ID)
	if err != nil {
		return nil, NewError(-32000, "Task not found")
	}

	task := storageToTask(st)
	// Load artifacts
	arts, err := s.provider.GetArtifacts(ctx, s.namespace, req.ID)
	if err != nil {
		return nil, NewError(-32003, "Failed to load artifacts: "+err.Error())
	}
	for _, a := range arts {
		var parts []Part
		json.Unmarshal(a.Parts, &parts)
		task.Artifacts = append(task.Artifacts, Artifact{Name: a.Name, Description: a.Description, Parts: parts})
	}

	return NewResponse(req.ID, taskToJSON(task)), nil
}

func (s *Server) handleTasksCancel(ctx context.Context, params json.RawMessage) (*JSONRPCResponse, error) {
	var req struct{ ID string `json:"id"` }
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewError(-32602, "Invalid params")
	}
	if req.ID == "" {
		return nil, NewError(-32602, "Missing required param: id")
	}

	st, err := s.provider.GetTask(ctx, s.namespace, req.ID)
	if err != nil {
		return nil, NewError(-32000, "Task not found")
	}

	if st.State == string(TaskCompleted) || st.State == string(TaskFailed) || st.State == string(TaskCanceled) {
		return nil, NewError(-32001, "Task already in terminal state", map[string]string{"currentState": st.State})
	}

	task := storageToTask(st)
	if task.ClaimID != "" {
		if err := s.client.Release(ctx, task.ClaimID, agentstate.OutcomeAbandoned); err != nil {
			// Log warning but proceed with cancel — release failure shouldn't prevent cancel
			_ = err
		}
	}
	if err := task.Transition(TaskCanceled); err != nil {
		return nil, NewError(-32001, "Task already in terminal state", map[string]string{"currentState": st.State})
	}
	if err := s.provider.UpdateTask(ctx, s.namespace, taskToStorage(task)); err != nil {
		return nil, NewError(-32003, "Failed to update task: "+err.Error())
	}

	return NewResponse(req.ID, taskToJSON(task)), nil
}
