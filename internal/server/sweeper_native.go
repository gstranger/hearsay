//go:build !wasm

package server

import (
	"context"
	"time"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

// RunSweeper starts a background goroutine that calls ReleaseExpired
// every interval. The goroutine stops when ctx is cancelled.
func RunSweeper(ctx context.Context, provider hearsay.Provider, namespace string, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				before := time.Now().UTC()
				provider.ReleaseExpired(context.Background(), namespace, before)
			case <-ctx.Done():
				return
			}
		}
	}()
}