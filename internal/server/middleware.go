package server

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gstranger/hearsay/pkg/hearsay"
)

// AuthMiddleware wraps handlers that require authentication.
type AuthMiddleware struct {
	Token string
	Audit *AuditLogger
}

func (a *AuthMiddleware) Wrap(next http.HandlerFunc) http.HandlerFunc {
	if a.Token == "" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			auth = r.Header.Get("X-Api-Key")
			if auth != "" && auth == a.Token {
				next(w, r)
				return
			}
		}
		if strings.HasPrefix(auth, "Bearer ") {
			token := strings.TrimPrefix(auth, "Bearer ")
			if token == a.Token {
				next(w, r)
				return
			}
		}
		if a.Audit != nil {
			a.Audit.Log(hearsay.AuditEventAuthFailure, "", "", "unauthorized", nil)
		}
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}
}

type LogFormat string

const (
	LogFormatText LogFormat = "text"
	LogFormatJSON LogFormat = "json"
)

type LoggingMiddleware struct {
	Format LogFormat
}

func (l *LoggingMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		agentID := r.Header.Get("X-Agent-ID")
		if agentID == "" {
			agentID = "-"
		}

		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wrapped, r)

		duration := time.Since(start)
		if l.Format == LogFormatJSON {
			log.Printf(`{"time":"%s","method":"%s","path":"%s","agent_id":"%s","status":%d,"duration_ms":%d}`,
				time.Now().UTC().Format(time.RFC3339),
				r.Method, r.URL.Path, agentID, wrapped.statusCode, duration.Milliseconds())
		} else {
			log.Printf("%s %s %s %d %s", r.Method, r.URL.Path, agentID, wrapped.statusCode, duration)
		}
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
