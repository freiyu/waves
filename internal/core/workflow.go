package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"

	"waves/internal/store"
)

// planningInstructions is what the harness reads with the planning request.
const planningInstructions = `Read the requirements below. Propose a workflow as one JSON document that follows the schema.
Rules: unknown fields are rejected; keys match ^[a-z0-9][a-z0-9_-]{0,63}$ and are unique in the document;
every phase needs an objective, at least one task and at least one validator; a code_change task needs acceptance criteria;
depends_on names tasks in the same or an earlier phase, without cycles; command validators run with /bin/sh -c in the
project folder after a human approves the exact command text. Submit with waves_submit_workflow_proposal. Nothing runs until a human approves.`

// proposalShape is the proposal schema the harness receives: every field the
// validator accepts, with its type or allowed values. "?" marks optional fields.
const proposalShape = `{
  "schema_version": 1,
  "summary": "string, 1..2000 chars",
  "assumptions?": ["string"],
  "open_questions?": ["string"],
  "risks?": [{"description": "string", "severity?": "low|medium|high"}],
  "phases": [{
    "key": "p1",
    "title": "string, 1..120",
    "objective": "string, 1..2000",
    "deliverables?": ["string"],
    "validators": [
      {"type": "command", "name": "string, 1..120", "command": "shell command text", "timeout_s?": "integer 1..3600, default 300"},
      {"type": "checklist", "name": "string, 1..120", "items": ["string"]}
    ],
    "tasks": [{
      "key": "p1-t1",
      "title": "string, 1..160",
      "description?": "string",
      "priority?": "low|normal|high",
      "depends_on?": ["task key in this or an earlier phase"],
      "acceptance_criteria": ["string; required for code_change tasks"],
      "workspace_mode?": "code_change|read_only, default code_change",
      "suggested_harness?": "string",
      "subtasks?": [{"key": "p1-t1-s1", "title": "string", "description?": "string"}]
    }]
  }]
}`

const excerptBudget = 24000

