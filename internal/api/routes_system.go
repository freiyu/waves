package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"waves/internal/api/contract"
	"waves/internal/core"
	"waves/internal/store"
)

// routes is the full route table. Scopes for contract routes come from the
// contract; the server-only routes (not called by the web UI) are listed here.
func (s *Server) routes() []route {
	h := map[string]handler{
		"GET /health":              s.health,
		"GET /status":              s.status,
		"GET /auth/link/{code}":    s.linkStatus,
		"POST /auth/consume":       s.consume,
		"GET /auth/session":        s.session,
		"POST /auth/logout":        s.logout,
		"POST /auth/stepup":        s.stepUp,
		"POST /auth/stepup/revoke": s.revoke,
		"GET /audit":               s.auditList,
		"POST /audit/acknowledge":  s.auditAck,
		"POST /backup":             s.backup,
		"GET /settings":            s.settings,
		"PATCH /settings":          s.patchSettings,
		"GET /events":              s.events,
	}
	for _, m := range []map[string]handler{s.domainHandlers(), s.agentHandlers()} {
		for k, v := range m {
			h[k] = v
		}
	}
	var out []route
	seen := map[string]bool{}
	for _, rt := range contract.Routes {
		key := rt.Method + " " + rt.Path
		if fn, ok := h[key]; ok {
			out = append(out, route{rt.Method, rt.Path, rt.Scope, fn})
			seen[key] = true
		}
	}
	extra := []route{
		{"POST", "/auth/login-links", "operator_token", s.createLink},
		{"PUT", "/auth/passphrase", "human", s.rotatePassphrase},
	}
	extra = append(extra, s.serverOnlyRoutes()...)
	for _, rt := range extra {
		if !seen[rt.method+" "+rt.path] {
			out = append(out, rt)
		}
	}
	return out
}

// MissingRoutes lists contract routes without a handler (a test keeps it empty).
func (s *Server) MissingRoutes() []string {
	have := map[string]bool{}
	for _, rt := range s.routes() {
		have[rt.method+" "+rt.path] = true
	}
	var out []string
	for _, rt := range contract.Routes {
		if !have[rt.Method+" "+rt.Path] {
			out = append(out, rt.Method+" "+rt.Path)
		}
	}
	return out
}

func (s *Server) health(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	return writeJSON(w, 200, contract.HealthResponse{Version: s.core.Version()})
}

func auditView(st core.AuditState) contract.AuditStatus {
	out := contract.AuditStatus{State: st.State, Records: st.Records, LastCheckAt: st.CheckedAt, LastCheckpoint: st.LastCheckpt}
	if st.State == "broken" {
		out.BrokenAtRecord = int(st.BrokenAt)
	}
	return out
}

func (s *Server) status(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	ctx := r.Context()
	c, err := s.core.ExecutionCounts(ctx)
	if err != nil {
		return err
	}
	items, err := s.core.NeedsAttention(ctx)
	if err != nil {
		return err
	}
	out := contract.StatusResponse{
		ServerVersion:     s.core.Version(),
		ClaudeCodeVersion: s.core.ClaudeCodeVersion(ctx),
		Counts:            contract.Counts{Running: c.Running, Queued: c.Queued, WaitingApproval: c.WaitingApproval, Failed: c.Failed, Disconnected: c.Disconnected},
		Audit:             auditView(s.core.AuditStatus(ctx)),
		NeedsAttention:    make([]contract.AttentionItem, 0, len(items)),
	}
	for _, a := range items {
		out.NeedsAttention = append(out.NeedsAttention, contract.AttentionItem{Kind: a.Kind, Label: a.Label, Subject: a.Subject, ProjectID: a.ProjectID, ProjectName: a.ProjectName, Since: a.Since, RefType: a.RefType, RefID: a.RefID})
	}
	if ns := nextStep(items); ns != nil {
		out.NextStep = ns
	}
	return writeJSON(w, 200, out)
}

func nextStep(items []core.Attention) *contract.NextStep {
	var first *core.Attention
	for i := range items {
		if items[i].Kind == "audit_chain" {
			first = &items[i]
			break
		}
	}
	if first == nil {
		for i := range items {
			if items[i].Kind == "approval" {
				first = &items[i]
				break
			}
		}
	}
	if first == nil && len(items) > 0 {
		first = &items[0]
	}
	if first == nil {
		return nil
	}
	text := fmt.Sprintf("Look at %s in %s.", first.Subject, first.ProjectName)
	switch first.Kind {
	case "audit_chain":
		text = "Acknowledge the audit chain break on the Activity page; approvals stay blocked until then."
	case "approval":
		text = fmt.Sprintf("Decide the %s approval for %s · %s.", first.Label, first.ProjectName, first.Subject)
	case "passphrase_reset", "database_restored", "stepup_failures", "bypass_attempt":
		text = first.Label + ": check Activity, then acknowledge it there."
	}
	return &contract.NextStep{Text: text, RefType: first.RefType, RefID: first.RefID}
}

func (s *Server) createLink(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	code, exp, err := s.core.CreateLoginLink(r.Context(), p)
	if err != nil {
		return err
	}
	// The code travels in the URL fragment, so it never reaches a server log or a Referer.
	return writeJSON(w, 201, map[string]any{"url": fmt.Sprintf("http://127.0.0.1:%d/#/signin/%s", s.port, code), "expires_at": exp})
}

