package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"waves/internal/core"
)

func (s *Server) agentHandlers() map[string]handler {
	return map[string]handler{
		"GET /executions":                       s.executions,
		"GET /executions/{id}":                  s.execution,
		"GET /executions/{id}/log":              s.executionLog,
		"GET /executions/{id}/diff":             s.executionDiff,
		"GET /executions/{id}/context":          s.executionContext,
		"POST /executions/{id}/cancel":          s.cancelExecution,
		"POST /executions/{id}/resolve":         s.resolveExecution,
		"POST /executions/{id}/orphans/stop":    s.stopOrphans,
		"GET /harnesses":                        s.harnesses,
		"GET /projects/{id}/guard":              s.guardInfo,
		"GET /projects/{id}/guard/decisions":    s.guardDecisions,
		"PATCH /projects/{id}/settings":         s.patchProjectSettings,
		"POST /projects/{id}/agent/disconnect":  s.disconnect,
		"GET /projects/{id}/agent/connect-plan": s.connectPlan,
		"POST /projects/{id}/agent/connect":     s.connect,
	}
}

func (s *Server) executions(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	q := r.URL.Query()
	l, err := s.core.Executions(r.Context(), q.Get("project_id"), q.Get("state"), q.Get("task_id"), listLimit(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, l)
}

func (s *Server) execution(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	d, err := s.core.ExecutionDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, d)
}

func streamText(w http.ResponseWriter, rc io.ReadCloser) error {
	defer rc.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, err := io.Copy(w, rc)
	return err
}

func (s *Server) executionLog(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	off, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	rc, err := s.core.OpenLog(r.Context(), r.PathValue("id"), max(off, 0))
	if err != nil {
		return err
	}
	return streamText(w, rc)
}

func (s *Server) executionDiff(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	rc, err := s.core.OpenDiff(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return streamText(w, rc)
}

func (s *Server) executionContext(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	c, err := s.core.DeliveredContext(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, c)
}

func (s *Server) cancelExecution(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	if err := s.core.CancelExecution(r.Context(), p, r.PathValue("id")); err != nil {
		return err
	}
	d, err := s.core.ExecutionDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 202, d)
}

func (s *Server) resolveExecution(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	var req struct {
		Decision string `json:"decision"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := s.core.ResolveExecution(r.Context(), p, r.PathValue("id"), req.Decision); err != nil {
		return err
	}
	return s.execution(w, r, p)
}

func (s *Server) stopOrphans(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	// Waves starts no harness process in this version, so it knows of no orphans.
	return &core.Error{Status: http.StatusConflict, Code: "invalid_state", Message: "No orphaned process is known to Waves.", NextStep: "Check for leftover processes in the project folder yourself."}
}

func (s *Server) harnesses(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	l, err := s.core.Harnesses(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, 200, l)
}

func (s *Server) guardInfo(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	g, err := s.core.GuardInfo(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, g)
}

func (s *Server) guardDecisions(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	l, err := s.core.GuardDecisions(r.Context(), r.PathValue("id"), listLimit(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, l)
}

func (s *Server) patchProjectSettings(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	var req struct {
		GuardPolicy       string  `json:"guard_policy"`
		UnmanagedSessions string  `json:"unmanaged_sessions"`
		PromptTemplate    *string `json:"prompt_template"`
		BaseBranch        *string `json:"base_branch"`
		ContextBudget     *int    `json:"context_budget"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.GuardPolicy != "" || req.UnmanagedSessions != "" {
		if err := s.core.SetProjectGuard(r.Context(), p, r.Header.Get(headerElev), r.PathValue("id"), req.GuardPolicy, req.UnmanagedSessions); err != nil {
			return err
		}
	}
	if err := s.core.SetProjectOptions(r.Context(), p, r.Header.Get(headerElev), r.PathValue("id"), req.PromptTemplate, req.BaseBranch, req.ContextBudget); err != nil {
		return err
	}
	return s.guardInfo(w, r, p)
}

func (s *Server) connectPlan(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	pl, err := s.core.ConnectPlan(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, pl)
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	var req struct {
		PlanHash string `json:"plan_hash"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	pl, err := s.core.Connect(r.Context(), p, r.Header.Get(headerElev), r.PathValue("id"), req.PlanHash)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, pl)
}

func (s *Server) disconnect(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	pl, err := s.core.Disconnect(r.Context(), p, r.Header.Get(headerElev), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, pl)
}

// guardDecide is the hook endpoint. It answers only allow or deny.
func (s *Server) guardDecide(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	var ev core.HookEvent
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&ev); err != nil {
		return &core.Error{Status: http.StatusBadRequest, Code: "validation_failed", Message: "The hook event is not valid JSON."}
	}
	d, err := s.core.DecideGuard(r.Context(), p, ev)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, d)
}

func (s *Server) tasksRoute(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	var req struct {
		Reason string `json:"reason"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	var err error
	switch r.PathValue("action") {
	case "cancel":
		err = s.core.CancelTask(r.Context(), p, r.PathValue("id"), req.Reason)
	case "retry":
		err = s.core.RetryTask(r.Context(), p, r.PathValue("id"))
	case "resume":
		err = s.core.ResumeTask(r.Context(), p, r.PathValue("id"))
	case "run":
		err = &core.Error{Status: http.StatusNotImplemented, Code: "unsupported", Message: "Waves does not start agents in this version.", NextStep: "Start Claude Code in the project; it claims the task through MCP."}
	default:
		err = &core.Error{Status: http.StatusNotFound, Code: "not_found", Message: "No such task action."}
	}
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
