package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"waves/internal/api/contract"
	"waves/internal/store"
)

// Read models: the API returns these contract types as they are.

func tptr(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t := store.ParseTime(s.String)
	return &t
}

func phaseRef(id, key, title, state string) contract.PhaseRef {
	return contract.PhaseRef{ID: id, Code: strings.ToUpper(key), Title: title, State: snake(state)}
}

// currentVersion is the approved version of a project, else the newest.
func (s *Service) currentVersion(ctx context.Context, projectID string) string {
	var id string
	s.db.R.QueryRowContext(ctx, `SELECT v.id FROM workflow_versions v JOIN workflows w ON w.id = v.workflow_id WHERE w.project_id = ?
		ORDER BY (v.state = 'approved') DESC, v.version_no DESC LIMIT 1`, projectID).Scan(&id)
	return id
}

// activePhase is the first phase of the current version that is not completed.
func (s *Service) activePhase(ctx context.Context, versionID string) *contract.PhaseRef {
	var id, key, title, state string
	err := s.db.R.QueryRowContext(ctx, `SELECT id, key, title, state FROM phases WHERE workflow_version_id = ? AND state <> 'Completed' ORDER BY position LIMIT 1`, versionID).Scan(&id, &key, &title, &state)
	if err != nil {
		return nil
	}
	r := phaseRef(id, key, title, state)
	return &r
}

// Projects lists registered projects.
func (s *Service) Projects(ctx context.Context) (contract.ProjectList, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT id FROM projects WHERE removed_at IS NULL ORDER BY is_active DESC, name`)
	if err != nil {
		return contract.ProjectList{}, wrap(err, "list projects")
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := contract.ProjectList{Items: []contract.Project{}}
	for _, id := range ids {
		p, err := s.Project(ctx, id)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, p)
	}
	return out, nil
}

// Project is one project summary.
func (s *Service) Project(ctx context.Context, id string) (contract.Project, error) {
	var p contract.Project
	var isGit, active int
	var base sql.NullString
	err := s.db.R.QueryRowContext(ctx, `SELECT p.id, p.name, p.path, p.is_git, p.base_branch, p.is_active, ps.guard_policy FROM projects p JOIN project_settings ps ON ps.project_id = p.id
		WHERE p.id = ? AND p.removed_at IS NULL`, id).Scan(&p.ID, &p.Name, &p.Path, &isGit, &base, &active, &p.GuardPolicy)
	if err != nil {
		return p, errNotFound("project", "Open the Projects list.")
	}
	p.IsGit, p.Active, p.BaseBranch = isGit == 1, active == 1, base.String
	if p.IsGit {
		_, p.Branch = gitInfo(ctx, p.Path)
	}
	if v := s.currentVersion(ctx, id); v != "" {
		p.ActivePhase = s.activePhase(ctx, v)
		var done, all int
		s.db.R.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE t.state IN ('Done','Cancelled')), count(*) FROM tasks t JOIN phases ph ON ph.id = t.phase_id WHERE ph.workflow_version_id = ? AND t.parent_task_id IS NULL`, v).Scan(&done, &all)
		if all > 0 {
			p.ProgressPct = done * 100 / all
		}
	}
	s.db.R.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE state IN ('Running','Starting','Waiting Approval')), count(*) FILTER (WHERE state = 'Failed') FROM executions WHERE project_id = ?`, id).Scan(&p.RunningExecutions, &p.FailedExecutions)
	s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM approvals WHERE project_id = ? AND state = 'pending'`, id).Scan(&p.PendingApprovals)
	var last sql.NullString
	s.db.R.QueryRowContext(ctx, `SELECT max(ts) FROM audit WHERE project_id = ?`, id).Scan(&last)
	p.LastActivityAt = tptr(last)
	return p, nil
}

