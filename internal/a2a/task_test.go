package a2a

import "testing"

func TestTaskTransition(t *testing.T) {
	task := NewTask("t1", "s1", "ns")
	if task.Status.State != TaskSubmitted { t.Fatal("expected submitted") }

	if err := task.Transition(TaskWorking); err != nil { t.Fatal(err) }
	if task.Status.State != TaskWorking { t.Fatal("expected working") }

	if err := task.Transition(TaskCompleted); err != nil { t.Fatal(err) }
	if task.Status.State != TaskCompleted { t.Fatal("expected completed") }
}

func TestTaskInvalidTransition(t *testing.T) {
	task := NewTask("t1", "s1", "ns")
	if err := task.Transition(TaskCompleted); err == nil {
		t.Fatal("expected error for submitted -> completed")
	}
}
