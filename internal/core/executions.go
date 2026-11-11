package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"waves/internal/api/contract"
	"waves/internal/store"
)

const execSelect = `SELECT e.id, e.task_id, t.title, t.key, e.project_id, p.name, e.mode, e.state, COALESCE(t.blocked_reason,''), e.started_at, e.ended_at, e.created_at
	FROM executions e JOIN tasks t ON t.id = e.task_id JOIN projects p ON p.id = e.project_id`

func (s *Service) scanSummary(sc interface{ Scan(...any) error }) (contract.ExecutionSummary, error) {
	var x contract.ExecutionSummary
	var st, reason, created string
	var started, ended sql.NullString
	if err := sc.Scan(&x.ID, &x.TaskID, &x.TaskTitle, &x.TaskCode, &x.ProjectID, &x.ProjectName, &x.Mode, &st, &reason, &started, &ended, &created); err != nil {
		return x, err
	}
	x.Harness, x.State, x.TaskCode = "Claude Code", snake(st), strings.ToUpper(x.TaskCode)
	x.StartedAt = tptr(started)
	if x.StartedAt != nil {
		end := s.now()
		if ended.Valid {
			end = store.ParseTime(ended.String)
		}
		d := int(end.Sub(*x.StartedAt).Seconds())
		x.DurationS = &d
	}
	switch st {
	case "Interrupted", "Disconnected":
		x.StateDetail = "needs your decision"
	case "Waiting Approval":
		x.StateDetail = "risky action"
	}
	return x, nil
}

// Executions lists executions across projects, newest first.
func (s *Service) Executions(ctx context.Context, projectID, state, taskID string, limit int) (contract.ExecutionList, error) {
	rows, err := s.db.R.QueryContext(ctx, execSelect+` WHERE (? = '' OR e.project_id = ?) AND (? = '' OR e.task_id = ?) ORDER BY e.created_at DESC, e.rowid DESC LIMIT ?`,
		projectID, projectID, taskID, taskID, limit*2)
	if err != nil {
		return contract.ExecutionList{}, wrap(err, "list executions")
	}
	defer rows.Close()
	out := contract.ExecutionList{Items: []contract.ExecutionSummary{}}
	for rows.Next() {
		x, err := s.scanSummary(rows)
		if err != nil {
			return out, wrap(err, "scan execution")
		}
		if state != "" && x.State != state {
			continue
		}
		if len(out.Items) < limit {
			out.Items = append(out.Items, x)
		}
	}
	return out, rows.Err()
}

