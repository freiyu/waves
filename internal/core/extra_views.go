package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"time"

	"waves/internal/api/contract"
	"waves/internal/store"
)

// Views and actions for routes the dashboard does not call (CLI, scripts).

// ProjectSettings is GET /projects/{id}/settings.
type ProjectSettings struct {
	GuardPolicy       string `json:"guard_policy"`
	UnmanagedSessions string `json:"unmanaged_sessions"`
	PromptTemplate    string `json:"prompt_template"`
	ContextBudget     int    `json:"context_budget"`
	BaseBranch        string `json:"base_branch"`
	UpdatedAt         string `json:"updated_at"`
}

func (s *Service) ProjectSettings(ctx context.Context, id string) (ProjectSettings, error) {
	var ps ProjectSettings
	var pt, bb sql.NullString
	err := s.db.R.QueryRowContext(ctx, `SELECT ps.guard_policy, ps.unmanaged_sessions, ps.prompt_template, ps.context_budget, p.base_branch, ps.updated_at
		FROM project_settings ps JOIN projects p ON p.id = ps.project_id WHERE p.id = ? AND p.removed_at IS NULL`, id).
		Scan(&ps.GuardPolicy, &ps.UnmanagedSessions, &pt, &ps.ContextBudget, &bb, &ps.UpdatedAt)
	if err != nil {
		return ps, errNotFound("project", "See waves project list.")
	}
	ps.PromptTemplate, ps.BaseBranch = pt.String, bb.String
	return ps, nil
}

// SetProjectOptions changes the non-security project settings. The route is
// human scope, so it already carries an elevation; it is checked here too.
func (s *Service) SetProjectOptions(ctx context.Context, p Principal, elevation, id string, prompt, baseBranch *string, budget *int) error {
	if prompt == nil && baseBranch == nil && budget == nil {
		return nil
	}
	if budget != nil && (*budget < 1000 || *budget > 200000) {
		return errValidation("context_budget must be 1000 to 200000.", "Use a value in the allowed range.")
	}
	if prompt != nil && len(*prompt) > 16<<10 {
		return errValidation("The prompt template is limited to 16 KiB.", "Shorten it.")
	}
	return s.tx(ctx, func(t *Tx) error {
		if err := s.RequireElevation(ctx, t, p, elevation); err != nil {
			return err
		}
		if err := s.mustProject(ctx, t, id); err != nil {
			return err
		}
		if prompt != nil {
			if _, err := t.ExecContext(ctx, `UPDATE project_settings SET prompt_template = ?, updated_at = ? WHERE project_id = ?`, store.NullStr(*prompt), s.ts(), id); err != nil {
				return wrap(err, "set prompt template")
			}
		}
		if budget != nil {
			if _, err := t.ExecContext(ctx, `UPDATE project_settings SET context_budget = ?, updated_at = ? WHERE project_id = ?`, *budget, s.ts(), id); err != nil {
				return wrap(err, "set context budget")
			}
		}
		if baseBranch != nil {
			if _, err := t.ExecContext(ctx, `UPDATE projects SET base_branch = ? WHERE id = ?`, store.NullStr(*baseBranch), id); err != nil {
				return wrap(err, "set base branch")
			}
		}
		t.publish("project.changed", map[string]string{"project_id": id})
		return t.audit(ctx, store.AuditRecord{Event: "settings.changed", ActorScope: "human", ActorRef: p.ActorRef(), ProjectID: id, Detail: map[string]any{"project_options": true}})
	})
}

// PlanningRequestView is GET /planning-requests/{id}.
func (s *Service) PlanningRequestView(ctx context.Context, id string) (map[string]any, error) {
	var project, state, body, at string
	if err := s.db.R.QueryRowContext(ctx, `SELECT project_id, state, body_json, created_at FROM planning_requests WHERE id = ?`, id).Scan(&project, &state, &body, &at); err != nil {
		return nil, errNotFound("planning request", "See the project's Plan page.")
	}
	var b any
	json.Unmarshal([]byte(body), &b)
	return map[string]any{"id": id, "project_id": project, "state": state, "created_at": store.ParseTime(at), "body": b}, nil
}