// ProjectDetail is the project overview page.
func (s *Service) ProjectDetail(ctx context.Context, id string) (contract.ProjectDetail, error) {
	p, err := s.Project(ctx, id)
	if err != nil {
		return contract.ProjectDetail{}, err
	}
	d := contract.ProjectDetail{Project: p, Phases: []contract.PhaseRef{}, Tasks: []contract.TaskSummary{}, PendingApprovals: []contract.ApprovalSummary{}, Requirements: []contract.RequirementRef{}}
	if v := s.currentVersion(ctx, id); v != "" {
		rows, err := s.db.R.QueryContext(ctx, `SELECT id, key, title, state FROM phases WHERE workflow_version_id = ? ORDER BY position`, v)
		if err != nil {
			return d, wrap(err, "list phases")
		}
		for rows.Next() {
			var pid, key, title, st string
			rows.Scan(&pid, &key, &title, &st)
			d.Phases = append(d.Phases, phaseRef(pid, key, title, st))
		}
		rows.Close()
		if p.ActivePhase != nil {
			d.TasksPhase = p.ActivePhase.ID
			d.Tasks, err = s.taskSummaries(ctx, p.ActivePhase.ID)
			if err != nil {
				return d, err
			}
		}
	}
	al, err := s.Approvals(ctx, "pending", id, 200)
	if err != nil {
		return d, err
	}
	d.PendingApprovals = al.Items
	reqs, err := s.Requirements(ctx, id)
	if err != nil {
		return d, err
	}
	for _, r := range reqs.Items {
		d.Requirements = append(d.Requirements, contract.RequirementRef{Source: r.Source, State: r.State})
	}
	var pr contract.PlanningRequest
	var prState string
	var created string
	if s.db.R.QueryRowContext(ctx, `SELECT id, state, created_at FROM planning_requests WHERE project_id = ? AND state IN ('open','submitted') ORDER BY created_at DESC, rowid DESC LIMIT 1`, id).Scan(&pr.ID, &prState, &created) == nil {
		pr.CreatedAt = store.ParseTime(created)
		pr.State = "submitted"
		if prState == "open" {
			pr.State = "waiting"
			var n int
			s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM audit WHERE event = 'planning.fetched' AND subject = ?`, pr.ID).Scan(&n)
			if n > 0 {
				pr.State = "fetched"
			}
		}
		d.Planning.Request = &pr
	}
	var prop contract.Proposal
	var valid int
	var errs, pcreated string
	if s.db.R.QueryRowContext(ctx, `SELECT id, is_valid, errors_json, created_at FROM proposals WHERE project_id = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`, id).Scan(&prop.ID, &valid, &errs, &pcreated) == nil {
		prop.Valid, prop.CreatedAt, prop.Errors = valid == 1, store.ParseTime(pcreated), []contract.ProposalError{}
		json.Unmarshal([]byte(errs), &prop.Errors)
		s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM proposals WHERE project_id = ? AND created_at <= ?`, id, pcreated).Scan(&prop.Version)
		var wv sql.NullString
		s.db.R.QueryRowContext(ctx, `SELECT id FROM workflow_versions WHERE proposal_id = ? ORDER BY version_no LIMIT 1`, prop.ID).Scan(&wv)
		prop.WorkflowVersionID = wv.String
		d.Planning.LatestProposal = &prop
	}
	if h, err := s.harnessBrief(ctx, id); err == nil {
		d.Harness = h
	}
	return d, nil
}

