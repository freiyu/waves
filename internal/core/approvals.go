package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"waves/internal/store"
)

// everyTime are the categories that need a fresh single-use step-up.
var everyTime = map[string]bool{"phase_start": true, "phase_completion": true}

// humanReject are the categories only a human (with an elevation) may reject.
var humanReject = map[string]bool{"workflow": true, "phase_start": true, "phase_completion": true}

// requestApproval opens a pending approval.
func (s *Service) requestApproval(ctx context.Context, t *Tx, category, subjectType, subjectID, project, by, subjectHash string, impact map[string]any) (string, error) {
	id := NewID("apr")
	imp, _ := json.Marshal(impact)
	if _, err := t.ExecContext(ctx, `INSERT INTO approvals(id, category, subject_type, subject_id, project_id, requested_by, impact_json, requested_at, subject_hash) VALUES (?,?,?,?,?,?,?,?,?)`,
		id, category, subjectType, subjectID, project, by, string(imp), s.ts(), subjectHash); err != nil {
		return "", wrap(err, "create approval")
	}
	t.publish("approval.changed", map[string]string{"approval_id": id, "project_id": project, "category": category, "state": "pending"})
	t.publish("attention.changed", func() any { return map[string]int{"count": s.AttentionCount(ctx)} })
	return id, t.audit(ctx, store.AuditRecord{Event: "approval.requested", ActorScope: map[string]string{"agent": "agent", "operator": "operator"}[by] + map[bool]string{true: "system"}[by == "system"], ProjectID: project, Subject: id,
		Detail: map[string]any{"category": category, "subject": subjectType + ":" + subjectID}})
}

// subjectHash is the hash of what the human sees when deciding. It changes
// when the subject changes, so a stale screen cannot approve.
func (s *Service) subjectHash(ctx context.Context, q store.Execer, category, subjectType, subjectID string) (string, error) {
	h := sha256.New()
	h.Write([]byte(category + "\x00" + subjectType + "\x00" + subjectID + "\x00"))
	switch subjectType {
	case "workflow_version":
		var ch, state string
		if err := q.QueryRowContext(ctx, `SELECT content_hash, state FROM workflow_versions WHERE id = ?`, subjectID).Scan(&ch, &state); err != nil {
			return "", wrap(err, "read version")
		}
		h.Write([]byte(ch))
		if category == "workflow" {
			// What approving it cancels is part of what the human decides on.
			items, err := s.openWork(ctx, q, subjectID)
			if err != nil {
				return "", err
			}
			for _, w := range items {
				h.Write([]byte(w.task + w.state + w.exe))
			}
		}
	case "phase":
		var ch, state string
		if err := q.QueryRowContext(ctx, `SELECT v.content_hash, p.state FROM phases p JOIN workflow_versions v ON v.id = p.workflow_version_id WHERE p.id = ?`, subjectID).Scan(&ch, &state); err != nil {
			return "", wrap(err, "read phase")
		}
		h.Write([]byte(ch + state))
		if category == "phase_completion" {
			rows, err := q.QueryContext(ctx, `SELECT r.id, r.status, COALESCE(r.exit_code,-1), r.confirmed_json FROM validation_runs r WHERE r.phase_id = ? ORDER BY r.rowid`, subjectID)
			if err != nil {
				return "", wrap(err, "read validation runs")
			}
			for rows.Next() {
				var id, st, conf string
				var code int
				rows.Scan(&id, &st, &code, &conf)
				h.Write([]byte(id + st + itoa(int64(code)) + conf))
			}
			rows.Close()
			rows, err = q.QueryContext(ctx, `SELECT id, state FROM tasks WHERE phase_id = ? ORDER BY id`, subjectID)
			if err != nil {
				return "", wrap(err, "read tasks")
			}
			for rows.Next() {
				var id, st string
				rows.Scan(&id, &st)
				h.Write([]byte(id + st))
			}
			rows.Close()
		}
	case "execution", "task":
		var res, diff, state sql.NullString
		if subjectType == "execution" {
			q.QueryRowContext(ctx, `SELECT result_json, diff_path, state FROM executions WHERE id = ?`, subjectID).Scan(&res, &diff, &state)
		} else {
			q.QueryRowContext(ctx, `SELECT e.result_json, e.diff_path, t.state FROM tasks t LEFT JOIN executions e ON e.task_id = t.id WHERE t.id = ? ORDER BY e.created_at DESC, e.rowid DESC LIMIT 1`, subjectID).Scan(&res, &diff, &state)
		}
		h.Write([]byte(res.String + "\x00" + state.String))
		if diff.Valid {
			if dh, err := fileHash(diff.String); err == nil {
				h.Write([]byte(dh))
			}
		}
		var imp string
		q.QueryRowContext(ctx, `SELECT impact_json FROM approvals WHERE subject_id = ? AND category = ? AND state = 'pending'`, subjectID, category).Scan(&imp)
		h.Write([]byte(imp))
	}
	return "sh_" + hex.EncodeToString(h.Sum(nil))[:32], nil
}