// ProposalView is one stored proposal.
type ProposalView struct {
	ID                string          `json:"id"`
	PlanningRequestID string          `json:"planning_request_id"`
	Valid             bool            `json:"valid"`
	Errors            []ProposalError `json:"errors"`
	CreatedAt         time.Time       `json:"created_at"`
	WorkflowVersionID string          `json:"workflow_version_id,omitempty"`
	Normalized        json.RawMessage `json:"normalized,omitempty"`
}

func (s *Service) scanProposal(sc interface{ Scan(...any) error }) (ProposalView, error) {
	var p ProposalView
	var valid int
	var errs, at string
	var norm, wv sql.NullString
	if err := sc.Scan(&p.ID, &p.PlanningRequestID, &valid, &errs, &at, &norm, &wv); err != nil {
		return p, err
	}
	p.Valid, p.CreatedAt, p.WorkflowVersionID, p.Errors = valid == 1, store.ParseTime(at), wv.String, []ProposalError{}
	json.Unmarshal([]byte(errs), &p.Errors)
	if norm.Valid {
		p.Normalized = json.RawMessage(norm.String)
	}
	return p, nil
}

const proposalSelect = `SELECT pr.id, pr.planning_request_id, pr.is_valid, pr.errors_json, pr.created_at, pr.normalized_json,
	(SELECT v.id FROM workflow_versions v WHERE v.proposal_id = pr.id ORDER BY v.version_no LIMIT 1) FROM proposals pr`

// Proposals lists a project's proposals, newest first.
func (s *Service) Proposals(ctx context.Context, project string) ([]ProposalView, error) {
	if err := s.mustProject(ctx, s.db.R, project); err != nil {
		return nil, err
	}
	rows, err := s.db.R.QueryContext(ctx, proposalSelect+` WHERE pr.project_id = ? ORDER BY pr.created_at DESC, pr.rowid DESC`, project)
	if err != nil {
		return nil, wrap(err, "list proposals")
	}
	defer rows.Close()
	out := []ProposalView{}
	for rows.Next() {
		p, err := s.scanProposal(rows)
		if err != nil {
			return nil, wrap(err, "scan proposal")
		}
		p.Normalized = nil
		out = append(out, p)
	}
	return out, rows.Err()
}

// Proposal is GET /proposals/{id}.
func (s *Service) Proposal(ctx context.Context, id string) (ProposalView, error) {
	p, err := s.scanProposal(s.db.R.QueryRowContext(ctx, proposalSelect+` WHERE pr.id = ?`, id))
	if err != nil {
		return p, errNotFound("proposal", "See the project's Plan page.")
	}
	return p, nil
}

// CreateWorkflowFromProposal makes a draft version from a valid proposal.
// The first valid proposal of a request already became a draft; this makes
// another draft from any valid proposal the human picks.
func (s *Service) CreateWorkflowFromProposal(ctx context.Context, p Principal, id string) (string, error) {
	if !p.IsOperator() {
		return "", errWrongScope
	}
	var vid string
	err := s.tx(ctx, func(t *Tx) error {
		var project string
		var norm sql.NullString
		if err := t.QueryRowContext(ctx, `SELECT project_id, normalized_json FROM proposals WHERE id = ? AND is_valid = 1`, id).Scan(&project, &norm); err != nil || !norm.Valid {
			return errValidation("Only a valid proposal can become a workflow.", "Fix the errors and submit a new proposal.")
		}
		var plan Plan
		if err := json.Unmarshal([]byte(norm.String), &plan); err != nil {
			return wrap(err, "read normalized proposal")
		}
		var err error
		vid, err = s.createVersion(ctx, t, project, id, &plan)
		return err
	})
	return vid, err
}

