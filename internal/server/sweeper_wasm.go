//go:build wasm

package server

import (
	"context"
	"time"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

// RunSweeper is a no-op in WASM. The sweeper is managed by the
// Durable Object's alarm() mechanism instead.
func RunSweeper(ctx context.Context, provider hearsay.Provider, namespace string, interval time.Duration) {
	// No-op: DO alarm handles sweeping in the Worker runtime
}