package api

import (
	"net/http"

	"waves/internal/api/contract"
	"waves/internal/core"
)

// domainHandlers are the contract routes for projects, workflow, approvals,
// executions and harnesses.
func (s *Server) domainHandlers() map[string]handler {
	return map[string]handler{
		"GET /projects":                                   s.projects,
		"POST /projects":                                  s.registerProject,
		"GET /projects/{id}":                              s.projectDetail,
		"POST /projects/{id}/activate":                    s.activateProject,
		"GET /projects/{id}/requirements":                 s.requirements,
		"POST /projects/{id}/requirements":                s.addRequirement,
		"POST /projects/{id}/planning-requests":           s.planningRequest,
		"GET /projects/{id}/workflow":                     s.workflowInfo,
		"GET /workflow-versions/{id}":                     s.workflowVersion,
		"POST /workflow-versions/{id}/versions":           s.newVersion,
		"POST /workflow-versions/{id}/submit":             s.submitVersion,
		"GET /phases/{id}/review":                         s.phaseReview,
		"POST /phases/{id}/validation-runs/{vid}/confirm": s.confirmItem,
		"POST /phases/{id}/reopen":                        s.reopenTasks,
		"GET /approvals":                                  s.approvals,
		"GET /approvals/{id}":                             s.approval,
		"POST /approvals/{id}/decision":                   s.decide,
	}
}

func (s *Server) projects(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	l, err := s.core.Projects(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, 200, l)
}

func (s *Server) registerProject(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	var req contract.RegisterProjectRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	id, err := s.core.RegisterProject(r.Context(), p, req.Path, req.Name)
	if err != nil {
		return err
	}
	pr, err := s.core.Project(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, 201, pr)
}

func (s *Server) projectDetail(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	d, err := s.core.ProjectDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, d)
}

func (s *Server) activateProject(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	if err := s.core.ActivateProject(r.Context(), p, r.PathValue("id")); err != nil {
		return err
	}
	pr, err := s.core.Project(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, pr)
}

func (s *Server) removeProject(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	if err := s.core.RemoveProject(r.Context(), p, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) requirements(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	l, err := s.core.Requirements(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, l)
}

func (s *Server) addRequirement(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	var req struct {
		Kind string `json:"kind"`
		Path string `json:"path"`
		Text string `json:"text"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	id, err := s.core.AddRequirement(r.Context(), p, r.PathValue("id"), req.Kind, req.Path, req.Text)
	if err != nil {
		return err
	}
	l, err := s.core.Requirements(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	for _, it := range l.Items {
		if it.ID == id {
			return writeJSON(w, 201, it)
		}
	}
	return writeJSON(w, 201, contract.Requirement{ID: id})
}

func (s *Server) planningRequest(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	id, err := s.core.CreatePlanningRequest(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	d, err := s.core.ProjectDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if d.Planning.Request != nil && d.Planning.Request.ID == id {
		return writeJSON(w, 201, d.Planning.Request)
	}
	return writeJSON(w, 201, contract.PlanningRequest{ID: id, State: "waiting"})
}

func (s *Server) workflowInfo(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	wi, err := s.core.WorkflowInfo(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, wi)
}

func (s *Server) workflowVersion(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	v, err := s.core.WorkflowVersion(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, v)
}

func (s *Server) newVersion(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	id, err := s.core.NewVersion(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	v, err := s.core.WorkflowVersion(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, 201, v.WorkflowVersionSummary)
}

func (s *Server) submitVersion(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	apr, err := s.core.SubmitVersion(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, contract.SubmitResponse{ApprovalID: apr})
}

func (s *Server) phaseReview(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	rv, err := s.core.PhaseReview(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, rv)
}

func (s *Server) reopenTasks(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	var req struct {
		TaskIDs []string `json:"task_ids"`
		Reason  string   `json:"reason"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := s.core.ReopenTasks(r.Context(), p, r.Header.Get(headerElev), r.PathValue("id"), req.TaskIDs, req.Reason); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) confirmItem(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	var req struct {
		Index int `json:"index"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := s.core.ConfirmChecklistItem(r.Context(), p, r.Header.Get(headerElev), r.PathValue("id"), r.PathValue("vid"), req.Index); err != nil {
		return err
	}
	v, err := s.core.ValidatorResult(r.Context(), r.PathValue("id"), r.PathValue("vid"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, v)
}

func (s *Server) approvals(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	q := r.URL.Query()
	l, err := s.core.Approvals(r.Context(), q.Get("state"), q.Get("project_id"), listLimit(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, l)
}

func (s *Server) approval(w http.ResponseWriter, r *http.Request, _ core.Principal) error {
	d, err := s.core.ApprovalDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, d)
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request, p core.Principal) error {
	var req struct {
		Decision       string `json:"decision"`
		SubjectHash    string `json:"subject_hash"`
		Feedback       string `json:"feedback"`
		OverrideReason string `json:"override_reason"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	err := s.core.Decide(r.Context(), p, core.Decision{ApprovalID: r.PathValue("id"), Decision: req.Decision, SubjectHash: req.SubjectHash,
		Feedback: req.Feedback, Override: req.OverrideReason, Elevation: r.Header.Get(headerElev)})
	if err != nil {
		return err
	}
	return s.approval(w, r, p)
}