// EditDraft replaces the content of a draft version with a new proposal
// document (same schema and rules). Approved versions never change.
func (s *Service) EditDraft(ctx context.Context, p Principal, versionID string, doc []byte) ([]ProposalError, error) {
	if !p.IsOperator() {
		return nil, errWrongScope
	}
	plan, errs := ValidateProposal(doc)
	if errs != nil {
		return errs, errValidation("The edited workflow is not valid.", "Fix the listed errors.")
	}
	return nil, s.tx(ctx, func(t *Tx) error {
		var state, project string
		if err := t.QueryRowContext(ctx, `SELECT v.state, w.project_id FROM workflow_versions v JOIN workflows w ON w.id = v.workflow_id WHERE v.id = ?`, versionID).Scan(&state, &project); err != nil {
			return errNotFound("workflow version", "See waves workflow show.")
		}
		if state != "draft" {
			return errInvalidState("Only a draft can be edited.", "Create a new version first.")
		}
		var created int
		if err := t.QueryRowContext(ctx, `SELECT count(*) FROM state_transitions WHERE entity_type IN ('phase','task') AND entity_id IN
			(SELECT id FROM phases WHERE workflow_version_id = ? UNION SELECT t.id FROM tasks t JOIN phases ph ON ph.id = t.phase_id WHERE ph.workflow_version_id = ?)`, versionID, versionID).Scan(&created); err != nil {
			return wrap(err, "check draft history")
		}
		if created > 0 {
			return errInvalidState("This draft already has history.", "Create a new version instead.")
		}
		for _, q := range []string{
			`DELETE FROM task_dependencies WHERE task_id IN (SELECT t.id FROM tasks t JOIN phases ph ON ph.id = t.phase_id WHERE ph.workflow_version_id = ?)`,
			`DELETE FROM tasks WHERE parent_task_id IS NOT NULL AND phase_id IN (SELECT id FROM phases WHERE workflow_version_id = ?)`,
			`DELETE FROM tasks WHERE phase_id IN (SELECT id FROM phases WHERE workflow_version_id = ?)`,
			`DELETE FROM validators WHERE phase_id IN (SELECT id FROM phases WHERE workflow_version_id = ?)`,
			`DELETE FROM phases WHERE workflow_version_id = ?`,
		} {
			if _, err := t.ExecContext(ctx, q, versionID); err != nil {
				return wrap(err, "clear draft")
			}
		}
		if err := s.writePlan(ctx, t, versionID, plan); err != nil {
			return err
		}
		if _, err := t.ExecContext(ctx, `UPDATE workflow_versions SET content_hash = ? WHERE id = ?`, contentHash(plan), versionID); err != nil {
			return wrap(err, "update hash")
		}
		t.publish("workflow.changed", map[string]string{"project_id": project, "version_id": versionID, "state": "draft"})
		return t.audit(ctx, store.AuditRecord{Event: "workflow.edited", ActorScope: "operator", ActorRef: p.ActorRef(), ProjectID: project, Subject: versionID, Detail: map[string]any{"content_hash": contentHash(plan)}})
	})
}

// PhaseDetail is GET /phases/{id}.
func (s *Service) PhaseDetail(ctx context.Context, id string) (map[string]any, error) {
	var key, title, state, objective, del, version string
	if err := s.db.R.QueryRowContext(ctx, `SELECT key, title, state, objective, deliverables_json, workflow_version_id FROM phases WHERE id = ?`, id).Scan(&key, &title, &state, &objective, &del, &version); err != nil {
		return nil, errNotFound("phase", "See waves workflow show.")
	}
	tasks, err := s.taskSummaries(ctx, id)
	if err != nil {
		return nil, err
	}
	vals, err := s.validatorsOf(ctx, id)
	if err != nil {
		return nil, err
	}
	var d []string
	json.Unmarshal([]byte(del), &d)
	return map[string]any{"phase": phaseRef(id, key, title, state), "objective": objective, "deliverables": d, "workflow_version_id": version, "tasks": tasks, "validators": vals}, nil
}

