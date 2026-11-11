package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"waves/internal/core"
	"waves/internal/events"
)

// pingEvery keeps proxies and the browser from closing an idle stream.
const pingEvery = 25 * time.Second

// events streams server-sent events. The request goroutine owns the loop; it
// ends when the client goes away, the bus drops the client, or the server stops.
func (s *Server) events(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	fl, ok := w.(http.Flusher)
	if !ok {
		return &core.Error{Status: http.StatusInternalServerError, Code: "unsupported", Message: "Streaming is not available."}
	}
	last, _ := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
	sub, replay, reset := s.core.Bus().Subscribe(last)
	defer s.core.Bus().Unsubscribe(sub)
	// Streams outlive the server's write timeout.
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Time{})
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	send := func(ev events.Event) bool {
		_, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Name, ev.Data)
		return err == nil
	}
	hello := fmt.Sprintf(`{"server_version":%q,"audit_ok":%t}`, s.core.Version(), s.core.AuditStatus(r.Context()).State == "verified")
	fmt.Fprintf(w, "retry: 2000\nevent: hello\ndata: %s\n\n", hello)
	if reset {
		fmt.Fprint(w, "event: reset\ndata: {}\n\n")
	}
	for _, ev := range replay {
		if !send(ev) {
			return nil
		}
	}
	fl.Flush()
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case ev, ok := <-sub.C:
			if !ok {
				return nil // dropped: the client reconnects and replays or resets
			}
			if !send(ev) {
				return nil
			}
			fl.Flush()
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return nil
			}
			fl.Flush()
		}
	}
}