func (s *Service) taskSummaries(ctx context.Context, phaseID string) ([]contract.TaskSummary, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT t.id, t.key, t.title, t.state, COALESCE(t.blocked_reason,''), t.workspace_mode,
		COALESCE((SELECT json_group_array(upper(d.key)) FROM task_dependencies td JOIN tasks d ON d.id = td.depends_on_task_id WHERE td.task_id = t.id),'[]'),
		COALESCE((SELECT e.id FROM executions e WHERE e.task_id = t.id ORDER BY e.created_at DESC, e.rowid DESC LIMIT 1),'')
		FROM tasks t WHERE t.phase_id = ? AND t.parent_task_id IS NULL ORDER BY t.position`, phaseID)
	if err != nil {
		return nil, wrap(err, "list tasks")
	}
	defer rows.Close()
	out := []contract.TaskSummary{}
	for rows.Next() {
		var t contract.TaskSummary
		var st, deps string
		rows.Scan(&t.ID, &t.Code, &t.Title, &st, &t.StateDetail, &t.Mode, &deps, &t.ExecutionID)
		t.Code, t.State, t.DependsOn = strings.ToUpper(t.Code), snake(st), []string{}
		json.Unmarshal([]byte(deps), &t.DependsOn)
		out = append(out, t)
	}
	return out, rows.Err()
}

// Requirements lists requirement sources with their current hash.
func (s *Service) Requirements(ctx context.Context, projectID string) (contract.RequirementList, error) {
	out := contract.RequirementList{Items: []contract.Requirement{}}
	if err := s.mustProject(ctx, s.db.R, projectID); err != nil {
		return out, err
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, kind, COALESCE(path,''), COALESCE(content_hash,''), COALESCE(body,''), created_at FROM requirements WHERE project_id = ? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return out, wrap(err, "list requirements")
	}
	defer rows.Close()
	cur := s.currentVersion(ctx, projectID)
	var curNo int
	s.db.R.QueryRowContext(ctx, `SELECT version_no FROM workflow_versions WHERE id = ?`, cur).Scan(&curNo)
	for rows.Next() {
		var r contract.Requirement
		var kind, path, hash, body, at string
		rows.Scan(&r.ID, &kind, &path, &hash, &body, &at)
		r.AddedAt, r.State = store.ParseTime(at), "unchanged"
		if kind == "file" {
			r.Kind, r.Source, r.RecordedHash = "file", path, hash
			if now, err := fileHash(path); err == nil {
				r.HashNow = now
			}
			if r.HashNow != r.RecordedHash {
				r.State = "changed"
			}
		} else {
			r.Kind, r.Source = "text", "Inline: "+truncate(body, 60)
		}
		if cur != "" {
			r.UsedBy, r.VersionID = fmt.Sprintf("Workflow v%d", curNo), cur
		}
		out.Items = append(out.Items, r)
	}
	return out, rows.Err()
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}

// WorkflowInfo lists a project's workflow versions.
func (s *Service) WorkflowInfo(ctx context.Context, projectID string) (contract.WorkflowInfo, error) {
	out := contract.WorkflowInfo{Versions: []contract.WorkflowVersionSummary{}}
	if err := s.mustProject(ctx, s.db.R, projectID); err != nil {
		return out, err
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT v.id, v.version_no, v.state, v.content_hash, v.created_at FROM workflow_versions v JOIN workflows w ON w.id = v.workflow_id WHERE w.project_id = ? ORDER BY v.version_no DESC`, projectID)
	if err != nil {
		return out, wrap(err, "list versions")
	}
	defer rows.Close()
	for rows.Next() {
		var v contract.WorkflowVersionSummary
		var at string
		rows.Scan(&v.ID, &v.Number, &v.State, &v.ContentHash, &at)
		v.CreatedAt = store.ParseTime(at)
		out.Versions = append(out.Versions, v)
	}
	out.CurrentID = s.currentVersion(ctx, projectID)
	return out, rows.Err()
}