// ExecutionDetail is the execution page.
func (s *Service) ExecutionDetail(ctx context.Context, id string) (contract.ExecutionDetail, error) {
	sum, err := s.scanSummary(s.db.R.QueryRowContext(ctx, execSelect+` WHERE e.id = ?`, id))
	if err != nil {
		return contract.ExecutionDetail{}, errNotFound("execution", "Open the Executions list.")
	}
	d := contract.ExecutionDetail{ExecutionSummary: sum, History: []contract.StateChange{}, Events: []contract.ExecutionEvent{}}
	var st, wmode string
	var start, diff, res, errText sql.NullString
	s.db.R.QueryRowContext(ctx, `SELECT e.state, e.workspace_mode, w.path, e.start_commit, e.diff_path, e.result_json, e.error FROM executions e JOIN workspaces w ON w.id = e.workspace_id WHERE e.id = ?`, id).
		Scan(&st, &wmode, &d.WorkspacePath, &start, &diff, &res, &errText)
	d.HarnessVersion, d.StartCommit, d.Error = s.ClaudeCodeVersion(ctx), start.String, errText.String
	d.WorkspaceLocked = wmode == "code_change" && strings.Contains("Starting Running Waiting Approval Paused Disconnected Interrupted", st)
	if res.Valid {
		var r struct {
			Summary string `json:"summary"`
		}
		json.Unmarshal([]byte(res.String), &r)
		d.Result = r.Summary
	}
	if diff.Valid {
		d.DiffFiles, _, _, _ = diffStats(diff.String)
	}
	var lid, exp string
	if s.db.R.QueryRowContext(ctx, `SELECT id, expires_at FROM leases WHERE execution_id = ? AND revoked_at IS NULL`, id).Scan(&lid, &exp) == nil {
		d.Lease = &contract.Lease{ID: lid, ExpiresAt: store.ParseTime(exp), HeartbeatInterval: int(s.heartbeatInterval(ctx).Seconds())}
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT ts, to_state, trigger FROM state_transitions WHERE entity_type = 'execution' AND entity_id = ? ORDER BY id`, id)
	if err == nil {
		for rows.Next() {
			var at, to, trig string
			rows.Scan(&at, &to, &trig)
			d.History = append(d.History, contract.StateChange{At: store.ParseTime(at), State: snake(to), Detail: trig})
		}
		rows.Close()
	}
	rows, err = s.db.R.QueryContext(ctx, `SELECT ts, kind, payload_json FROM execution_events WHERE execution_id = ? ORDER BY id DESC LIMIT 50`, id)
	if err == nil {
		for rows.Next() {
			var at, kind, pl string
			rows.Scan(&at, &kind, &pl)
			var m map[string]any
			json.Unmarshal([]byte(pl), &m)
			msg, _ := m["message"].(string)
			if msg == "" {
				msg, _ = m["reason"].(string)
			}
			d.Events = append(d.Events, contract.ExecutionEvent{At: store.ParseTime(at), Kind: kind, Message: msg})
		}
		rows.Close()
	}
	if st == "Interrupted" || st == "Disconnected" {
		rec := &contract.Recovery{Reason: "The service stopped or the agent stopped sending heartbeats.", Timeline: d.History}
		if st == "Disconnected" {
			rec.Reason = "The agent stopped sending heartbeats. It can still reconnect until the lease expires."
		}
		if start.Valid {
			if p, n := s.captureDiff(ctx, id); p != "" {
				_, a, r, _ := diffStats(p)
				rec.PartialFiles, rec.PartialAdded, rec.PartialRemoved = n, a, r
				s.db.W.ExecContext(ctx, `UPDATE executions SET diff_path = ? WHERE id = ?`, p, id)
			}
		}
		if lp := s.logPath(ctx, id); lp != "" {
			if b, err := os.ReadFile(lp); err == nil {
				rec.LogLines = strings.Count(string(b), "\n")
			}
			if fi, err := os.Stat(lp); err == nil {
				rec.LastLogAt = fi.ModTime().UTC()
			}
		}
		var sess sql.NullString
		s.db.R.QueryRowContext(ctx, `SELECT session_id FROM executions WHERE id = ?`, id).Scan(&sess)
		rec.ResumeSession = sess.String
		d.Recovery = rec
	}
	return d, nil
}

func (s *Service) logPath(ctx context.Context, id string) string {
	var p sql.NullString
	s.db.R.QueryRowContext(ctx, `SELECT log_path FROM executions WHERE id = ?`, id).Scan(&p)
	return p.String
}

// OpenLog opens an execution log at offset for streaming.
func (s *Service) OpenLog(ctx context.Context, id string, offset int64) (io.ReadCloser, error) {
	if err := s.mustExecution(ctx, id); err != nil {
		return nil, err
	}
	f, err := os.Open(s.logPath(ctx, id))
	if err != nil {
		return io.NopCloser(strings.NewReader("")), nil
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			return nil, wrap(err, "seek log")
		}
	}
	return f, nil
}

// OpenDiff opens the stored diff of an execution.
func (s *Service) OpenDiff(ctx context.Context, id string) (io.ReadCloser, error) {
	if err := s.mustExecution(ctx, id); err != nil {
		return nil, err
	}
	var p sql.NullString
	s.db.R.QueryRowContext(ctx, `SELECT diff_path FROM executions WHERE id = ?`, id).Scan(&p)
	f, err := os.Open(p.String)
	if err != nil {
		return io.NopCloser(strings.NewReader("")), nil
	}
	return f, nil
}

func (s *Service) mustExecution(ctx context.Context, id string) error {
	var n int
	s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM executions WHERE id = ?`, id).Scan(&n)
	if n == 0 {
		return errNotFound("execution", "Open the Executions list.")
	}
	return nil
}

