// Package api is the HTTP face of the Core Service: the JSON API under
// /api/v1, the server-sent event stream, and the embedded web UI. Handlers
// authenticate and translate; every rule lives in core.
package api

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"waves/internal/api/contract"
	"waves/internal/core"
)

const (
	apiPrefix  = "/api/v1"
	maxBody    = 1 << 20
	cookieName = "waves_session"
	headerCSRF = "X-Waves-CSRF"
	headerElev = "X-Waves-Elevation"
	headerTTY  = "X-Waves-TTY"
)

// Server serves the API and the UI on one loopback port.
type Server struct {
	core *core.Service
	port int
	ui   fs.FS
	log  *slog.Logger
	mux  *http.ServeMux
}

// handler is an API handler. It returns an error the server maps to the
// contract error body.
type handler func(w http.ResponseWriter, r *http.Request, p core.Principal) error

type route struct {
	method, path, scope string
	h                   handler
}

// New builds the server. port is the listening port (for Host and Origin
// checks); ui is the built web app, or nil.
func New(c *core.Service, port int, ui fs.FS, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{core: c, port: port, ui: ui, log: log, mux: http.NewServeMux()}
	for _, rt := range s.routes() {
		s.mux.Handle(rt.method+" "+apiPrefix+rt.path, s.wrap(rt))
	}
	s.mux.HandleFunc("GET /{$}", s.serveUI)
	s.mux.HandleFunc("GET /assets/", s.serveUI)
	s.mux.HandleFunc("GET /favicon.svg", s.serveUI)
	return s
}

// Mount adds a handler outside /api/v1 (for example /mcp). It must do its own
// authentication; Host and Origin checks still apply.
func (s *Server) Mount(pattern string, h http.Handler) { s.mux.Handle(pattern, h) }

// Handler returns the root handler with the loopback checks in front.
func (s *Server) Handler() http.Handler { return s.loopback(s.mux) }

// loopback refuses requests for other hosts (DNS rebinding) and cross-origin
// writes, and sets the security headers on every response.
func (s *Server) loopback(next http.Handler) http.Handler {
	hosts := map[string]bool{"127.0.0.1:" + strconv.Itoa(s.port): true, "localhost:" + strconv.Itoa(s.port): true}
	origins := map[string]bool{"http://127.0.0.1:" + strconv.Itoa(s.port): true, "http://localhost:" + strconv.Itoa(s.port): true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		// Svelte components use style attributes, so inline styles are allowed; scripts are not.
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		if !hosts[r.Host] {
			writeErr(w, &core.Error{Status: http.StatusMisdirectedRequest, Code: "wrong_host", Message: "This service answers only on its loopback address.", NextStep: "Open the address that waves open prints."})
			return
		}
		if o := r.Header.Get("Origin"); o != "" && r.Method != http.MethodGet && r.Method != http.MethodHead && !origins[o] {
			writeErr(w, &core.Error{Status: http.StatusForbidden, Code: "bad_origin", Message: "Requests from other sites are refused.", NextStep: "Use the Waves dashboard."})
			return
		}
		if fs := r.Header.Get("Sec-Fetch-Site"); fs == "cross-site" && r.Method != http.MethodGet {
			writeErr(w, &core.Error{Status: http.StatusForbidden, Code: "bad_origin", Message: "Requests from other sites are refused.", NextStep: "Use the Waves dashboard."})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Authenticate resolves the caller from a bearer token or the session cookie.
// For cookie sessions it also returns the CSRF token the request must carry.
func (s *Server) Authenticate(r *http.Request) (core.Principal, string) {
	src := r.RemoteAddr
	if a := r.Header.Get("Authorization"); a != "" {
		tok, ok := strings.CutPrefix(a, "Bearer ")
		if !ok {
			return core.Principal{Kind: core.KindNone, Source: src}, ""
		}
		p, _ := s.core.Bearer(r.Context(), strings.TrimSpace(tok), src)
		if p.Kind == core.KindOperator {
			p.TTY = r.Header.Get(headerTTY) == "1"
		}
		return p, ""
	}
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		p, csrf, ok := s.core.Session(r.Context(), c.Value, src)
		if ok {
			return p, csrf
		}
	}
	return core.Principal{Kind: core.KindNone, Source: src}, ""
}

func (s *Server) wrap(rt route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		write := r.Method != http.MethodGet && r.Method != http.MethodHead
		if write && r.ContentLength != 0 {
			if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
				writeErr(w, &core.Error{Status: http.StatusUnsupportedMediaType, Code: "unsupported", Message: "Requests must be JSON.", NextStep: "Send Content-Type: application/json."})
				return
			}
		}
		p, csrf := s.Authenticate(r)
		err := s.authorize(r, rt, p, csrf, write)
		if err == nil {
			err = rt.h(w, r, p)
		}
		if err != nil {
			// A human-only action tried without a valid passphrase step-up, or while
			// the audit chain is broken, is a gate attempt: record it.
			if e := core.AsError(err); e != nil && (e.Code == "stepup_required" || e.Code == "audit_chain_broken") {
				s.core.AuditStepUpRefused(r.Context(), p, rt.method+" "+rt.path, e.Code+": "+e.Message)
			}
			s.fail(w, r, err)
		}
	})
}