// WorkflowVersion is one version with phases, tasks and exact commands.
func (s *Service) WorkflowVersion(ctx context.Context, id string) (contract.WorkflowVersion, error) {
	var v contract.WorkflowVersion
	var at string
	var prop sql.NullString
	err := s.db.R.QueryRowContext(ctx, `SELECT v.id, v.version_no, v.state, v.content_hash, v.created_at, w.project_id, p.name, v.proposal_id FROM workflow_versions v
		JOIN workflows w ON w.id = v.workflow_id JOIN projects p ON p.id = w.project_id WHERE v.id = ?`, id).Scan(&v.ID, &v.Number, &v.State, &v.ContentHash, &at, &v.ProjectID, &v.ProjectName, &prop)
	if err != nil {
		return v, errNotFound("workflow version", "Open the project workflow.")
	}
	v.CreatedAt, v.ProposalID, v.Phases = store.ParseTime(at), prop.String, []contract.WorkflowPhase{}
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, key, title, objective FROM phases WHERE workflow_version_id = ? ORDER BY position`, id)
	if err != nil {
		return v, wrap(err, "list phases")
	}
	for rows.Next() {
		var ph contract.WorkflowPhase
		rows.Scan(&ph.ID, &ph.Code, &ph.Title, &ph.Objective)
		ph.Code = strings.ToUpper(ph.Code)
		v.Phases = append(v.Phases, ph)
	}
	rows.Close()
	for i := range v.Phases {
		ph := &v.Phases[i]
		ph.Tasks, ph.Validators = []contract.WorkflowTask{}, []contract.Validator{}
		tr, err := s.db.R.QueryContext(ctx, `SELECT key, title, workspace_mode, acceptance_json FROM tasks WHERE phase_id = ? AND parent_task_id IS NULL ORDER BY position`, ph.ID)
		if err != nil {
			return v, wrap(err, "list tasks")
		}
		for tr.Next() {
			var t contract.WorkflowTask
			var acc string
			tr.Scan(&t.Code, &t.Title, &t.Mode, &acc)
			t.Code, t.Acceptance = strings.ToUpper(t.Code), []string{}
			json.Unmarshal([]byte(acc), &t.Acceptance)
			ph.Tasks = append(ph.Tasks, t)
		}
		tr.Close()
		vals, err := s.validatorsOf(ctx, ph.ID)
		if err != nil {
			return v, err
		}
		ph.Validators = vals
	}
	return v, nil
}

func (s *Service) validatorsOf(ctx context.Context, phaseID string) ([]contract.Validator, error) {
	vr, err := s.db.R.QueryContext(ctx, `SELECT id, type, name, COALESCE(command,''), COALESCE(timeout_s,0), COALESCE(items_json,'') FROM validators WHERE phase_id = ? ORDER BY position`, phaseID)
	if err != nil {
		return nil, wrap(err, "list validators")
	}
	defer vr.Close()
	out := []contract.Validator{}
	for vr.Next() {
		var x contract.Validator
		var items string
		vr.Scan(&x.ID, &x.Kind, &x.Name, &x.Command, &x.TimeoutS, &items)
		if items != "" {
			json.Unmarshal([]byte(items), &x.Items)
		}
		out = append(out, x)
	}
	return out, vr.Err()
}

// PhaseReview is the review package of a phase in or after validation.
func (s *Service) PhaseReview(ctx context.Context, phaseID string) (contract.PhaseReview, error) {
	var r contract.PhaseReview
	var key, title, state string
	err := s.db.R.QueryRowContext(ctx, `SELECT p.key, p.title, p.state, w.project_id, pr.name, v.version_no FROM phases p JOIN workflow_versions v ON v.id = p.workflow_version_id
		JOIN workflows w ON w.id = v.workflow_id JOIN projects pr ON pr.id = w.project_id WHERE p.id = ?`, phaseID).Scan(&key, &title, &state, &r.ProjectID, &r.ProjectName, &r.WorkflowNumber)
	if err != nil {
		return r, errNotFound("phase", "")
	}
	var runs int
	s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM validation_runs WHERE phase_id = ?`, phaseID).Scan(&runs)
	if runs == 0 {
		return r, errNotFound("review package", "A package exists once the phase reaches validation.")
	}
	r.Phase, r.GeneratedAt = phaseRef(phaseID, key, title, state), s.now()
	r.Validators, r.Tasks, r.Findings = []contract.ValidatorResult{}, []contract.ReviewTask{}, []contract.Finding{}
	rows, err := s.db.R.QueryContext(ctx, `SELECT v.id, v.name, v.type, r.status, r.exit_code, r.started_at, r.finished_at, COALESCE(v.items_json,''), r.confirmed_json
		FROM validators v JOIN validation_runs r ON r.validator_id = v.id WHERE r.phase_id = ? AND r.rowid = (SELECT max(rowid) FROM validation_runs x WHERE x.validator_id = v.id) ORDER BY v.position`, phaseID)
	if err != nil {
		return r, wrap(err, "list validator runs")
	}
	failed := 0
	for rows.Next() {
		var x contract.ValidatorResult
		var st, items, conf string
		var code sql.NullInt64
		var started, finished sql.NullString
		rows.Scan(&x.ID, &x.Name, &x.Kind, &st, &code, &started, &finished, &items, &conf)
		x.State = map[string]string{"pass": "passed", "confirmed": "passed", "pending": "waiting", "unconfirmed": "waiting"}[st]
		if x.State == "" {
			x.State = "failed"
			failed++
		} else if x.State == "waiting" {
			failed++
		}
		if code.Valid && x.Kind == "command" {
			c := int(code.Int64)
			x.ExitCode = &c
		}
		if started.Valid && finished.Valid {
			d := int(store.ParseTime(finished.String).Sub(store.ParseTime(started.String)).Seconds())
			x.DurationS = &d
		}
		if x.Kind == "checklist" {
			var list []string
			var done []int
			json.Unmarshal([]byte(items), &list)
			json.Unmarshal([]byte(conf), &done)
			for i, it := range list {
				c := false
				for _, d := range done {
					c = c || d == i
				}
				x.Items = append(x.Items, contract.ValidatorChecklistItem{Text: it, Confirmed: c})
			}
		}
		r.Validators = append(r.Validators, x)
	}
	rows.Close()
	tr, err := s.db.R.QueryContext(ctx, `SELECT t.id, t.key, t.title, t.state, COALESCE(t.blocked_reason,''), (SELECT count(*) FROM executions e WHERE e.task_id = t.id)
		FROM tasks t WHERE t.phase_id = ? AND t.parent_task_id IS NULL ORDER BY t.position`, phaseID)
	if err != nil {
		return r, wrap(err, "list tasks")
	}
	done := 0
	for tr.Next() {
		var x contract.ReviewTask
		var id, st string
		tr.Scan(&id, &x.Code, &x.Title, &st, &x.StateDetail, &x.Attempts)
		x.ID, x.Code, x.State = id, strings.ToUpper(x.Code), snake(st)
		if st == "Done" {
			done++
		}
		r.Tasks = append(r.Tasks, x)
	}
	tr.Close()
	r.Diff, r.Findings = s.phaseDiff(ctx, phaseID, r.Tasks)
	r.Summary = fmt.Sprintf("%d of %d tasks done. %d of %d validators passed. %d files changed across %d executions.", done, len(r.Tasks), len(r.Validators)-failed, len(r.Validators), r.Diff.Files, r.Diff.Executions)
	// One rule for the screen and for Decide: reviewNeedsOverride.
	needs, err := s.reviewNeedsOverride(ctx, s.db.R, phaseID)
	if err != nil {
		return r, err
	}
	r.RequiresOverride = needs
	if needs && failed == 0 {
		r.Recommendation = "A task you reopened was cancelled instead of redone. Approving needs a written override reason."
	} else if needs {
		r.Recommendation = "A validator failed or is not confirmed. Approving needs a written override reason."
	}
	var apr sql.NullString
	s.db.R.QueryRowContext(ctx, `SELECT id FROM approvals WHERE subject_id = ? AND category = 'phase_completion' AND state = 'pending'`, phaseID).Scan(&apr)
	r.ApprovalID = apr.String
	return r, nil
}

