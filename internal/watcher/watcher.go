package watcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

// Config configures the filesystem watcher.
type Config struct {
	Path         string
	Namespace    string
	PollInterval time.Duration
	ClaimTTL     int
	Include      []string
	Exclude      []string
	Client       *hearsay.Client
}

// Watcher detects file changes and creates retroactive claims via hearsay.
type Watcher struct {
	cfg Config
}

// New creates a new filesystem watcher with optional config defaults.
func New(cfg Config) *Watcher {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.ClaimTTL == 0 {
		cfg.ClaimTTL = 60
	}
	return &Watcher{cfg: cfg}
}

// Run starts watching the configured path and blocks until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create fsnotify watcher: %w", err)
	}
	defer watcher.Close()

	// Walk path and add all subdirectories
	err = filepath.Walk(w.cfg.Path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Skip excluded directories
		if info.IsDir() && w.isExcluded(path) {
			return filepath.SkipDir
		}
		// Watch the directory itself (not individual files)
		if info.IsDir() {
			if err := watcher.Add(path); err != nil {
				return fmt.Errorf("watch %s: %w", path, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk path: %w", err)
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			// Translate fsnotify operations to hearsay operations
			var op hearsay.Operation
			switch {
			case event.Has(fsnotify.Write) || event.Has(fsnotify.Create):
				op = hearsay.OpWrite
			case event.Has(fsnotify.Remove):
				op = hearsay.OpDelete
			case event.Has(fsnotify.Rename):
				op = hearsay.OpRename
			default:
				continue
			}
			if w.isIncluded(event.Name) && !w.isExcluded(event.Name) {
				w.handleChange(ctx, event.Name, op)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			return fmt.Errorf("fsnotify error: %w", err)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (w *Watcher) handleChange(ctx context.Context, path string, op hearsay.Operation) {
	uri := "file://" + path
	intent := fmt.Sprintf("auto-detected: file changed at %s", time.Now().Format(time.RFC3339))

	req := hearsay.ClaimRequest{
		AgentID:     "watcher:auto",
		ResourceURI: uri,
		Operation:   op,
		Intent:      intent,
		TTLSeconds:  w.cfg.ClaimTTL,
	}

	if _, err := w.cfg.Client.Claim(ctx, req); err != nil {
		// Watcher never blocks: log and continue
		fmt.Fprintf(os.Stderr, "[watcher] claim failed for %s: %v\n", path, err)
	}
}

func (w *Watcher) isIncluded(path string) bool {
	if len(w.cfg.Include) == 0 {
		return true
	}
	basename := filepath.Base(path)
	for _, pattern := range w.cfg.Include {
		matched, _ := filepath.Match(pattern, basename)
		if matched {
			return true
		}
	}
	return false
}

func (w *Watcher) isExcluded(path string) bool {
	for _, pattern := range w.cfg.Exclude {
		// Match against basename
		if matched, _ := filepath.Match(pattern, filepath.Base(path)); matched {
			return true
		}
		// Match against full path for directory exclusions
		if strings.Contains(path, pattern) {
			return true
		}
	}
	return false
}