func (s *Server) authorize(r *http.Request, rt route, p core.Principal, csrf string, write bool) error {
	if rt.scope == "none" {
		return nil
	}
	if p.Kind == core.KindNone {
		return &core.Error{Status: http.StatusUnauthorized, Code: "unauthenticated", Message: "Your session ended.", NextStep: "Open a fresh link: waves open."}
	}
	switch rt.scope {
	case "op", "human", "operator_token":
		if !p.IsOperator() || (rt.scope == "operator_token" && p.Kind != core.KindOperator) {
			// Lease, guard and MCP credentials never reach operator or human routes (AH-1, AH-4).
			s.core.AuditRejected(r.Context(), p, rt.method+" "+rt.path)
			return &core.Error{Status: http.StatusForbidden, Code: "wrong_scope", Message: "This credential cannot do that.", NextStep: "Use the dashboard or the CLI as the human operator."}
		}
	case "guard":
		if p.Kind != core.KindGuard {
			s.core.AuditRejected(r.Context(), p, rt.method+" "+rt.path)
			return &core.Error{Status: http.StatusForbidden, Code: "wrong_scope", Message: "Only the guard hook may call this.", NextStep: ""}
		}
	default:
		return fmt.Errorf("route %s has unknown scope %q", rt.path, rt.scope)
	}
	// A human route without an elevation stops here, so a handler cannot
	// silently act with operator scope. Rejecting a task-level approval is
	// the one operator decision on a human route; core checks the rest.
	if rt.scope == "human" && r.Header.Get(headerElev) == "" && rt.path != "/approvals/{id}/decision" {
		return &core.Error{Status: http.StatusForbidden, Code: "stepup_required", Message: "This action needs your approval passphrase.", NextStep: "Enter the approval passphrase and try again."}
	}
	if write && p.Kind == core.KindSession && !core.SameHash(r.Header.Get(headerCSRF), csrf) {
		return &core.Error{Status: http.StatusForbidden, Code: "csrf_failed", Message: "The request did not carry the CSRF token.", NextStep: "Reload the page."}
	}
	return nil
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if e := core.AsError(err); e != nil {
		writeErr(w, e)
		return
	}
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		writeErr(w, &core.Error{Status: http.StatusRequestEntityTooLarge, Code: "limit_exceeded", Message: "The request body is too large.", NextStep: "Send at most 1 MiB."})
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	id := core.NewID("req")
	s.log.Error("request failed", "request_id", id, "method", r.Method, "path", r.URL.Path, "err", err)
	writeErr(w, &core.Error{Status: http.StatusInternalServerError, Code: "internal", Message: "Something went wrong inside Waves (request " + id + ").", NextStep: "Run waves doctor; the service log has the details."})
}

func writeErr(w http.ResponseWriter, e *core.Error) {
	body := contract.ErrorBody{Error: contract.ErrorDetail{Code: e.Code, Message: e.Message, NextStep: e.NextStep}}
	if v, ok := e.Extra["attempts_left"]; ok {
		body.Error.AttemptsLeft = &v
	}
	if v, ok := e.Extra["retry_after_s"]; ok {
		body.Error.RetryAfterS = &v
		w.Header().Set("Retry-After", strconv.Itoa(v))
	}
	if v, ok := e.Extra["record"]; ok {
		body.Error.Record = &v
	}
	writeJSON(w, e.Status, body)
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

// decode reads a JSON body into v and refuses unknown fields.
func decode(r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return nil
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return err
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return &core.Error{Status: http.StatusBadRequest, Code: "validation_failed", Message: "The request body is not valid: " + jsonProblem(err), NextStep: "Check the fields you sent."}
	}
	return nil
}

func jsonProblem(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, "json: "); i >= 0 {
		msg = msg[i+6:]
	}
	if len(msg) > 120 {
		msg = msg[:120]
	}
	return msg
}

// clientOf returns p unchanged: the client (cli or dashboard) follows from
// the credential kind, never from a request header.
func clientOf(_ *http.Request, p core.Principal) core.Principal { return p }

func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	if s.ui == nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, "The dashboard was not built into this binary. Build it with: make build\n")
		return
	}
	if r.URL.Path == "/" {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		r.Header.Del("Range")
		w.Header().Set("Vary", "Accept-Encoding")
		gz := &gzipWriter{ResponseWriter: w}
		defer gz.close()
		w = gz
	}
	http.FileServerFS(s.ui).ServeHTTP(w, r)
}

// gzipWriter compresses the dashboard's static files on the fly (they are small).
type gzipWriter struct {
	http.ResponseWriter
	zw *gzip.Writer
}

func (g *gzipWriter) WriteHeader(code int) {
	if code == http.StatusOK {
		g.Header().Del("Content-Length")
		g.Header().Set("Content-Encoding", "gzip")
		g.zw = gzip.NewWriter(g.ResponseWriter)
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if g.zw == nil {
		return g.ResponseWriter.Write(b)
	}
	return g.zw.Write(b)
}

func (g *gzipWriter) close() {
	if g.zw != nil {
		g.zw.Close()
	}
}