func (s *Server) linkStatus(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	ok, left := s.core.LinkStatus(r.Context(), r.PathValue("code"))
	if !ok {
		return writeJSON(w, 200, contract.LinkStatus{State: "invalid"})
	}
	return writeJSON(w, 200, contract.LinkStatus{State: "valid", ExpiresInS: left})
}

func (s *Server) consume(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	var req contract.ConsumeLinkRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	secret, err := s.core.ConsumeLink(r.Context(), req.Code, r.RemoteAddr)
	if err != nil {
		return err
	}
	p, csrf, _ := s.core.Session(r.Context(), secret, r.RemoteAddr)
	info, err := s.core.SessionInfo(r.Context(), p, csrf)
	if err != nil {
		return err
	}
	// No __Host- prefix: it needs Secure, which plain loopback HTTP cannot offer.
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: secret, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: info.ExpiresAt})
	return writeJSON(w, 200, sessionView(info))
}

func sessionView(i core.SessionInfo) contract.Session {
	out := contract.Session{Scope: "operator", CSRFToken: i.CSRF, ExpiresAt: i.ExpiresAt, PassphraseSet: i.PassphraseSet}
	if i.Elevation != nil {
		out.Elevation = &contract.Elevation{Kind: i.Elevation.Kind, ExpiresAt: i.Elevation.ExpiresAt, ApprovalID: i.Elevation.ApprovalID}
	}
	return out
}

func (s *Server) session(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	_, csrf := s.Authenticate(r)
	info, err := s.core.SessionInfo(r.Context(), p, csrf)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, sessionView(info))
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	if err := s.core.Logout(r.Context(), p); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) stepUp(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	var req contract.StepUpRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	res, err := s.core.StepUp(r.Context(), clientOf(r, p), req.Passphrase, req.ApprovalID)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, contract.StepUpResponse{ElevationSecret: res.Secret, Kind: res.Kind, ExpiresAt: res.ExpiresAt, ApprovalID: res.ApprovalID})
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	if err := s.core.RevokeElevation(r.Context(), p, r.Header.Get(headerElev)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) rotatePassphrase(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := s.core.RotatePassphrase(r.Context(), clientOf(r, p), r.Header.Get(headerElev), req.Current, req.New); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func listLimit(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n < 1 {
		return 50
	}
	return min(n, 200)
}

func (s *Server) auditList(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	before, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	recs, next, err := s.core.AuditPage(r.Context(), listLimit(r), before)
	if err != nil {
		return err
	}
	out := contract.AuditList{Items: make([]contract.AuditRecord, 0, len(recs)), Status: auditView(s.core.AuditStatus(r.Context()))}
	for _, a := range recs {
		out.Items = append(out.Items, contract.AuditRecord{Seq: int(a.Seq), At: store.ParseTime(a.TS), Event: a.Event, ActorScope: a.ActorScope, Subject: a.Subject, Detail: a.Detail})
	}
	if next > 0 {
		out.NextCursor = strconv.FormatInt(next, 10)
	}
	return writeJSON(w, 200, out)
}

func (s *Server) auditAck(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	st, err := s.core.AcknowledgeAudit(r.Context(), clientOf(r, p), r.Header.Get(headerElev))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, auditView(st))
}

func (s *Server) backup(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	var req struct {
		Path string `json:"path"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.Path != "" {
		return &core.Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "The service writes backups into its own backup folder only.", NextStep: "Run waves backup <path> to get a copy at a path you choose."}
	}
	path, n, err := s.core.Backup(r.Context(), p)
	if err != nil {
		return err
	}
	// The path is extra for the CLI; the dashboard reads only the contract fields.
	return writeJSON(w, 200, struct {
		contract.BackupResult
		Path string `json:"path"`
	}{contract.BackupResult{At: time.Now().UTC(), Bytes: int(n)}, path})
}

func (s *Server) gc(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	files, bytes, err := s.core.GC(r.Context(), p)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, map[string]int{"removed_files": files, "freed_bytes": int(bytes)})
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	v, at, size := s.core.SettingsView(r.Context())
	out := contract.Settings{
		PassphraseSet: s.core.PassphraseSet(r.Context()), ElevationWindowMin: v["elevation_window_min"], Listen: fmt.Sprintf("127.0.0.1:%d", s.port),
		MaxConcurrent: v["max_concurrent"], HardCap: v["hard_cap"], HeartbeatS: v["heartbeat_interval_s"], LogRetentionDays: v["log_retention_days"],
		LogMaxMB: v["log_max_mb"], SessionTTLHours: v["session_ttl_h"], LastBackupBytes: size,
	}
	if at != "" {
		t := store.ParseTime(at)
		out.LastBackupAt = &t
	}
	return writeJSON(w, 200, out)
}

func (s *Server) patchSettings(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	var req contract.PatchSettingsRequest
	if err := decode(r, &req); err != nil {
		if e := core.AsError(err); e != nil {
			e.Status = http.StatusUnprocessableEntity
			e.NextStep = "Use the documented settings only."
		}
		return err
	}
	ch := map[string]int{}
	for k, v := range map[string]*int{"elevation_window_min": req.ElevationWindowMin, "max_concurrent": req.MaxConcurrent, "heartbeat_interval_s": req.HeartbeatS, "log_retention_days": req.LogRetentionDays} {
		if v != nil {
			ch[k] = *v
		}
	}
	if err := s.core.PatchSettings(r.Context(), clientOf(r, p), r.Header.Get(headerElev), ch); err != nil {
		return err
	}
	return s.settings(w, r, p)
}