// CancelExecution interrupts an execution. Waves did not start the harness
// process, so it ends the execution and its lease; the agent sees the
// cancel on its next call. The task is Blocked until the human decides.
func (s *Service) CancelExecution(ctx context.Context, p Principal, id string) error {
	if !p.IsOperator() {
		return errWrongScope
	}
	return s.tx(ctx, func(t *Tx) error {
		var st, task string
		if err := t.QueryRowContext(ctx, `SELECT state, task_id FROM executions WHERE id = ?`, id).Scan(&st, &task); err != nil {
			return errNotFound("execution", "Open the Executions list.")
		}
		if st == "Completed" || st == "Failed" || st == "Cancelled" {
			return errInvalidState("An execution that is "+snake(st)+" cannot be interrupted.", "")
		}
		if _, err := t.ExecContext(ctx, `UPDATE executions SET cancel_requested = 1 WHERE id = ?`, id); err != nil {
			return wrap(err, "request cancel")
		}
		if _, err := s.transition(ctx, t, "execution", id, "Cancelled", actorOf(p), "interrupted by operator", ""); err != nil {
			return err
		}
		var ts string
		t.QueryRowContext(ctx, `SELECT state FROM tasks WHERE id = ?`, task).Scan(&ts)
		if ts == "In Progress" || ts == "Waiting Approval" {
			if ts == "Waiting Approval" {
				return nil // the task is decided in the approval inbox or cancelled there
			}
			if _, err := s.transition(ctx, t, "task", task, "Blocked", sysActor, "execution interrupted", ""); err != nil {
				return err
			}
			_, err := t.ExecContext(ctx, `UPDATE tasks SET blocked_reason = 'interrupted' WHERE id = ?`, task)
			return wrap(err, "record reason")
		}
		return nil
	})
}

// ResolveExecution records the human's decision on an interrupted or
// disconnected execution. Nothing is rerun by Waves.
func (s *Service) ResolveExecution(ctx context.Context, p Principal, id, decision string) error {
	if !p.IsOperator() {
		return errWrongScope
	}
	if decision == "resume" {
		return &Error{Status: http.StatusNotImplemented, Code: "unsupported", Message: "Waves cannot resume a Claude Code session it did not start.",
			NextStep: "Resume it yourself with claude --resume, or choose Retry so the agent claims the task again."}
	}
	diffPath, files := s.captureDiff(ctx, id)
	a := actorOf(p)
	return s.tx(ctx, func(t *Tx) error {
		var st, task, project string
		if err := t.QueryRowContext(ctx, `SELECT state, task_id, project_id FROM executions WHERE id = ?`, id).Scan(&st, &task, &project); err != nil {
			return errNotFound("execution", "Open the Executions list.")
		}
		if st != "Interrupted" && st != "Disconnected" {
			return errInvalidState("Only an interrupted or disconnected execution needs a decision.", "")
		}
		var ts string
		t.QueryRowContext(ctx, `SELECT state FROM tasks WHERE id = ?`, task).Scan(&ts)
		if ts == "In Progress" {
			if err := s.blockForRecovery(ctx, t, task); err != nil {
				return err
			}
		}
		switch decision {
		case "fail":
			_, err := s.transition(ctx, t, "execution", id, "Failed", a, "resolved: fail", "")
			return err
		case "retry_fresh":
			if _, err := s.transition(ctx, t, "execution", id, "Cancelled", a, "resolved: retry", ""); err != nil {
				return err
			}
			_, err := s.transition(ctx, t, "task", task, "Ready", a, "retry after recovery", "")
			return err
		case "complete_partial":
			if _, err := t.ExecContext(ctx, `UPDATE executions SET diff_path = ? WHERE id = ?`, store.NullStr(diffPath), id); err != nil {
				return wrap(err, "store diff")
			}
			if _, err := s.transition(ctx, t, "execution", id, "Completed", a, "resolved: accept partial work", ""); err != nil {
				return err
			}
			if _, err := s.transition(ctx, t, "task", task, "In Progress", a, "partial result accepted for review", ""); err != nil {
				return err
			}
			if _, err := s.transition(ctx, t, "task", task, "In Review", sysActor, "partial result needs review", ""); err != nil {
				return err
			}
			apr, err := s.requestApproval(ctx, t, "result", "task", task, project, "operator", "", map[string]any{"summary": "partial work after an interruption", "files_changed": files, "impact": "Accepting marks the task done with the partial changes."})
			if err != nil {
				return err
			}
			return s.refreshSubjectHash(ctx, t, apr)
		}
		return errValidation("Unknown decision.", "Use fail, complete_partial, resume or retry_fresh.")
	})
}