// CreatePlanningRequest opens a planning request with the project's
// requirements, the proposal schema and the rules. An open request is reused.
func (s *Service) CreatePlanningRequest(ctx context.Context, p Principal, projectID string) (string, error) {
	if !p.IsOperator() {
		return "", errWrongScope
	}
	root, err := s.projectPath(ctx, s.db.R, projectID)
	if err != nil {
		return "", err
	}
	type ref struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Path    string `json:"path,omitempty"`
		SHA256  string `json:"sha256,omitempty"`
		Excerpt string `json:"excerpt"`
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, kind, COALESCE(path,''), COALESCE(content_hash,''), COALESCE(body,'') FROM requirements WHERE project_id = ? ORDER BY created_at`, projectID)
	if err != nil {
		return "", wrap(err, "read requirements")
	}
	refs := []ref{}
	budget := excerptBudget
	for rows.Next() {
		var r ref
		var body string
		if err := rows.Scan(&r.ID, &r.Kind, &r.Path, &r.SHA256, &body); err != nil {
			rows.Close()
			return "", wrap(err, "scan requirement")
		}
		if r.Kind == "file" {
			if real, err := InsideRoot(root, r.Path); err == nil {
				if b, err := readHead(real, budget); err == nil {
					body = string(b)
				}
			}
		}
		if len(body) > budget {
			body = body[:budget]
		}
		budget -= len(body)
		r.Excerpt = body
		refs = append(refs, r)
	}
	rows.Close()
	if len(refs) == 0 {
		return "", errValidation("The project has no requirements yet.", "Add a requirement file or text first.")
	}
	var name string
	s.db.R.QueryRowContext(ctx, `SELECT name FROM projects WHERE id = ?`, projectID).Scan(&name)
	body, _ := json.Marshal(map[string]any{"project": name, "requirements": refs, "schema_version": proposalSchemaVer, "instructions": planningInstructions, "schema": json.RawMessage(proposalShape),
		"limits": map[string]int{"phases": maxPhases, "tasks_per_phase": maxTasksPerPhase, "subtasks_per_task": maxSubtasks, "validators_per_phase": maxValidators, "command_bytes": maxCommandBytes, "timeout_s_max": maxValidatorSecs}})
	id := NewID("plr")
	err = s.tx(ctx, func(t *Tx) error {
		if _, err := t.ExecContext(ctx, `UPDATE planning_requests SET state = 'cancelled' WHERE project_id = ? AND state = 'open'`, projectID); err != nil {
			return wrap(err, "close old planning requests")
		}
		if _, err := t.ExecContext(ctx, `INSERT INTO planning_requests(id, project_id, state, body_json, created_at) VALUES (?,?, 'open', ?, ?)`, id, projectID, string(body), s.ts()); err != nil {
			return wrap(err, "store planning request")
		}
		t.publish("project.changed", map[string]string{"project_id": projectID})
		return t.audit(ctx, store.AuditRecord{Event: "planning.requested", ActorScope: "operator", ActorRef: p.ActorRef(), ProjectID: projectID, Subject: id})
	})
	return id, err
}

func readHead(path string, n int) ([]byte, error) {
	if st, err := os.Lstat(path); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, n)
	k, err := f.Read(b)
	if k == 0 && err != nil {
		return nil, err
	}
	return b[:k], nil
}

// PlanningRequestBody returns the open (or named) planning request for an
// agent of projectID, with the errors of its latest invalid proposal.
func (s *Service) PlanningRequestBody(ctx context.Context, projectID, id string) (map[string]any, error) {
	q := `SELECT id, body_json FROM planning_requests WHERE project_id = ? AND state = 'open' ORDER BY created_at DESC, rowid DESC LIMIT 1`
	args := []any{projectID}
	if id != "" {
		q = `SELECT id, body_json FROM planning_requests WHERE project_id = ? AND id = ? AND state = 'open'`
		args = append(args, id)
	}
	var pid, body string
	if err := s.db.R.QueryRowContext(ctx, q, args...).Scan(&pid, &body); err != nil {
		return nil, errNotFound("open planning request", "Ask the human to create one: waves plan request.")
	}
	var out map[string]any
	json.Unmarshal([]byte(body), &out)
	out["planning_request_id"] = pid
	var errs string
	if s.db.R.QueryRowContext(ctx, `SELECT errors_json FROM proposals WHERE planning_request_id = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`, pid).Scan(&errs) == nil {
		var e []ProposalError
		json.Unmarshal([]byte(errs), &e)
		out["previous_errors"] = e
	}
	s.Audit(ctx, store.AuditRecord{Event: "planning.fetched", ActorScope: "agent", ProjectID: projectID, Subject: pid})
	return out, nil
}

// SubmitProposal stores a proposal exactly as submitted and validates it. A
// valid proposal becomes a draft workflow version and closes the request.
func (s *Service) SubmitProposal(ctx context.Context, projectID, planningID string, raw []byte, via string) (string, bool, []ProposalError, error) {
	plan, errs := ValidateProposal(raw)
	if errs == nil {
		errs = []ProposalError{}
	}
	id := NewID("prp")
	errJSON, _ := json.Marshal(errs)
	var norm any
	if plan != nil {
		b, _ := json.Marshal(plan)
		norm = string(b)
	}
	err := s.tx(ctx, func(t *Tx) error {
		var state, pid string
		err := t.QueryRowContext(ctx, `SELECT state, project_id FROM planning_requests WHERE id = ?`, planningID).Scan(&state, &pid)
		if err != nil || pid != projectID {
			return errNotFound("planning request", "Fetch the open request with waves_get_planning_request.")
		}
		if state != "open" {
			return errInvalidState("This planning request is closed.", "Ask the human for a new planning request.")
		}
		if _, err := t.ExecContext(ctx, `INSERT INTO proposals(id, planning_request_id, project_id, schema_version, raw_json, normalized_json, is_valid, errors_json, submitted_via, created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			id, planningID, projectID, proposalSchemaVer, string(raw), norm, b2i(plan != nil), string(errJSON), via, s.ts()); err != nil {
			return wrap(err, "store proposal")
		}
		ev := "proposal.invalid"
		if plan != nil {
			ev = "proposal.submitted"
		}
		if err := t.audit(ctx, store.AuditRecord{Event: ev, ActorScope: "agent", ActorRef: via, ProjectID: projectID, Subject: id, Detail: map[string]any{"errors": len(errs)}}); err != nil {
			return err
		}
		t.publish("project.changed", map[string]string{"project_id": projectID})
		if plan == nil {
			return nil
		}
		if _, err := t.ExecContext(ctx, `UPDATE planning_requests SET state = 'submitted' WHERE id = ?`, planningID); err != nil {
			return wrap(err, "close planning request")
		}
		_, err = s.createVersion(ctx, t, projectID, id, plan)
		return err
	})
	if err != nil {
		return "", false, nil, err
	}
	return id, plan != nil, errs, nil
}

