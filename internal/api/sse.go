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
func (s *Server) events(w http.ResponseWriter, r *http.Request, p core.Principal) error {
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
	// A dashboard stream ends when its session does (logout, expiry): re-checked at most every 5 s while events flow and on every ping.
	cookie, _ := r.Cookie(cookieName)
	var checked = time.Now()
	alive := func() bool {
		if p.Kind != core.KindSession || cookie == nil {
			return true
		}
		checked = time.Now()
		_, _, ok := s.core.Session(r.Context(), cookie.Value, p.Source)
		return ok
	}
	send := func(ev events.Event) bool {
		if time.Since(checked) > 5*time.Second && !alive() {
			return false
		}
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
			if !alive() {
				return nil
			}
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return nil
			}
			fl.Flush()
		}
	}
}
