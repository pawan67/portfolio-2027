package metrics

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	// Comment frames keep the connection alive through Cloudflare, which drops
	// an idle proxied connection after about 100 seconds.
	heartbeatInterval = 30 * time.Second
	// Each write extends the deadline. Without this the server's WriteTimeout
	// would terminate every stream after 20 seconds.
	writeTimeout = 10 * time.Second
)

// Stream returns the SSE handler for GET /api/metrics/stream.
func Stream(hub *Hub) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		events, release, ok := hub.Subscribe()
		if !ok {
			w.Header().Set("Retry-After", "30")
			http.Error(w, "too many listeners", http.StatusServiceUnavailable)
			return
		}
		defer release()

		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-store")
		h.Set("Connection", "keep-alive")
		// Tells nginx-family proxies not to buffer the stream into uselessness.
		h.Set("X-Accel-Buffering", "no")

		rc := http.NewResponseController(w)
		w.WriteHeader(http.StatusOK)

		// Send the current sample immediately so the panel has content before
		// the next tick rather than showing an empty frame for two seconds.
		if snapshot, ok := hub.Latest(); ok {
			if err := writeEvent(w, rc, snapshot); err != nil {
				return
			}
		}

		heartbeat := time.NewTicker(heartbeatInterval)
		defer heartbeat.Stop()

		for {
			select {
			case <-r.Context().Done():
				return

			case snapshot := <-events:
				if err := writeEvent(w, rc, snapshot); err != nil {
					return
				}

			case <-heartbeat.C:
				_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
				if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
					return
				}
				if err := rc.Flush(); err != nil {
					return
				}
			}
		}
	})
}

func writeEvent(w http.ResponseWriter, rc *http.ResponseController, snapshot Snapshot) error {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}

	if err := rc.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}
	return rc.Flush()
}
