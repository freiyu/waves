package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"waves/internal/store"
)

// Actor is who causes a transition. Scope is system, agent, operator or human.
type Actor struct {
	Scope string
	Ref   string
}

var (
	sysActor = Actor{Scope: "system", Ref: "core"}
)

// actorOf maps a principal to its transition actor. A human actor is only
// produced by the approval service after a step-up; here operators stay operators.
func actorOf(p Principal) Actor {
	switch p.Kind {
	case KindSession, KindOperator:
		return Actor{Scope: "operator", Ref: p.ActorRef()}
	case KindLease, KindMCP:
		return Actor{Scope: "agent", Ref: p.ActorRef()}
	}
	return Actor{Scope: "none", Ref: p.ActorRef()}
}

type entityInfo struct{ table string }

var entities = map[string]entityInfo{
	"workflow_version": {"workflow_versions"},
	"phase":            {"phases"},
	"task":             {"tasks"},
	"execution":        {"executions"},
}

// snake turns a stored state ("Awaiting Phase Review") into its API form.
func snake(s string) string { return strings.ReplaceAll(strings.ToLower(s), " ", "_") }

// eventName is the audit event for a move.
func eventName(entity, from, to, actor string) string {
	k := entity + ":" + from + ">" + to
	if ev, ok := map[string]string{
		"phase:Awaiting Approval>Approved":              "phase.approved",
		"phase:Awaiting Approval>Revision Required":     "phase.rejected",
		"phase:Approved>Ready for Execution":            "phase.ready",
		"phase:Ready for Execution>In Progress":         "phase.started",
		"phase:Blocked>In Progress":                     "phase.unblocked",
		"phase:In Progress>Validation":                  "phase.validation_started",
		"phase:Validation>Awaiting Phase Review":        "phase.review_requested",
		"phase:Validation>In Progress":                  "phase.validation_failed",
		"phase:Awaiting Phase Review>Completed":         "phase.completed",
		"phase:Awaiting Phase Review>Revision Required": "phase.rejected",
		"phase:Revision Required>In Progress":           "phase.revision_started",
		"task:Ready>In Progress":                        "task.started",
		"task:Waiting Approval>In Progress":             "task.approval_decided",
		"task:Blocked>In Progress":                      "task.resume",
		"task:In Review>Ready":                          "task.rejected",
		"task:Blocked>Ready":                            "task.retry",
		"task:Done>Ready":                               "task.reopened",
		"execution:Starting>Running":                    "execution.running",
		"execution:Waiting Approval>Running":            "execution.approval_decided",
		"execution:Paused>Running":                      "execution.unpaused",
		"execution:Interrupted>Completed":               "execution.completed_partial",
		"execution:Disconnected>Completed":              "execution.completed_partial",
	}[k]; ok {
		return ev
	}
	if entity == "execution" && to == "Running" {
		if from == "Disconnected" && actor == "agent" {
			return "execution.reconnected"
		}
		return "execution.resumed"
	}
	if entity == "workflow_version" {
		return "workflow." + map[string]string{"awaiting_approval": "submitted", "approved": "approved", "rejected": "rejected", "draft": "returned_to_draft", "superseded": "superseded"}[to]
	}
	return entity + "." + snake(to)
}