// Task actions from the CLI: cancel, retry.

// CancelTask cancels a task with a reason; its lease is revoked.
func (s *Service) CancelTask(ctx context.Context, p Principal, id, reason string) error {
	if !p.IsOperator() {
		return errWrongScope
	}
	if strings.TrimSpace(reason) == "" {
		return errValidation("Cancelling needs a reason.", "")
	}
	return s.tx(ctx, func(t *Tx) error {
		rows, err := t.QueryContext(ctx, `SELECT id FROM executions WHERE task_id = ? AND state NOT IN ('Completed','Failed','Cancelled')`, id)
		if err != nil {
			return wrap(err, "list open executions")
		}
		var open []string
		for rows.Next() {
			var e string
			rows.Scan(&e)
			open = append(open, e)
		}
		rows.Close()
		for _, e := range open {
			if _, err := s.transition(ctx, t, "execution", e, "Cancelled", actorOf(p), "task cancelled", ""); err != nil {
				return err
			}
		}
		if _, err := s.transition(ctx, t, "task", id, "Cancelled", actorOf(p), "cancelled: "+truncate(reason, 200), ""); err != nil {
			return err
		}
		if _, err := t.ExecContext(ctx, `UPDATE tasks SET blocked_reason = ? WHERE id = ?`, "cancelled: "+truncate(reason, 500), id); err != nil {
			return wrap(err, "record reason")
		}
		return s.afterTaskTerminal(ctx, t, id)
	})
}