// Tasks is GET /tasks?project_id&phase_id&state.
func (s *Service) Tasks(ctx context.Context, project, phase, state string) ([]contract.TaskSummary, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT ph.id FROM phases ph JOIN workflow_versions v ON v.id = ph.workflow_version_id JOIN workflows w ON w.id = v.workflow_id
		WHERE (? = '' OR w.project_id = ?) AND (? = '' OR ph.id = ?) AND v.state IN ('approved','draft','awaiting_approval') ORDER BY v.version_no DESC, ph.position`, project, project, phase, phase)
	if err != nil {
		return nil, wrap(err, "list phases")
	}
	var phases []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		phases = append(phases, id)
	}
	rows.Close()
	out := []contract.TaskSummary{}
	for _, ph := range phases {
		ts, err := s.taskSummaries(ctx, ph)
		if err != nil {
			return nil, err
		}
		for _, t := range ts {
			if state == "" || t.State == state {
				out = append(out, t)
			}
		}
	}
	return out, nil
}

// TaskDetail is GET /tasks/{id}: the task with its execution history.
func (s *Service) TaskDetail(ctx context.Context, id string) (map[string]any, error) {
	var phase, project string
	if err := s.db.R.QueryRowContext(ctx, `SELECT t.phase_id, w.project_id FROM tasks t JOIN phases ph ON ph.id = t.phase_id JOIN workflow_versions v ON v.id = ph.workflow_version_id JOIN workflows w ON w.id = v.workflow_id WHERE t.id = ?`, id).Scan(&phase, &project); err != nil {
		return nil, errNotFound("task", "See waves task list.")
	}
	ts, err := s.taskSummaries(ctx, phase)
	if err != nil {
		return nil, err
	}
	var task contract.TaskSummary
	for _, t := range ts {
		if t.ID == id {
			task = t
		}
	}
	ex, err := s.Executions(ctx, project, "", id, 200)
	if err != nil {
		return nil, err
	}
	var desc, acc string
	s.db.R.QueryRowContext(ctx, `SELECT description, acceptance_json FROM tasks WHERE id = ?`, id).Scan(&desc, &acc)
	var accList []string
	json.Unmarshal([]byte(acc), &accList)
	return map[string]any{"task": task, "description": desc, "acceptance_criteria": accList, "phase_id": phase, "project_id": project, "executions": ex.Items}, nil
}

// DoctorCheck is one line of GET /doctor.
type DoctorCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// Doctor checks the database, the audit chain, directory modes and the tools Waves uses.
func (s *Service) Doctor(ctx context.Context) []DoctorCheck {
	var out []DoctorCheck
	add := func(n string, ok bool, d string) { out = append(out, DoctorCheck{n, ok, d}) }
	var ic string
	s.db.R.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&ic)
	add("database", ic == "ok", s.db.Path+": "+ic)
	var fk int
	s.db.R.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk)
	add("foreign keys", fk == 1, "PRAGMA foreign_keys = "+itoa(int64(fk)))
	st := s.AuditStatus(ctx)
	add("audit chain", st.State == "verified", st.State+", "+itoa(int64(st.Records))+" records")
	for _, d := range []string{s.dirs.Config, s.dirs.Data, s.dirs.State} {
		fi, err := os.Stat(d)
		add("mode "+d, err == nil && fi.Mode().Perm() == 0o700, "want 700")
	}
	for _, bin := range []string{"git", "claude"} {
		p, err := exec.LookPath(bin)
		if err != nil {
			p = "not on PATH; install it"
		}
		add(bin, err == nil, p)
	}
	if v := s.ClaudeCodeVersion(ctx); v != "" {
		add("claude code version", true, v)
	}
	return out
}