// Decision is a human decision on one approval.
type Decision struct {
	ApprovalID  string
	Decision    string // approve | reject
	SubjectHash string
	Feedback    string
	Override    string
	Elevation   string
}

// Decide applies a decision. Approving always needs `human` scope: an
// elevation from a passphrase step-up, single-use and bound to the approval
// for phase gates. The audit chain is verified first; a stale subject fails.
func (s *Service) Decide(ctx context.Context, p Principal, d Decision) error {
	if !p.IsOperator() {
		s.auditBypass(ctx, p, "approval "+d.ApprovalID)
		return errWrongScope
	}
	if d.Decision != "approve" && d.Decision != "reject" {
		return errValidation("The decision must be approve or reject.", "")
	}
	if err := s.processGuard(ctx, p); err != nil {
		return err
	}
	return s.tx(ctx, func(t *Tx) error {
		// Verified inside the write transaction, so no record can slip in between.
		if st := s.auditStatusOn(ctx, t.Tx); st.State == "broken" {
			return fail(http.StatusConflict, "audit_chain_broken", "Approvals are blocked: the audit chain does not verify.", "Acknowledge the break in the dashboard (Activity page).").with("record", int(st.BrokenAt))
		}
		var cat, stype, sid, project, state string
		err := t.QueryRowContext(ctx, `SELECT category, subject_type, subject_id, project_id, state FROM approvals WHERE id = ?`, d.ApprovalID).Scan(&cat, &stype, &sid, &project, &state)
		if err != nil {
			return errNotFound("approval", "Open the approval inbox.")
		}
		if state != "pending" {
			return errInvalidState("This approval is already "+state+".", "Nothing more to do.")
		}
		cur, err := s.subjectHash(ctx, t, cat, stype, sid)
		if err != nil {
			return err
		}
		if d.SubjectHash != cur {
			return fail(http.StatusConflict, "stale_subject", "What you are approving changed after you opened it.", "Reload the approval and review it again.")
		}
		scope, method, elevID := "operator", "", ""
		if d.Decision == "approve" || humanReject[cat] {
			id, err := s.useElevation(ctx, t, p, d.Elevation, d.ApprovalID, !(d.Decision == "approve" && everyTime[cat]))
			if err != nil {
				return err
			}
			scope, method, elevID = "human", "passphrase", id
		}
		if d.Decision == "reject" && strings.TrimSpace(d.Feedback) == "" {
			return errValidation("Rejecting needs feedback.", "Say what must change.")
		}
		if d.Decision == "approve" && cat == "phase_completion" {
			needs, err := s.reviewNeedsOverride(ctx, t, sid)
			if err != nil {
				return wrap(err, "check whether the review needs an override")
			}
			if needs && strings.TrimSpace(d.Override) == "" {
				return errValidation("A failed or unconfirmed validator needs a written reason to approve.", "Write the override reason.")
			}
		}
		newState := map[string]string{"approve": "approved", "reject": "rejected"}[d.Decision]
		if _, err := t.ExecContext(ctx, `UPDATE approvals SET state = ?, decided_at = ?, decided_by_scope = ?, stepup_method = ?, elevation_id = ?, tty = ?, client = ?, source_addr = ?, feedback = ?, override_reason = ? WHERE id = ? AND state = 'pending'`,
			newState, s.ts(), scope, store.NullStr(method), store.NullStr(elevID), b2i(p.TTY), p.Client, p.Source, store.NullStr(d.Feedback), store.NullStr(d.Override), d.ApprovalID); err != nil {
			return wrap(err, "record decision")
		}
		actor := Actor{Scope: scope, Ref: p.ActorRef()}
		if err := s.applyDecision(ctx, t, cat, stype, sid, project, d.Decision == "approve", d.ApprovalID, actor); err != nil {
			return err
		}
		t.publish("approval.changed", map[string]string{"approval_id": d.ApprovalID, "project_id": project, "category": cat, "state": newState})
		t.publish("attention.changed", func() any { return map[string]int{"count": s.AttentionCount(ctx)} })
		return t.audit(ctx, store.AuditRecord{Event: "approval." + newState, ActorScope: scope, ActorRef: p.ActorRef(), ProjectID: project, Subject: d.ApprovalID,
			Detail: map[string]any{"category": cat, "subject": stype + ":" + sid, "credential": p.Kind, "stepup_method": method, "tty": p.TTY, "client": p.Client, "source": p.Source, "override": d.Override != ""}})
	})
}