// ReopenTasks sends finished tasks of a phase whose review the human rejected
// back to work: each task becomes Ready, the phase In Progress, and
// the next claim's context carries the review feedback. When the tasks are
// done again, validators run again and a new review is requested. It is a
// human action: it needs a passphrase step-up (a window covers it).
func (s *Service) ReopenTasks(ctx context.Context, p Principal, elevation, phaseID string, taskIDs []string, reason string) error {
	if !p.IsOperator() {
		s.auditBypass(ctx, p, "reopen tasks "+phaseID)
		return errWrongScope
	}
	if len(taskIDs) == 0 {
		return errValidation("Choose at least one task to reopen.", "")
	}
	if strings.TrimSpace(reason) == "" {
		return errValidation("Reopening needs a reason.", "Say what must be redone.")
	}
	return s.tx(ctx, func(t *Tx) error {
		if err := s.RequireElevation(ctx, t, p, elevation); err != nil {
			return err
		}
		var state, project string
		var vstate string
		if err := t.QueryRowContext(ctx, `SELECT p.state, w.project_id, v.state FROM phases p JOIN workflow_versions v ON v.id = p.workflow_version_id JOIN workflows w ON w.id = v.workflow_id WHERE p.id = ?`, phaseID).Scan(&state, &project, &vstate); err != nil {
			return errNotFound("phase", "Open the project workflow.")
		}
		if vstate != "approved" {
			return errInvalidState("This phase belongs to a workflow version that is no longer the approved one.", "Work on the approved version.")
		}
		// Only a rejected phase review reopens work; a rejected start gate goes back through its approval.
		var last string
		if err := t.QueryRowContext(ctx, `SELECT from_state FROM state_transitions WHERE entity_type = 'phase' AND entity_id = ? ORDER BY id DESC LIMIT 1`, phaseID).Scan(&last); err != nil {
			return wrap(err, "read last phase move")
		}
		if state != "Revision Required" || last != "Awaiting Phase Review" {
			return errInvalidState("Only a phase whose review was rejected can reopen tasks.", "")
		}
		for _, id := range taskIDs {
			var ph string
			var parent sql.NullString
			if err := t.QueryRowContext(ctx, `SELECT phase_id, parent_task_id FROM tasks WHERE id = ?`, id).Scan(&ph, &parent); err != nil || ph != phaseID || parent.Valid {
				return errValidation("Task "+id+" is not a task of this phase.", "")
			}
			if _, err := s.transition(ctx, t, "task", id, "Ready", Actor{Scope: "human", Ref: p.ActorRef()}, "reopened after review", ""); err != nil {
				return err
			}
		}
		if _, err := s.transition(ctx, t, "phase", phaseID, "In Progress", Actor{Scope: "human", Ref: p.ActorRef()}, "revision started", ""); err != nil {
			return err
		}
		return t.audit(ctx, store.AuditRecord{Event: "phase.tasks_reopened", ActorScope: "human", ActorRef: p.ActorRef(), ProjectID: project, Subject: phaseID,
			Detail: map[string]any{"tasks": taskIDs, "reason": truncate(reason, 2000)}})
	})
}

// RetryTask moves a Blocked task back to Ready after the human decided.
func (s *Service) RetryTask(ctx context.Context, p Principal, id string) error {
	if !p.IsOperator() {
		return errWrongScope
	}
	return s.tx(ctx, func(t *Tx) error {
		var exe string
		err := t.QueryRowContext(ctx, `SELECT id FROM executions WHERE task_id = ? AND state NOT IN ('Completed','Failed','Cancelled') LIMIT 1`, id).Scan(&exe)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return wrap(err, "check open executions")
		}
		if exe != "" {
			return errInvalidState("Execution "+exe+" of this task is still open.", "Decide it on its Recovery page in the dashboard, or cancel it: waves execution cancel "+exe)
		}
		_, err = s.transition(ctx, t, "task", id, "Ready", actorOf(p), "retry", "")
		return err
	})
}

// ResumeTask unblocks a task whose agent reported a blocker and still holds
// its lease: the agent continues. Without a live lease there is nothing to
// resume; retry or resolve the execution instead.
func (s *Service) ResumeTask(ctx context.Context, p Principal, id string) error {
	if !p.IsOperator() {
		return errWrongScope
	}
	return s.tx(ctx, func(t *Tx) error {
		var leases int
		if err := t.QueryRowContext(ctx, `SELECT count(*) FROM leases l JOIN executions e ON e.id = l.execution_id WHERE l.task_id = ? AND l.revoked_at IS NULL AND l.expires_at > ? AND e.state = 'Running'`, id, s.ts()).Scan(&leases); err != nil {
			return wrap(err, "check lease")
		}
		if leases == 0 {
			return &Error{Status: http.StatusNotImplemented, Code: "unsupported", Message: "No agent holds this task, and Waves cannot resume a Claude Code session it did not start.",
				NextStep: "Retry the task so an agent claims it again, or resume the session yourself with claude --resume."}
		}
		if _, err := s.transition(ctx, t, "task", id, "In Progress", actorOf(p), "blocker resolved", ""); err != nil {
			return err
		}
		_, err := t.ExecContext(ctx, `UPDATE tasks SET blocked_reason = NULL WHERE id = ?`, id)
		return wrap(err, "clear reason")
	})
}