// transition is the single path for every state change. Inside t it checks
// the move against allowed_transitions and the code guards, writes the
// transition row (the database trigger checks it again), updates the state
// column, revokes leases the move ends, writes the audit record and queues
// the event for after the commit.
func (s *Service) transition(ctx context.Context, t *Tx, entity, id, to string, a Actor, trigger, approvalID string) (string, error) {
	info, ok := entities[entity]
	if !ok {
		return "", fmt.Errorf("unknown entity %q", entity)
	}
	var from string
	err := t.QueryRowContext(ctx, `SELECT state FROM `+info.table+` WHERE id = ?`, id).Scan(&from)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotFound(strings.ReplaceAll(entity, "_", " "), "")
	}
	if err != nil {
		return "", wrap(err, "read %s %s", entity, id)
	}
	var cat sql.NullString
	err = t.QueryRowContext(ctx, `SELECT approval_category FROM allowed_transitions WHERE entity_type = ? AND from_state = ? AND to_state = ? AND actor_scope = ?`,
		entity, from, to, a.Scope).Scan(&cat)
	if errors.Is(err, sql.ErrNoRows) {
		return "", &Error{Status: http.StatusConflict, Code: "illegal_transition",
			Message: fmt.Sprintf("A %s cannot move from %s to %s by %s.", strings.ReplaceAll(entity, "_", " "), from, to, a.Scope), NextStep: "Nothing was changed."}
	}
	if err != nil {
		return "", wrap(err, "check transition")
	}
	if cat.Valid {
		if err := s.checkGateApproval(ctx, t, entity, id, cat.String, approvalID, a); err != nil {
			return "", err
		}
	}
	if err := s.guard(ctx, t, entity, id, from, to); err != nil {
		return "", err
	}
	now := s.ts()
	if _, err := t.ExecContext(ctx, `INSERT INTO state_transitions(entity_type, entity_id, from_state, to_state, trigger, actor_scope, actor_ref, approval_id, ts) VALUES (?,?,?,?,?,?,?,?,?)`,
		entity, id, from, to, trigger, a.Scope, store.NullStr(a.Ref), store.NullStr(approvalID), now); err != nil {
		return "", wrap(err, "record transition")
	}
	q, args := `UPDATE `+info.table+` SET state = ?`, []any{to}
	switch entity {
	case "phase", "task":
		q, args = q+`, updated_at = ?`, append(args, now)
	case "execution":
		if to == "Running" {
			q, args = q+`, started_at = COALESCE(started_at, ?)`, append(args, now)
		}
		if to == "Completed" || to == "Failed" || to == "Cancelled" {
			q, args = q+`, ended_at = ?`, append(args, now)
		}
	}
	if _, err := t.ExecContext(ctx, q+` WHERE id = ?`, append(args, id)...); err != nil {
		return "", wrap(err, "update %s state", entity)
	}
	ctxInfo, err := s.entityContext(ctx, t, entity, id)
	if err != nil {
		return "", err
	}
	if err := s.afterMove(ctx, t, entity, id, from, to, ctxInfo); err != nil {
		return "", err
	}
	detail := map[string]any{"from": from, "to": to, "trigger": trigger}
	if approvalID != "" {
		detail["approval_id"] = approvalID
	}
	if err := t.audit(ctx, store.AuditRecord{TS: now, Event: eventName(entity, from, to, a.Scope), ActorScope: a.Scope, ActorRef: a.Ref, ProjectID: ctxInfo.project, Subject: id, Detail: detail}); err != nil {
		return "", err
	}
	switch entity {
	case "phase":
		t.publish("phase.changed", map[string]string{"phase_id": id, "project_id": ctxInfo.project, "from": snake(from), "to": snake(to)})
	case "task":
		t.publish("task.changed", map[string]string{"task_id": id, "phase_id": ctxInfo.phase, "from": snake(from), "to": snake(to)})
	case "execution":
		t.publish("execution.changed", map[string]string{"execution_id": id, "task_id": ctxInfo.task, "project_id": ctxInfo.project, "from": snake(from), "to": snake(to)})
		t.publish("attention.changed", func() any { return map[string]int{"count": s.AttentionCount(ctx)} })
	case "workflow_version":
		t.publish("workflow.changed", map[string]string{"project_id": ctxInfo.project, "version_id": id, "state": to})
	}
	return from, nil
}

type entityCtx struct{ project, phase, task string }