// contentHash covers phases, tasks, dependencies and the exact validator commands.
func contentHash(p *Plan) string {
	// Dependency order carries no meaning; sort it so a stored copy hashes the same.
	phases := make([]PlanPhase, len(p.Phases))
	copy(phases, p.Phases)
	for i := range phases {
		tasks := make([]PlanTask, len(phases[i].Tasks))
		copy(tasks, phases[i].Tasks)
		for j := range tasks {
			d := append([]string{}, tasks[j].DependsOn...)
			sort.Strings(d)
			tasks[j].DependsOn = d
		}
		phases[i].Tasks = tasks
	}
	b, _ := json.Marshal(phases)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// createVersion writes a draft version with Planned phases and Backlog tasks.
func (s *Service) createVersion(ctx context.Context, t *Tx, projectID, proposalID string, plan *Plan) (string, error) {
	var wf string
	err := t.QueryRowContext(ctx, `SELECT id FROM workflows WHERE project_id = ?`, projectID).Scan(&wf)
	if errors.Is(err, sql.ErrNoRows) {
		wf = NewID("wfl")
		if _, err := t.ExecContext(ctx, `INSERT INTO workflows(id, project_id, created_at) VALUES (?,?,?)`, wf, projectID, s.ts()); err != nil {
			return "", wrap(err, "create workflow")
		}
	} else if err != nil {
		return "", wrap(err, "read workflow")
	}
	var no int
	t.QueryRowContext(ctx, `SELECT COALESCE(max(version_no),0)+1 FROM workflow_versions WHERE workflow_id = ?`, wf).Scan(&no)
	vid := NewID("wfv")
	now := s.ts()
	if _, err := t.ExecContext(ctx, `INSERT INTO workflow_versions(id, workflow_id, version_no, proposal_id, state, content_hash, created_at) VALUES (?,?,?,?, 'draft', ?, ?)`,
		vid, wf, no, store.NullStr(proposalID), contentHash(plan), now); err != nil {
		return "", wrap(err, "create workflow version")
	}
	if err := s.writePlan(ctx, t, vid, plan); err != nil {
		return "", err
	}
	t.publish("workflow.changed", map[string]string{"project_id": projectID, "version_id": vid, "state": "draft"})
	return vid, t.audit(ctx, store.AuditRecord{Event: "workflow.created", ActorScope: "system", ProjectID: projectID, Subject: vid, Detail: map[string]any{"version": no, "proposal_id": proposalID}})
}

// writePlan writes phases (Planned), tasks (Backlog), dependencies and
// validators of a plan under a version.
func (s *Service) writePlan(ctx context.Context, t *Tx, vid string, plan *Plan) error {
	now := s.ts()
	taskIDs := map[string]string{}
	for pi, ph := range plan.Phases {
		phID := NewID("phs")
		del, _ := json.Marshal(ph.Deliverables)
		if _, err := t.ExecContext(ctx, `INSERT INTO phases(id, workflow_version_id, key, position, title, objective, deliverables_json, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
			phID, vid, ph.Key, pi+1, ph.Title, ph.Objective, string(del), now); err != nil {
			return wrap(err, "create phase")
		}
		for vi, v := range ph.Validators {
			var items any
			if v.Type == "checklist" {
				b, _ := json.Marshal(v.Items)
				items = string(b)
			}
			var cmd, to any
			if v.Type == "command" {
				cmd, to = v.Command, v.TimeoutS
			}
			if _, err := t.ExecContext(ctx, `INSERT INTO validators(id, phase_id, type, name, command, timeout_s, items_json, position) VALUES (?,?,?,?,?,?,?,?)`,
				NewID("val"), phID, v.Type, v.Name, cmd, to, items, vi+1); err != nil {
				return wrap(err, "create validator")
			}
		}
		for ti, tk := range ph.Tasks {
			tid := NewID("tsk")
			taskIDs[tk.Key] = tid
			acc, _ := json.Marshal(tk.Acceptance)
			if _, err := t.ExecContext(ctx, `INSERT INTO tasks(id, phase_id, key, position, title, description, priority, workspace_mode, acceptance_json, suggested_harness, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
				tid, phID, tk.Key, ti+1, tk.Title, tk.Description, tk.Priority, tk.WorkspaceMode, string(acc), store.NullStr(tk.SuggestedHarness), now); err != nil {
				return wrap(err, "create task")
			}
			for si, st := range tk.Subtasks {
				if _, err := t.ExecContext(ctx, `INSERT INTO tasks(id, phase_id, parent_task_id, key, position, title, description, workspace_mode, updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
					NewID("tsk"), phID, tid, st.Key, 1000*(ti+1)+si+1, st.Title, st.Description, tk.WorkspaceMode, now); err != nil {
					return wrap(err, "create subtask")
				}
			}
		}
	}
	for _, ph := range plan.Phases {
		for _, tk := range ph.Tasks {
			for _, d := range tk.DependsOn {
				if _, err := t.ExecContext(ctx, `INSERT INTO task_dependencies(task_id, depends_on_task_id) VALUES (?,?)`, taskIDs[tk.Key], taskIDs[d]); err != nil {
					return wrap(err, "create dependency")
				}
			}
		}
	}
	return nil
}

// planOf rebuilds the plan of a stored version (for copies and hashing).
func (s *Service) planOf(ctx context.Context, q store.Execer, versionID string) (*Plan, string, error) {
	var project string
	if err := q.QueryRowContext(ctx, `SELECT w.project_id FROM workflow_versions v JOIN workflows w ON w.id = v.workflow_id WHERE v.id = ?`, versionID).Scan(&project); err != nil {
		return nil, "", errNotFound("workflow version", "Open the project workflow.")
	}
	p := &Plan{SchemaVersion: proposalSchemaVer}
	rows, err := q.QueryContext(ctx, `SELECT id, key, title, objective, deliverables_json FROM phases WHERE workflow_version_id = ? ORDER BY position`, versionID)
	if err != nil {
		return nil, "", wrap(err, "read phases")
	}
	type ph struct {
		id string
		PlanPhase
	}
	var phs []ph
	for rows.Next() {
		var x ph
		var del string
		rows.Scan(&x.id, &x.Key, &x.Title, &x.Objective, &del)
		json.Unmarshal([]byte(del), &x.Deliverables)
		phs = append(phs, x)
	}
	rows.Close()
	for _, x := range phs {
		vr, err := q.QueryContext(ctx, `SELECT type, name, COALESCE(command,''), COALESCE(timeout_s,0), COALESCE(items_json,'') FROM validators WHERE phase_id = ? ORDER BY position`, x.id)
		if err != nil {
			return nil, "", wrap(err, "read validators")
		}
		for vr.Next() {
			var v PlanValidator
			var items string
			vr.Scan(&v.Type, &v.Name, &v.Command, &v.TimeoutS, &items)
			if items != "" {
				json.Unmarshal([]byte(items), &v.Items)
			}
			x.Validators = append(x.Validators, v)
		}
		vr.Close()
		tr, err := q.QueryContext(ctx, `SELECT t.id, t.key, t.title, t.description, t.priority, t.workspace_mode, t.acceptance_json, COALESCE(t.suggested_harness,''),
			COALESCE((SELECT json_group_array(d.key) FROM task_dependencies td JOIN tasks d ON d.id = td.depends_on_task_id WHERE td.task_id = t.id),'[]')
			FROM tasks t WHERE t.phase_id = ? AND t.parent_task_id IS NULL ORDER BY t.position`, x.id)
		if err != nil {
			return nil, "", wrap(err, "read tasks")
		}
		type tk struct {
			id string
			PlanTask
		}
		var tks []tk
		for tr.Next() {
			var k tk
			var acc, deps string
			tr.Scan(&k.id, &k.Key, &k.Title, &k.Description, &k.Priority, &k.WorkspaceMode, &acc, &k.SuggestedHarness, &deps)
			json.Unmarshal([]byte(acc), &k.Acceptance)
			json.Unmarshal([]byte(deps), &k.DependsOn)
			tks = append(tks, k)
		}
		tr.Close()
		for _, k := range tks {
			k.Subtasks = []PlanSubtask{}
			sr, err := q.QueryContext(ctx, `SELECT key, title, description FROM tasks WHERE parent_task_id = ? ORDER BY position`, k.id)
			if err != nil {
				return nil, "", wrap(err, "read subtasks")
			}
			for sr.Next() {
				var st PlanSubtask
				sr.Scan(&st.Key, &st.Title, &st.Description)
				k.Subtasks = append(k.Subtasks, st)
			}
			sr.Close()
			x.Tasks = append(x.Tasks, k.PlanTask)
		}
		p.Phases = append(p.Phases, x.PlanPhase)
	}
	return p, project, nil
}

// NewVersion copies an approved, rejected or superseded version into a new
// draft (a scope change is always a new version).
func (s *Service) NewVersion(ctx context.Context, p Principal, fromID string) (string, error) {
	if !p.IsOperator() {
		return "", errWrongScope
	}
	var vid string
	err := s.tx(ctx, func(t *Tx) error {
		var state string
		if err := t.QueryRowContext(ctx, `SELECT state FROM workflow_versions WHERE id = ?`, fromID).Scan(&state); err != nil {
			return errNotFound("workflow version", "Open the project workflow.")
		}
		if state == "draft" || state == "awaiting_approval" {
			return errInvalidState("This version is still a draft or awaits approval.", "Edit or decide it first.")
		}
		plan, project, err := s.planOf(ctx, t, fromID)
		if err != nil {
			return err
		}
		var prop sql.NullString
		t.QueryRowContext(ctx, `SELECT proposal_id FROM workflow_versions WHERE id = ?`, fromID).Scan(&prop)
		vid, err = s.createVersion(ctx, t, project, prop.String, plan)
		return err
	})
	return vid, err
}

// SubmitVersion moves a draft to awaiting_approval and requests the
// workflow approval, bound to the content hash.
func (s *Service) SubmitVersion(ctx context.Context, p Principal, versionID string) (string, error) {
	if !p.IsOperator() {
		return "", errWrongScope
	}
	var apr string
	err := s.tx(ctx, func(t *Tx) error {
		var state, hash, project string
		var no int
		if err := t.QueryRowContext(ctx, `SELECT v.state, v.content_hash, w.project_id, v.version_no FROM workflow_versions v JOIN workflows w ON w.id = v.workflow_id WHERE v.id = ?`, versionID).Scan(&state, &hash, &project, &no); err != nil {
			return errNotFound("workflow version", "Open the project workflow.")
		}
		if state != "draft" {
			return errInvalidState("Only a draft can be submitted.", "Create a new version first.")
		}
		// The stored hash must still match the content (a direct edit would show here).
		plan, _, err := s.planOf(ctx, t, versionID)
		if err != nil {
			return err
		}
		if contentHash(plan) != hash {
			return fail(http.StatusConflict, "stale_subject", "The version content does not match its hash.", "Create a new version.")
		}
		if _, err := s.transition(ctx, t, "workflow_version", versionID, "awaiting_approval", actorOf(p), "submitted", ""); err != nil {
			return err
		}
		impact := map[string]any{"version": no, "content_hash": hash}
		apr, err = s.requestApproval(ctx, t, "workflow", "workflow_version", versionID, project, "operator", hash, impact)
		return err
	})
	return apr, err
}

func versionCommands(plan *Plan) []string {
	out := []string{}
	for _, ph := range plan.Phases {
		for _, v := range ph.Validators {
			if v.Type == "command" {
				out = append(out, fmt.Sprintf("[%s] %s (timeout %d s): %s", ph.Key, v.Name, v.TimeoutS, v.Command))
			}
		}
	}
	return out
}
