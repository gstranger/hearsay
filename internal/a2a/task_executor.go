package a2a

import (
	"context"
)

// TaskEvent represents a single event in a task execution stream.
type TaskEvent struct {
	Type      string        // "status", "artifact", "done", "error"
	Task      *Task         // task at point of event (for status/artifact/done)
	Artifacts []Artifact    // for "artifact" events
	Error     *JSONRPCError // for "error" events
}

// TaskExecutor runs executeSkill in a goroutine and streams progress through a channel.
// The caller reads from Events() and handles framing (SSE, logging, etc.).
type TaskExecutor struct {
	task   *Task
	events chan TaskEvent
	ctx    context.Context
}

// Events returns the event channel. It is closed when execution completes or fails.
func (e *TaskExecutor) Events() <-chan TaskEvent {
	return e.events
}

// StartTaskExecution launches executeSkill in a goroutine. It sends status updates
// for state transitions, artifacts when the skill produces output, and a final "done"
// or "error" event. Callers read from executor.Events() to receive progress.
func StartTaskExecution(ctx context.Context, task *Task, msg Message, srv *Server) *TaskExecutor {
	exec := &TaskExecutor{
		task:   task,
		events: make(chan TaskEvent, 8), // buffered so the goroutine doesn't block on fast sequences
		ctx:    ctx,
	}

	go func() {
		defer close(exec.events)

		// Helper to send a snapshot of the task (avoid pointer aliasing)
		sendStatus := func(typ string) bool {
			snapshot := *task
			return exec.send(TaskEvent{Type: typ, Task: &snapshot})
		}

		// Emit submitted (blocks on ctx if cancelled)
		if !sendStatus("status") {
			// Context already cancelled — emit error event directly (don't use send)
			snapshot := *task
			exec.events <- TaskEvent{
				Type:  "error",
				Task:  &snapshot,
				Error: NewError(-32003, "Task cancelled: "+ctx.Err().Error()),
			}
			return
		}

		// Transition to working
		if err := task.Transition(TaskWorking); err != nil {
			exec.send(TaskEvent{
				Type:  "error",
				Error: NewError(-32003, "State transition failed: "+err.Error()),
			})
			return
		}
		if !sendStatus("status") {
			return
		}

		// Check context before executing
		if ctx.Err() != nil {
			_ = task.Transition(TaskFailed)
			snapshot := *task
			exec.send(TaskEvent{
				Type:  "error",
				Task:  &snapshot,
				Error: NewError(-32003, "Task cancelled: "+ctx.Err().Error()),
			})
			return
		}

		// Execute skill
		result, err := srv.executeSkill(ctx, task, msg)
		if err != nil {
			_ = task.Transition(TaskFailed)
			snapshot := *task
			if rpcErr, ok := err.(*JSONRPCError); ok {
				exec.send(TaskEvent{Type: "error", Task: &snapshot, Error: rpcErr})
			} else {
				exec.send(TaskEvent{
					Type:  "error",
					Task:  &snapshot,
					Error: NewError(-32003, err.Error()),
				})
			}
			return
		}

		// Emit artifacts
		if len(result.Artifacts) > 0 {
			task.Artifacts = result.Artifacts
			snapshot := *task
			if !exec.send(TaskEvent{Type: "artifact", Task: &snapshot, Artifacts: result.Artifacts}) {
				return
			}
		}

		// Transition to completed
		if err := task.Transition(TaskCompleted); err != nil {
			exec.send(TaskEvent{
				Type:  "error",
				Error: NewError(-32003, "State transition failed: "+err.Error()),
			})
			return
		}

		// Emit completed status then done
		if !sendStatus("status") {
			return
		}
		snapshot := *task
		exec.send(TaskEvent{Type: "done", Task: &snapshot})
	}()

	return exec
}

// send sends a TaskEvent on the channel, respecting context cancellation.
// Returns false if the context is cancelled (caller should stop).
func (e *TaskExecutor) send(evt TaskEvent) bool {
	select {
	case <-e.ctx.Done():
		return false
	case e.events <- evt:
		return true
	}
}