// applyDecision moves the subject of a decided approval.
func (s *Service) applyDecision(ctx context.Context, t *Tx, cat, stype, sid, project string, approve bool, aprID string, a Actor) error {
	move := func(entity, id, to string, actor Actor, apr string) error {
		_, err := s.transition(ctx, t, entity, id, to, actor, "approval "+aprID, apr)
		return err
	}
	switch cat {
	case "workflow":
		if !approve {
			return move("workflow_version", sid, "rejected", a, "")
		}
		var wf string
		t.QueryRowContext(ctx, `SELECT workflow_id FROM workflow_versions WHERE id = ?`, sid).Scan(&wf)
		var old sql.NullString
		t.QueryRowContext(ctx, `SELECT id FROM workflow_versions WHERE workflow_id = ? AND state = 'approved'`, wf).Scan(&old)
		if old.Valid {
			// The new requirements replace the old ones at once: what the old version still has open ends here, and the agent
			// chooses its next task from the new version. Nothing is rerun.
			if err := s.supersedeRunning(ctx, t, old.String, project, a, aprID); err != nil {
				return err
			}
			if err := move("workflow_version", old.String, "superseded", sysActor, ""); err != nil {
				return err
			}
		}
		if err := move("workflow_version", sid, "approved", a, aprID); err != nil {
			return err
		}
		var first string
		if err := t.QueryRowContext(ctx, `SELECT id FROM phases WHERE workflow_version_id = ? ORDER BY position LIMIT 1`, sid).Scan(&first); err != nil {
			return wrap(err, "first phase")
		}
		return s.openPhaseGate(ctx, t, first, project)
	case "phase_start":
		if !approve {
			return move("phase", sid, "Revision Required", a, "")
		}
		if err := move("phase", sid, "Approved", a, aprID); err != nil {
			return err
		}
		if err := move("phase", sid, "Ready for Execution", sysActor, ""); err != nil {
			return err
		}
		return s.readyTasks(ctx, t, sid)
	case "phase_completion":
		if !approve {
			return move("phase", sid, "Revision Required", a, "")
		}
		if err := move("phase", sid, "Completed", a, aprID); err != nil {
			return err
		}
		var next string
		err := t.QueryRowContext(ctx, `SELECT q.id FROM phases p JOIN phases q ON q.workflow_version_id = p.workflow_version_id AND q.position = p.position + 1 WHERE p.id = ?`, sid).Scan(&next)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return wrap(err, "next phase")
		}
		return s.openPhaseGate(ctx, t, next, project)
	case "result":
		task := sid
		if stype == "execution" {
			t.QueryRowContext(ctx, `SELECT task_id FROM executions WHERE id = ?`, sid).Scan(&task)
		}
		if !approve {
			// Withholding a result is allowed for the operator; the task gets a new attempt.
			return move("task", task, "Ready", Actor{Scope: map[bool]string{true: "human", false: "operator"}[a.Scope == "human"], Ref: a.Ref}, "")
		}
		if err := move("task", task, "Done", a, aprID); err != nil {
			return err
		}
		return s.afterTaskTerminal(ctx, t, task)
	case "risky_action":
		if !approve {
			return nil // the execution stays waiting; the agent sees the rejection on its next heartbeat and releases the task
		}
		return move("execution", sid, "Running", a, aprID)
	}
	return nil
}

