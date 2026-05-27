//go:build !wasm

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCursorStateFilePath(t *testing.T) {
	path := cursorStateFilePath("sess_abc123")
	if !strings.Contains(path, "hearsay-cursor-sess_abc123.json") {
		t.Fatalf("got %q, expected path to contain hearsay-cursor-sess_abc123.json", path)
	}
	// Should be under a persistent config dir, not /tmp
	tempDir := filepath.Join(os.TempDir(), "hearsay-cursor-sess_abc123.json")
	if path == tempDir {
		t.Fatal("cursor state path should not be in /tmp")
	}
}

func TestCursorStatePath(t *testing.T) {
	path := cursorStateFilePath("sess_test")
	if !strings.Contains(path, "hearsay") || !strings.Contains(path, "cursor") {
		t.Fatalf("unexpected cursor state path: %s", path)
	}
	dir := cursorStateDir()
	if dir == "" {
		t.Fatal("cursor state dir should not be empty")
	}
}