func (s *Service) entityContext(ctx context.Context, t *Tx, entity, id string) (entityCtx, error) {
	var c entityCtx
	var err error
	switch entity {
	case "phase":
		c.phase = id
		err = t.QueryRowContext(ctx, `SELECT w.project_id FROM phases p JOIN workflow_versions v ON v.id = p.workflow_version_id JOIN workflows w ON w.id = v.workflow_id WHERE p.id = ?`, id).Scan(&c.project)
	case "task":
		c.task = id
		err = t.QueryRowContext(ctx, `SELECT w.project_id, t.phase_id FROM tasks t JOIN phases p ON p.id = t.phase_id JOIN workflow_versions v ON v.id = p.workflow_version_id JOIN workflows w ON w.id = v.workflow_id WHERE t.id = ?`, id).Scan(&c.project, &c.phase)
	case "execution":
		err = t.QueryRowContext(ctx, `SELECT e.project_id, e.task_id, t.phase_id FROM executions e JOIN tasks t ON t.id = e.task_id WHERE e.id = ?`, id).Scan(&c.project, &c.task, &c.phase)
	case "workflow_version":
		err = t.QueryRowContext(ctx, `SELECT w.project_id FROM workflow_versions v JOIN workflows w ON w.id = v.workflow_id WHERE v.id = ?`, id).Scan(&c.project)
	}
	return c, wrap(err, "read %s context", entity)
}

// checkGateApproval verifies, in code, what the database trigger also checks:
// a decided approval of the right category, by human scope with a passphrase
// step-up and a recorded elevation, for this exact subject.
func (s *Service) checkGateApproval(ctx context.Context, t *Tx, entity, id, category, approvalID string, a Actor) error {
	refuse := &Error{Status: http.StatusForbidden, Code: "approval_required", Message: "This move needs a human approval with your passphrase.", NextStep: "Decide it in the approval inbox."}
	if a.Scope != "human" || approvalID == "" {
		return refuse
	}
	var state, cat, subj, scope, method string
	var elev sql.NullString
	err := t.QueryRowContext(ctx, `SELECT state, category, subject_id, COALESCE(decided_by_scope,''), COALESCE(stepup_method,''), elevation_id FROM approvals WHERE id = ?`, approvalID).
		Scan(&state, &cat, &subj, &scope, &method, &elev)
	if err != nil || state != "approved" || cat != category || subj != id || scope != "human" || method != "passphrase" || !elev.Valid {
		return refuse
	}
	// The approval must be fresh: a decided approval moves its subject once.
	var used int
	if err := t.QueryRowContext(ctx, `SELECT count(*) FROM state_transitions WHERE approval_id = ?`, approvalID).Scan(&used); err != nil || used > 0 {
		return refuse
	}
	return nil
}