// supersedeRunning ends the open work of a workflow version that a newer one replaces, in the transaction of the approval:
// every execution not yet finished is cancelled (its lease ends, its workspace lock and slot are freed, the agent's next
// call is refused), every task that is not Done or Cancelled is Cancelled, and the approvals still waiting on them can no
// longer be decided. Phases keep their state: every view reads the phases of the approved version only.
func (s *Service) supersedeRunning(ctx context.Context, t *Tx, version, project string, a Actor, aprID string) error {
	ids := func(q string) ([]string, error) {
		rows, err := t.QueryContext(ctx, q, version)
		if err != nil {
			return nil, wrap(err, "list open work of the old version")
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, wrap(err, "read open work of the old version")
			}
			out = append(out, id)
		}
		return out, rows.Err()
	}
	// Taken before the executions are cancelled: ending an execution already withdraws its risky-action approval, and that one is recorded too.
	pend, err := t.QueryContext(ctx, `SELECT id, category FROM approvals WHERE state = 'pending' AND (
		(subject_type = 'phase' AND subject_id IN (SELECT id FROM phases WHERE workflow_version_id = ?1))
		OR (subject_type = 'task' AND subject_id IN (SELECT t.id FROM tasks t JOIN phases p ON p.id = t.phase_id WHERE p.workflow_version_id = ?1))
		OR (subject_type = 'execution' AND subject_id IN (SELECT e.id FROM executions e JOIN tasks t ON t.id = e.task_id JOIN phases p ON p.id = t.phase_id WHERE p.workflow_version_id = ?1)))`, version)
	if err != nil {
		return wrap(err, "list approvals of the old version")
	}
	type pa struct{ id, cat string }
	var withdrawn []pa
	for pend.Next() {
		var x pa
		if err := pend.Scan(&x.id, &x.cat); err != nil {
			pend.Close()
			return wrap(err, "read approval of the old version")
		}
		withdrawn = append(withdrawn, x)
	}
	err = pend.Err()
	pend.Close()
	if err != nil {
		return wrap(err, "list approvals of the old version")
	}
	exes, err := ids(`SELECT e.id FROM executions e JOIN tasks t ON t.id = e.task_id JOIN phases p ON p.id = t.phase_id
		WHERE p.workflow_version_id = ? AND e.state NOT IN ('Completed','Failed','Cancelled')`)
	if err != nil {
		return err
	}
	tasks, err := ids(`SELECT t.id FROM tasks t JOIN phases p ON p.id = t.phase_id WHERE p.workflow_version_id = ? AND t.state NOT IN ('Done','Cancelled')`)
	if err != nil {
		return err
	}
	for _, e := range exes {
		if _, err := t.ExecContext(ctx, `UPDATE executions SET cancel_requested = 1 WHERE id = ?`, e); err != nil {
			return wrap(err, "request cancel")
		}
		if _, err := s.transition(ctx, t, "execution", e, "Cancelled", a, "superseded by a new workflow version", ""); err != nil {
			return err
		}
	}
	vph, err := ids(`SELECT p.id FROM phases p WHERE p.workflow_version_id = ? AND p.state = 'Validation'`)
	if err != nil {
		return err
	}
	for _, ph := range vph { // a queued or restarted validation would look at a replaced phase
		if _, err := s.transition(ctx, t, "phase", ph, "In Progress", a, "superseded by a new workflow version", ""); err != nil {
			return err
		}
	}
	for _, k := range tasks {
		if _, err := s.transition(ctx, t, "task", k, "Cancelled", a, "superseded by a new workflow version", ""); err != nil {
			return err
		}
	}
	for _, x := range withdrawn {
		if _, err := t.ExecContext(ctx, `UPDATE approvals SET state = 'cancelled', decided_at = ? WHERE id = ? AND state = 'pending'`, s.ts(), x.id); err != nil {
			return wrap(err, "withdraw approval of the old version")
		}
		t.publish("approval.changed", map[string]string{"approval_id": x.id, "project_id": project, "category": x.cat, "state": "cancelled"})
		if err := t.audit(ctx, store.AuditRecord{Event: "approval.cancelled", ActorScope: a.Scope, ActorRef: a.Ref, ProjectID: project, Subject: x.id,
			Detail: map[string]any{"category": x.cat, "reason": "superseded by a new workflow version"}}); err != nil {
			return err
		}
	}
	if len(withdrawn) > 0 {
		t.publish("attention.changed", func() any { return map[string]int{"count": s.AttentionCount(ctx)} })
	}
	if len(exes)+len(tasks)+len(withdrawn) == 0 {
		return nil
	}
	return t.audit(ctx, store.AuditRecord{Event: "workflow.superseded_running", ActorScope: a.Scope, ActorRef: a.Ref, ProjectID: project, Subject: version,
		Detail: map[string]any{"executions_cancelled": len(exes), "tasks_cancelled": len(tasks), "approvals_withdrawn": len(withdrawn), "approval_id": aprID}})
}

