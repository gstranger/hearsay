package server

import (
	"fmt"
	"net/http"
	"strconv"
)

// handleSSE streams coordination events as Server-Sent Events.
// Clients can request replay with ?since=<seq> to catch up after disconnection.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		http.Error(w, "namespace required", http.StatusBadRequest)
		return
	}

	var since int64
	if v := r.URL.Query().Get("since"); v != "" {
		var err error
		since, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			http.Error(w, "invalid since parameter", http.StatusBadRequest)
			return
		}
	}

	flusher, canFlush := w.(http.Flusher)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	if canFlush {
		flusher.Flush()
	}

	events, err := s.provider.SubscribeEvents(r.Context(), namespace, since)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: {\"error\":\"%s\"}\n\n", err.Error())
		if canFlush {
			flusher.Flush()
		}
		return
	}

	for event := range events {
		fmt.Fprintf(w, "event: %s\ndata: ", event.Type)
		w.Write(event.Payload)
		fmt.Fprintf(w, "\n\n")
		if canFlush {
			flusher.Flush()
		}
	}
}