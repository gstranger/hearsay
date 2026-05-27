package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCursorStateFilePath(t *testing.T) {
	path := cursorStateFilePath("sess_abc123")
	want := filepath.Join(os.TempDir(), "agentstate-cursor-sess_abc123.json")
	if path != want {
		t.Fatalf("got %q, want %q", path, want)
	}
}
