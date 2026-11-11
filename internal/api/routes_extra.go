package api

import (
	"io"
	"net/http"

	"waves/internal/core"
)

// serverOnlyRoutes are routes the web UI does not call (CLI, hook, scripts).
func (s *Server) serverOnlyRoutes() []route {
	return []route{
		{"DELETE", "/projects/{id}", "op", s.removeProject},
		{"POST", "/guard/decide", "guard", s.guardDecide},
		{"GET", "/tasks", "op", s.tasks},
		{"GET", "/tasks/{id}", "op", s.taskDetail},
		{"POST", "/tasks/{id}/{action}", "op", s.tasksRoute},
		{"POST", "/gc", "op", s.gc},
		{"GET", "/doctor", "op", s.doctorRoute},
		{"GET", "/projects/{id}/settings", "op", s.projectSettings},
		{"GET", "/planning-requests/{id}", "op", s.planningRequestView},
		{"GET", "/projects/{id}/proposals", "op", s.proposals},
		{"GET", "/proposals/{id}", "op", s.proposal},
		{"POST", "/proposals/{id}/create-workflow", "op", s.createWorkflow},
		{"PATCH", "/workflow-versions/{id}", "op", s.editDraft},
		{"GET", "/phases/{id}", "op", s.phaseDetail},
	}
}

func jsonOr(w http.ResponseWriter, status int, v any, err error) error {
	if err != nil {
		return err
	}
	return writeJSON(w, status, v)
}

func (s *Server) tasks(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	q := r.URL.Query()
	l, err := s.core.Tasks(r.Context(), q.Get("project_id"), q.Get("phase_id"), q.Get("state"))
	return jsonOr(w, 200, map[string]any{"items": l}, err)
}

func (s *Server) taskDetail(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	d, err := s.core.TaskDetail(r.Context(), r.PathValue("id"))
	return jsonOr(w, 200, d, err)
}

func (s *Server) doctorRoute(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	return writeJSON(w, 200, map[string]any{"checks": s.core.Doctor(r.Context())})
}

func (s *Server) projectSettings(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	ps, err := s.core.ProjectSettings(r.Context(), r.PathValue("id"))
	return jsonOr(w, 200, ps, err)
}

func (s *Server) planningRequestView(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	v, err := s.core.PlanningRequestView(r.Context(), r.PathValue("id"))
	return jsonOr(w, 200, v, err)
}

func (s *Server) proposals(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	l, err := s.core.Proposals(r.Context(), r.PathValue("id"))
	return jsonOr(w, 200, map[string]any{"items": l}, err)
}

func (s *Server) proposal(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	p, err := s.core.Proposal(r.Context(), r.PathValue("id"))
	return jsonOr(w, 200, p, err)
}

func (s *Server) createWorkflow(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	id, err := s.core.CreateWorkflowFromProposal(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	v, err := s.core.WorkflowVersion(r.Context(), id)
	return jsonOr(w, 201, v.WorkflowVersionSummary, err)
}

// editDraft takes a whole proposal document (the same schema the agent uses)
// and replaces the draft's content with it.
func (s *Server) editDraft(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	doc, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	errs, err := s.core.EditDraft(r.Context(), p, r.PathValue("id"), doc)
	if errs != nil {
		return writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]any{"code": "validation_failed", "message": "The edited workflow is not valid.", "next_step": "Fix the listed errors."}, "errors": errs})
	}
	if err != nil {
		return err
	}
	v, err := s.core.WorkflowVersion(r.Context(), r.PathValue("id"))
	return jsonOr(w, 200, v, err)
}

func (s *Server) phaseDetail(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	d, err := s.core.PhaseDetail(r.Context(), r.PathValue("id"))
	return jsonOr(w, 200, d, err)
}