// ValidatorResult returns one validator's latest result in a phase.
func (s *Service) ValidatorResult(ctx context.Context, phaseID, validatorID string) (contract.ValidatorResult, error) {
	rv, err := s.PhaseReview(ctx, phaseID)
	if err != nil {
		return contract.ValidatorResult{}, err
	}
	for _, v := range rv.Validators {
		if v.ID == validatorID {
			return v, nil
		}
	}
	return contract.ValidatorResult{}, errNotFound("validator", "")
}

// Approvals lists approvals, newest first.
func (s *Service) Approvals(ctx context.Context, state, projectID string, limit int) (contract.ApprovalList, error) {
	q := `SELECT a.id, a.category, a.state, a.project_id, p.name, COALESCE(` + approvalSubjectSQL + `,''), a.requested_by, a.requested_at, a.decided_at
		FROM approvals a JOIN projects p ON p.id = a.project_id WHERE (? = '' OR a.state = ?) AND (? = '' OR a.project_id = ?) ORDER BY a.requested_at DESC LIMIT ?`
	rows, err := s.db.R.QueryContext(ctx, q, state, state, projectID, projectID, limit)
	if err != nil {
		return contract.ApprovalList{}, wrap(err, "list approvals")
	}
	defer rows.Close()
	out := contract.ApprovalList{Items: []contract.ApprovalSummary{}}
	for rows.Next() {
		var a contract.ApprovalSummary
		var at string
		var dec sql.NullString
		rows.Scan(&a.ID, &a.Category, &a.State, &a.ProjectID, &a.ProjectName, &a.Subject, &a.RequestedBy, &at, &dec)
		a.CategoryLabel, a.RequestedAt, a.DecidedAt = CategoryLabel[a.Category], store.ParseTime(at), tptr(dec)
		a.RequestedBy = map[string]string{"system": "Waves", "agent": "Agent", "operator": "You"}[a.RequestedBy]
		out.Items = append(out.Items, a)
	}
	return out, rows.Err()
}

