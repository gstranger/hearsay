package server

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

// RateLimiter implements per-agent sliding window rate limiting.
type RateLimiter struct {
	mu       sync.Mutex
	perAgent map[string][]time.Time
	limit    int // max requests per second (0 = disabled)
	Audit    *AuditLogger
}

func NewRateLimiter(limit, burst int) *RateLimiter {
	return &RateLimiter{
		perAgent: make(map[string][]time.Time),
		limit:    limit,
	}
}

func (rl *RateLimiter) Wrap(next http.HandlerFunc) http.HandlerFunc {
	if rl.limit <= 0 {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.Header.Get("X-Agent-ID")
		if agentID == "" {
			agentID = "unknown"
		}

		now := time.Now()
		windowStart := now.Add(-1 * time.Second)

		rl.mu.Lock()
		timestamps := rl.perAgent[agentID]

		// Evict old timestamps outside the window
		cutoff := len(timestamps)
		for i, ts := range timestamps {
			if ts.After(windowStart) {
				cutoff = i
				break
			}
		}
		timestamps = timestamps[cutoff:]

		count := len(timestamps)
		limited := count >= rl.limit

		if !limited {
			timestamps = append(timestamps, now)
			rl.perAgent[agentID] = timestamps
		}
		rl.mu.Unlock()

		// Set rate limit headers
		remaining := rl.limit - count - 1
		if remaining < 0 {
			remaining = 0
		}
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rl.limit))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(now.Unix()+1, 10))

		if limited {
			w.Header().Set("Retry-After", "1")
			if rl.Audit != nil {
				rl.Audit.Log(hearsay.AuditEventRateLimit, agentID, "", "blocked", nil)
			}
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}

		next(w, r)
	}
}