// openPhaseGate moves a Planned phase to Awaiting Approval and requests its
// Phase Start approval.
func (s *Service) openPhaseGate(ctx context.Context, t *Tx, phaseID, project string) error {
	if _, err := s.transition(ctx, t, "phase", phaseID, "Awaiting Approval", sysActor, "previous gate passed", ""); err != nil {
		return err
	}
	h, err := s.subjectHash(ctx, t, "phase_start", "phase", phaseID)
	if err != nil {
		return err
	}
	_, err = s.requestApproval(ctx, t, "phase_start", "phase", phaseID, project, "system", h, map[string]any{"phase": phaseID})
	return err
}

// readyTasks moves Backlog tasks whose dependencies are done to Ready.
func (s *Service) readyTasks(ctx context.Context, t *Tx, phaseID string) error {
	rows, err := t.QueryContext(ctx, `SELECT id FROM tasks t WHERE phase_id = ? AND parent_task_id IS NULL AND state = 'Backlog'
		AND NOT EXISTS (SELECT 1 FROM task_dependencies d JOIN tasks x ON x.id = d.depends_on_task_id WHERE d.task_id = t.id AND x.state <> 'Done')`, phaseID)
	if err != nil {
		return wrap(err, "find ready tasks")
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if _, err := s.transition(ctx, t, "task", id, "Ready", sysActor, "dependencies done", ""); err != nil {
			return err
		}
	}
	return nil
}

// afterTaskTerminal readies dependents and starts validation when every task
// of the phase is finished.
func (s *Service) afterTaskTerminal(ctx context.Context, t *Tx, taskID string) error {
	var phase, pstate string
	if err := t.QueryRowContext(ctx, `SELECT t.phase_id, p.state FROM tasks t JOIN phases p ON p.id = t.phase_id WHERE t.id = ?`, taskID).Scan(&phase, &pstate); err != nil {
		return wrap(err, "read task phase")
	}
	// Dependents in later phases become Ready when their phase opens.
	if err := s.readyTasks(ctx, t, phase); err != nil {
		return err
	}
	if pstate != "In Progress" {
		return nil
	}
	var open, leases int
	t.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE phase_id = ? AND parent_task_id IS NULL AND state NOT IN ('Done','Cancelled')`, phase).Scan(&open)
	t.QueryRowContext(ctx, `SELECT count(*) FROM leases l JOIN tasks x ON x.id = l.task_id WHERE x.phase_id = ? AND l.revoked_at IS NULL`, phase).Scan(&leases)
	if open > 0 || leases > 0 {
		return nil
	}
	if _, err := s.transition(ctx, t, "phase", phase, "Validation", sysActor, "all tasks finished", ""); err != nil {
		return err
	}
	return s.startValidation(ctx, t, phase)
}