// guard holds the code conditions of the moves (beyond the table).
func (s *Service) guard(ctx context.Context, t *Tx, entity, id, from, to string) error {
	no := func(msg string) error {
		return &Error{Status: http.StatusConflict, Code: "invalid_state", Message: msg, NextStep: "Nothing was changed."}
	}
	// A failed read must not let a move through: the first error is returned below.
	var qerr error
	count := func(q string, args ...any) int {
		var n int
		if err := t.QueryRowContext(ctx, q, args...).Scan(&n); err != nil && qerr == nil {
			qerr = err
		}
		return n
	}
	switch entity + ":" + from + ">" + to {
	case "phase:Planned>Awaiting Approval", "phase:Revision Required>Awaiting Approval":
		if count(`SELECT count(*) FROM phases p JOIN workflow_versions v ON v.id = p.workflow_version_id WHERE p.id = ? AND v.state = 'approved'`, id) == 0 {
			return no("The workflow version of this phase is not approved.")
		}
		if count(`SELECT count(*) FROM phases p JOIN phases q ON q.workflow_version_id = p.workflow_version_id AND q.position < p.position WHERE p.id = ? AND q.state <> 'Completed'`, id) > 0 {
			return no("An earlier phase is not completed.")
		}
		if count(`SELECT count(*) FROM tasks WHERE phase_id = ?`, id) == 0 || count(`SELECT count(*) FROM validators WHERE phase_id = ?`, id) == 0 {
			return no("The phase has no tasks or no validators.")
		}
	case "phase:Revision Required>In Progress":
		// Work resumes only after a rejected review; a rejected start gate goes back through its approval.
		var last string
		if err := t.QueryRowContext(ctx, `SELECT from_state FROM state_transitions WHERE entity_type = 'phase' AND entity_id = ? ORDER BY id DESC LIMIT 1`, id).Scan(&last); err != nil && qerr == nil {
			qerr = err
		}
		if last != "Awaiting Phase Review" {
			return no("Only a phase whose review was rejected can resume work.")
		}
	case "phase:Ready for Execution>In Progress":
		if count(`SELECT count(*) FROM leases l JOIN tasks t ON t.id = l.task_id WHERE t.phase_id = ? AND l.revoked_at IS NULL`, id) == 0 {
			return no("A phase starts when an agent claims its first task.")
		}
	case "phase:In Progress>Validation":
		if count(`SELECT count(*) FROM tasks WHERE phase_id = ? AND parent_task_id IS NULL AND state NOT IN ('Done','Cancelled')`, id) > 0 {
			return no("Every task must be Done or Cancelled before validation.")
		}
		if count(`SELECT count(*) FROM leases l JOIN tasks t ON t.id = l.task_id WHERE t.phase_id = ? AND l.revoked_at IS NULL`, id) > 0 {
			return no("A lease is still active in this phase.")
		}
	case "task:Backlog>Ready":
		if count(`SELECT count(*) FROM task_dependencies d JOIN tasks x ON x.id = d.depends_on_task_id WHERE d.task_id = ? AND x.state <> 'Done'`, id) > 0 {
			return no("A dependency is not done.")
		}
		if count(`SELECT count(*) FROM tasks t JOIN phases p ON p.id = t.phase_id WHERE t.id = ? AND p.state IN ('Ready for Execution','In Progress')`, id) == 0 {
			return no("The phase is not open for work.")
		}
	case "task:Ready>In Progress":
		if count(`SELECT count(*) FROM leases WHERE task_id = ? AND revoked_at IS NULL`, id) == 0 {
			return no("A task starts only under a lease.")
		}
	}
	return wrap(qerr, "check %s move", entity)
}

// afterMove revokes leases that a move ends, in the same transaction.
func (s *Service) afterMove(ctx context.Context, t *Tx, entity, id, from, to string, c entityCtx) error {
	revoke := func(where, reason string, args ...any) error {
		_, err := t.ExecContext(ctx, `UPDATE leases SET revoked_at = ?, revoke_reason = ? WHERE revoked_at IS NULL AND `+where, append([]any{s.ts(), reason}, args...)...)
		return wrap(err, "revoke leases")
	}
	switch {
	case entity == "phase" && from == "In Progress":
		return revoke(`task_id IN (SELECT id FROM tasks WHERE phase_id = ?)`, "phase_exit", id)
	case entity == "task" && (to == "Done" || to == "Cancelled"):
		reason := "task_terminal"
		if to == "Cancelled" {
			reason = "cancelled"
		}
		return revoke(`task_id = ?`, reason, id)
	case entity == "execution" && (to == "Completed" || to == "Failed" || to == "Cancelled" || to == "Interrupted"):
		reason := map[string]string{"Completed": "task_terminal", "Failed": "task_terminal", "Cancelled": "cancelled", "Interrupted": "recovery"}[to]
		if to != "Interrupted" {
			// A pending risky-action or result request about an ended execution can no longer be decided.
			if _, err := t.ExecContext(ctx, `UPDATE approvals SET state = 'cancelled', decided_at = ? WHERE subject_id = ? AND state = 'pending' AND category = 'risky_action'`, s.ts(), id); err != nil {
				return wrap(err, "cancel approvals")
			}
		}
		return revoke(`execution_id = ?`, reason, id)
	}
	return nil
}