// ApprovalDetail is everything the human needs to decide, plus the subject hash.
func (s *Service) ApprovalDetail(ctx context.Context, id string) (contract.ApprovalDetail, error) {
	var d contract.ApprovalDetail
	var stype, sid, impact, at string
	var dec sql.NullString
	err := s.db.R.QueryRowContext(ctx, `SELECT a.id, a.category, a.state, a.project_id, p.name, COALESCE(`+approvalSubjectSQL+`,''), a.requested_by, a.requested_at, a.decided_at, a.subject_type, a.subject_id, a.impact_json
		FROM approvals a JOIN projects p ON p.id = a.project_id WHERE a.id = ?`, id).Scan(&d.ID, &d.Category, &d.State, &d.ProjectID, &d.ProjectName, &d.Subject, &d.RequestedBy, &at, &dec, &stype, &sid, &impact)
	if err != nil {
		return d, errNotFound("approval", "Open the approval inbox.")
	}
	d.CategoryLabel, d.RequestedAt, d.DecidedAt = CategoryLabel[d.Category], store.ParseTime(at), tptr(dec)
	d.RequestedBy = map[string]string{"system": "Waves", "agent": "Agent", "operator": "You"}[d.RequestedBy]
	d.Facts, d.Commands, d.AllowedDecisions = []contract.ApprovalFact{}, []string{}, []string{}
	if d.State == "pending" {
		d.AllowedDecisions = []string{"approve", "reject"}
	}
	d.StepUp = "window_ok"
	if everyTime[d.Category] {
		d.StepUp = "every_time"
	}
	d.RejectNeedsStepUp = humanReject[d.Category]
	if d.SubjectHash, err = s.subjectHash(ctx, s.db.R, d.Category, stype, sid); err != nil {
		return d, err
	}
	fact := func(l, v string, mono bool) {
		d.Facts = append(d.Facts, contract.ApprovalFact{Label: l, Value: v, Mono: mono})
	}
	switch d.Category {
	case "workflow":
		plan, _, err := s.planOf(ctx, s.db.R, sid)
		if err != nil {
			return d, err
		}
		var hash string
		s.db.R.QueryRowContext(ctx, `SELECT content_hash FROM workflow_versions WHERE id = ?`, sid).Scan(&hash)
		d.Action = "Approve " + d.Subject
		fact("Content hash", hash, true)
		fact("Phases", fmt.Sprint(len(plan.Phases)), false)
		d.Commands = versionCommands(plan)
		d.Impact = "Approving locks exactly the commands listed. Waves runs them with /bin/sh in the project folder when a phase reaches validation. Each phase still needs its own start approval."
	case "phase_start", "phase_completion":
		d.PhaseID = sid
		vals, _ := s.validatorsOf(ctx, sid)
		for _, v := range vals {
			if v.Kind == "command" {
				d.Commands = append(d.Commands, v.Command)
			}
		}
		var n int
		s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE phase_id = ? AND parent_task_id IS NULL`, sid).Scan(&n)
		fact("Tasks", fmt.Sprint(n), false)
		if d.Category == "phase_start" {
			d.Action = "Start phase " + d.Subject
			d.Impact = "Agents can claim this phase's tasks once you approve. Nothing outside this phase opens."
		} else {
			d.Action = "Complete phase " + d.Subject
			d.Impact = "Completing closes the phase and asks for the next phase's start approval. Review the validator results first."
			if rv, err := s.PhaseReview(ctx, sid); err == nil {
				fact("Summary", rv.Summary, false)
			}
		}
	default:
		var m map[string]any
		json.Unmarshal([]byte(impact), &m)
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if d.Category == "result" && k == "impact" {
				continue
			}
			label := strings.ToUpper(k[:1]) + strings.ReplaceAll(k[1:], "_", " ")
			if d.Category == "risky_action" {
				label = "Agent's stated " + strings.ReplaceAll(k, "_", " ")
			}
			fact(label, fmt.Sprint(m[k]), false)
		}
		d.Action = map[string]string{"result": "Accept the result of " + d.Subject, "risky_action": "Allow the action requested in " + d.Subject}[d.Category]
		// The impact line is Waves' own text; what the agent wrote stays in the labelled facts above.
		d.Impact = "Accepting marks the task done."
		if d.Category == "risky_action" {
			d.Impact = "Allowing lets the agent go ahead with the action it described. Waves does not check what the action does; the diff is recorded."
		}
	}
	return d, nil
}
