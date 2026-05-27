package watcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gstranger/hearsay/internal/memory"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

func TestWatcherDetectsFileChange(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(file, []byte("hello"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	mem := memory.New()
	if err := mem.CreateNamespace(context.Background(), hearsay.Namespace{ID: "watch-test", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	client := hearsay.NewClient(mem, "watch-test")

	w := New(Config{
		Path:      dir,
		Namespace: "watch-test",
		Client:    client,
		ClaimTTL:  60,
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- w.Run(ctx)
	}()

	// Modify file
	time.Sleep(200 * time.Millisecond)
	if err := os.WriteFile(file, []byte("changed"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Wait for claim to propagate
	time.Sleep(200 * time.Millisecond)
	cancel()
	if err := <-errCh; err != nil && err != context.Canceled {
		t.Fatalf("watcher error: %v", err)
	}

	// Verify retroactive claim
	claims, err := mem.ActiveClaims(context.Background(), "watch-test", "file://"+file)
	if err != nil {
		t.Fatalf("ActiveClaims: %v", err)
	}
	if len(claims) == 0 {
		t.Fatal("expected retroactive claim for file change, got none")
	}
	if claims[0].AgentID != "watcher:auto" {
		t.Fatalf("expected agent 'watcher:auto', got %s", claims[0].AgentID)
	}
	if claims[0].Operation != hearsay.OpWrite {
		t.Fatalf("expected OpWrite, got %s", claims[0].Operation)
	}
}

func TestWatcherIsIncludedAndExcluded(t *testing.T) {
	w := New(Config{
		Include: []string{"*.ts"},
		Exclude: []string{"node_modules", "*.log"},
	})

	if !w.isIncluded("foo.ts") {
		t.Error("expected foo.ts to be included")
	}
	if w.isIncluded("foo.js") {
		t.Error("expected foo.js to be excluded")
	}

	if !w.isExcluded("node_modules") {
		t.Error("expected node_modules to be excluded")
	}
	if !w.isExcluded("debug.log") {
		t.Error("expected debug.log to be excluded")
	}
}
