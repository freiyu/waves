package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"waves/internal/store"
)

// Lease timing, in heartbeat intervals: an execution counts as disconnected
// after 2 missed intervals; the lease expires after 6.
const (
	disconnectAfter = 2
	expireAfter     = 6
	minHeartbeat    = 15 * time.Second
)

func (s *Service) heartbeatInterval(ctx context.Context) time.Duration {
	return time.Duration(max(s.settingInt(ctx, "heartbeat_interval_s"), 15)) * time.Second
}

func leaseRefused(code, msg string) *Error {
	next := map[string]string{
		"lease_invalid":    "Stop work on this task. Your lease ended or was cancelled; the human decides what happens next.",
		"limit_exceeded":   "Wait, or report this to the human. Do not retry in a loop.",
		"workspace_locked": "Wait, or report this to the human. Do not retry in a loop.",
	}[code]
	if next == "" {
		next = "List available tasks and claim one."
	}
	return &Error{Status: http.StatusForbidden, Code: code, Message: msg, NextStep: next}
}

// ClaimResult is what waves_claim_task returns.
type ClaimResult struct {
	LeaseToken  string    `json:"lease_token"`
	ExecutionID string    `json:"execution_id"`
	TaskID      string    `json:"task_id"`
	ExpiresAt   time.Time `json:"expires_at"`
	HeartbeatS  int       `json:"heartbeat_interval_s"`
}

// AvailableTask is one claimable task.
type AvailableTask struct {
	ID            string   `json:"task_id"`
	Key           string   `json:"key"`
	Title         string   `json:"title"`
	Phase         string   `json:"phase"`
	WorkspaceMode string   `json:"workspace_mode"`
	Acceptance    []string `json:"acceptance_criteria"`
}

// AvailableTasks lists Ready tasks of open phases in the caller's project only.
func (s *Service) AvailableTasks(ctx context.Context, p Principal) ([]AvailableTask, error) {
	if p.Kind != KindMCP || p.ProjectID == "" {
		return nil, errWrongScope
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT t.id, t.key, t.title, ph.key, t.workspace_mode, t.acceptance_json FROM tasks t JOIN phases ph ON ph.id = t.phase_id
		JOIN workflow_versions v ON v.id = ph.workflow_version_id JOIN workflows w ON w.id = v.workflow_id
		WHERE w.project_id = ? AND v.state = 'approved' AND ph.state IN ('Ready for Execution','In Progress') AND t.state = 'Ready' AND t.parent_task_id IS NULL
		ORDER BY ph.position, CASE t.priority WHEN 'high' THEN 0 WHEN 'normal' THEN 1 ELSE 2 END, t.position`, p.ProjectID)
	if err != nil {
		return nil, wrap(err, "list available tasks")
	}
	defer rows.Close()
	out := []AvailableTask{}
	for rows.Next() {
		var a AvailableTask
		var acc string
		rows.Scan(&a.ID, &a.Key, &a.Title, &a.Phase, &a.WorkspaceMode, &acc)
		a.Acceptance = []string{}
		json.Unmarshal([]byte(acc), &a.Acceptance)
		out = append(out, a)
	}
	return out, rows.Err()
}

func gitHead(ctx context.Context, dir string) string {
	out, err := git(ctx, dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ClaimTask grants a lease for one Ready task to the calling MCP session. In
// one transaction it creates the execution, takes the workspace lock, grants
// the lease and starts task and phase. Duplicate claims fail on the unique
// indexes (one active lease per task, one code-changing execution per workspace).
func (s *Service) ClaimTask(ctx context.Context, p Principal, mcpSession, taskID, toolUseID string) (ClaimResult, error) {
	if p.Kind != KindMCP || p.ProjectID == "" {
		return ClaimResult{}, errWrongScope
	}
	if mcpSession == "" {
		return ClaimResult{}, leaseRefused("lease_invalid", "A claim needs an MCP session.")
	}
	root, err := s.projectPath(ctx, s.db.R, p.ProjectID)
	if err != nil {
		return ClaimResult{}, leaseRefused("task_not_ready", "This task cannot be claimed.")
	}
	head := gitHead(ctx, root)
	fingerprint := workspaceFingerprint(ctx, root)
	hb := s.heartbeatInterval(ctx)
	secret := NewSecret(PrefixLease)
	res := ClaimResult{LeaseToken: secret, ExecutionID: NewID("exe"), TaskID: taskID, HeartbeatS: int(hb.Seconds())}
	lease := NewID("lse")
	err = s.tx(ctx, func(t *Tx) error {
		var tstate, pstate, project, phase, mode, wsp string
		err := t.QueryRowContext(ctx, `SELECT t.state, ph.state, w.project_id, ph.id, t.workspace_mode FROM tasks t JOIN phases ph ON ph.id = t.phase_id
			JOIN workflow_versions v ON v.id = ph.workflow_version_id JOIN workflows w ON w.id = v.workflow_id WHERE t.id = ? AND t.parent_task_id IS NULL AND v.state = 'approved'`, taskID).
			Scan(&tstate, &pstate, &project, &phase, &mode)
		// Tasks of other projects look exactly like missing ones.
		if err != nil || project != p.ProjectID {
			return leaseRefused("task_not_ready", "This task cannot be claimed.")
		}
		if pstate != "Ready for Execution" && pstate != "In Progress" {
			return leaseRefused("phase_not_open", "This task's phase is not open for work.")
		}
		if tstate != "Ready" {
			return leaseRefused("task_not_ready", "This task is not Ready.")
		}
		var active int
		if err := t.QueryRowContext(ctx, `SELECT count(*) FROM executions WHERE state IN ('Starting','Running','Waiting Approval','Paused')`).Scan(&active); err != nil {
			return wrap(err, "count running executions")
		}
		if limit := s.settingInt(ctx, "max_concurrent"); active >= limit {
			return leaseRefused("limit_exceeded", fmt.Sprintf("%d executions are already running (limit %d).", active, limit))
		}
		if err := t.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE project_id = ? AND mode = 'in_place'`, project).Scan(&wsp); err != nil {
			return wrap(err, "read workspace")
		}
		hid, err := s.claudeHarnessID(ctx, t)
		if err != nil {
			return err
		}
		logPath := filepath.Join(s.dirs.Logs(), res.ExecutionID+".log")
		if _, err := t.ExecContext(ctx, `INSERT INTO executions(id, task_id, project_id, harness_id, workspace_id, mode, workspace_mode, start_commit, log_path, created_at) VALUES (?,?,?,?,?, 'harness_driven', ?,?,?,?)`,
			res.ExecutionID, taskID, project, hid, wsp, mode, store.NullStr(head), logPath, s.ts()); err != nil {
			return wrap(err, "create execution")
		}
		if _, err := s.transition(ctx, t, "execution", res.ExecutionID, "Starting", sysActor, "claimed", ""); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return leaseRefused("workspace_locked", "Another code-changing execution holds this workspace.")
			}
			return err
		}
		exp := s.now().Add(expireAfter * hb)
		res.ExpiresAt = exp
		if _, err := t.ExecContext(ctx, `INSERT INTO leases(id, execution_id, task_id, project_id, token_hash, granted_at, expires_at, last_heartbeat_at, mcp_session) VALUES (?,?,?,?,?,?,?,?,?)`,
			lease, res.ExecutionID, taskID, project, HashSecret(secret), s.ts(), store.FormatTime(exp), s.ts(), mcpSession); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return leaseRefused("task_not_ready", "This task is already leased.")
			}
			return wrap(err, "grant lease")
		}
		if _, err := s.transition(ctx, t, "task", taskID, "In Progress", sysActor, "lease granted", ""); err != nil {
			return err
		}
		if pstate == "Ready for Execution" {
			if _, err := s.transition(ctx, t, "phase", phase, "In Progress", sysActor, "first lease granted", ""); err != nil {
				return err
			}
		}
		if _, err := s.transition(ctx, t, "execution", res.ExecutionID, "Running", Actor{Scope: "agent", Ref: p.ActorRef()}, "agent claimed the lease", ""); err != nil {
			return err
		}
		if _, err := t.ExecContext(ctx, `INSERT INTO execution_events(execution_id, ts, kind, payload_json) VALUES (?,?, 'workspace', ?)`, res.ExecutionID, s.ts(), `{"fingerprint":"`+fingerprint+`"}`); err != nil {
			return wrap(err, "record workspace state")
		}
		if toolUseID != "" {
			if err := s.bindToolUse(ctx, t, toolUseID, "", lease); err != nil {
				return err
			}
		}
		t.afterCommit(s.pokeLeases)
		return t.audit(ctx, store.AuditRecord{Event: "lease.granted", ActorScope: "agent", ActorRef: p.ActorRef(), ProjectID: project, Subject: lease, Detail: map[string]any{"task_id": taskID, "execution_id": res.ExecutionID}})
	})
	if err != nil {
		if e := AsError(err); e != nil && e.Status == http.StatusForbidden {
			s.Audit(ctx, store.AuditRecord{Event: "lease.refused", ActorScope: "agent", ActorRef: p.ActorRef(), ProjectID: p.ProjectID, Subject: taskID, Detail: map[string]any{"reason": e.Code}})
		}
		return ClaimResult{}, err
	}
	os.MkdirAll(s.dirs.Logs(), 0o700)
	s.appendLog(res.ExecutionID, "lease granted, task "+taskID)
	return res, nil
}

// bindToolUse joins the hook event and the MCP call that carry the same
// tool_use_id, in either order, and binds the Claude Code session to the lease.
func (s *Service) bindToolUse(ctx context.Context, t *Tx, toolUseID, sessionID, leaseID string) error {
	if _, err := t.ExecContext(ctx, `INSERT INTO tool_use_bindings(tool_use_id, session_id, lease_id, hook_seen_at, mcp_seen_at) VALUES (?,?,?,?,?)
		ON CONFLICT(tool_use_id) DO UPDATE SET session_id = COALESCE(excluded.session_id, session_id), lease_id = COALESCE(excluded.lease_id, lease_id),
		hook_seen_at = COALESCE(hook_seen_at, excluded.hook_seen_at), mcp_seen_at = COALESCE(mcp_seen_at, excluded.mcp_seen_at)`,
		toolUseID, store.NullStr(sessionID), store.NullStr(leaseID), map[bool]any{true: s.ts()}[sessionID != ""], map[bool]any{true: s.ts()}[leaseID != ""]); err != nil {
		return wrap(err, "bind tool use")
	}
	var sess, lease sql.NullString
	t.QueryRowContext(ctx, `SELECT session_id, lease_id FROM tool_use_bindings WHERE tool_use_id = ?`, toolUseID).Scan(&sess, &lease)
	if !sess.Valid || !lease.Valid {
		return nil
	}
	// First binding wins: a later tool use cannot move the lease to another session.
	if _, err := t.ExecContext(ctx, `UPDATE leases SET session_id = ? WHERE id = ? AND session_id IS NULL`, sess.String, lease.String); err != nil {
		return wrap(err, "bind session to lease")
	}
	_, err := t.ExecContext(ctx, `UPDATE executions SET session_id = ? WHERE id = (SELECT execution_id FROM leases WHERE id = ?) AND session_id IS NULL`, sess.String, lease.String)
	return wrap(err, "bind session to execution")
}

// leaseRow is an active lease resolved for a lease call.
type leaseRow struct {
	id, execution, task, project, phase, state string
}

// lease resolves a lease for a lease-scoped MCP call: the token (or, when it
// is empty, the single active lease of this MCP session) must be active,
// unexpired, of the caller's project and claimed on this MCP session.
func (s *Service) lease(ctx context.Context, q store.Execer, p Principal, mcpSession, token string) (leaseRow, error) {
	if p.Kind != KindMCP {
		return leaseRow{}, errWrongScope
	}
	bad := leaseRefused("lease_invalid", "This lease is not valid on this connection.")
	where, arg := `l.token_hash = ?`, HashSecret(token)
	if token == "" {
		where, arg = `l.mcp_session = ? AND l.revoked_at IS NULL`, mcpSession
	}
	rows, err := q.QueryContext(ctx, `SELECT l.id, l.execution_id, l.task_id, l.project_id, t.phase_id, e.state, l.revoked_at, l.expires_at, COALESCE(l.mcp_session,'')
		FROM leases l JOIN tasks t ON t.id = l.task_id JOIN executions e ON e.id = l.execution_id WHERE `+where, arg)
	if err != nil {
		return leaseRow{}, wrap(err, "read lease")
	}
	defer rows.Close()
	var out []leaseRow
	for rows.Next() {
		var l leaseRow
		var revoked sql.NullString
		var exp, sess string
		rows.Scan(&l.id, &l.execution, &l.task, &l.project, &l.phase, &l.state, &revoked, &exp, &sess)
		if revoked.Valid || !store.ParseTime(exp).After(s.now()) || l.project != p.ProjectID || sess != mcpSession {
			return leaseRow{}, bad
		}
		out = append(out, l)
	}
	if len(out) != 1 {
		return leaseRow{}, bad
	}
	return out[0], nil
}

// HeartbeatResult tells the agent what changed.
type HeartbeatResult struct {
	LeaseOK         bool      `json:"lease_ok"`
	PhaseState      string    `json:"phase_state"`
	CancelRequested bool      `json:"cancel_requested"`
	Approval        string    `json:"approval,omitempty"` // pending | approved | rejected
	ExpiresAt       time.Time `json:"expires_at"`
}

// Heartbeat extends the lease. Calls closer than 15 s apart do not extend it.
func (s *Service) Heartbeat(ctx context.Context, p Principal, mcpSession, token string) (HeartbeatResult, error) {
	var out HeartbeatResult
	err := s.tx(ctx, func(t *Tx) error {
		l, err := s.lease(ctx, t, p, mcpSession, token)
		if err != nil {
			return err
		}
		var last, exp string
		var cancel int
		t.QueryRowContext(ctx, `SELECT l.last_heartbeat_at, l.expires_at, e.cancel_requested FROM leases l JOIN executions e ON e.id = l.execution_id WHERE l.id = ?`, l.id).Scan(&last, &exp, &cancel)
		out.ExpiresAt = store.ParseTime(exp)
		if s.now().Sub(store.ParseTime(last)) >= minHeartbeat || l.state == "Disconnected" {
			e := s.now().Add(expireAfter * s.heartbeatInterval(ctx))
			out.ExpiresAt = e
			if _, err := t.ExecContext(ctx, `UPDATE leases SET last_heartbeat_at = ?, expires_at = ? WHERE id = ?`, s.ts(), store.FormatTime(e), l.id); err != nil {
				return wrap(err, "renew lease")
			}
		}
		if l.state == "Disconnected" {
			if _, err := s.transition(ctx, t, "execution", l.execution, "Running", Actor{Scope: "agent", Ref: p.ActorRef()}, "heartbeat resumed", ""); err != nil {
				return err
			}
		}
		var ps string
		t.QueryRowContext(ctx, `SELECT state FROM phases WHERE id = ?`, l.phase).Scan(&ps)
		var apr sql.NullString
		t.QueryRowContext(ctx, `SELECT state FROM approvals WHERE subject_id = ? AND category = 'risky_action' ORDER BY requested_at DESC, rowid DESC LIMIT 1`, l.execution).Scan(&apr)
		out.LeaseOK, out.PhaseState, out.CancelRequested, out.Approval = true, snake(ps), cancel == 1, apr.String
		t.afterCommit(s.pokeLeases)
		return nil
	})
	return out, err
}

// ReportProgress appends a progress line to the execution.
func (s *Service) ReportProgress(ctx context.Context, p Principal, mcpSession, token, message string, percent *int) error {
	message = truncate(message, 2000)
	err := s.tx(ctx, func(t *Tx) error {
		l, err := s.lease(ctx, t, p, mcpSession, token)
		if err != nil {
			return err
		}
		pl, _ := json.Marshal(map[string]any{"message": message, "percent": percent})
		_, err = t.ExecContext(ctx, `INSERT INTO execution_events(execution_id, ts, kind, payload_json) VALUES (?,?, 'progress', ?)`, l.execution, s.ts(), string(pl))
		if err == nil {
			t.afterCommit(func() { s.appendLog(l.execution, "progress "+message) })
		}
		return wrap(err, "record progress")
	})
	return err
}

// ReportBlocker blocks the task with the agent's reason; the human decides.
func (s *Service) ReportBlocker(ctx context.Context, p Principal, mcpSession, token, reason string) error {
	reason = truncate(reason, 2000)
	if strings.TrimSpace(reason) == "" {
		return errValidation("A blocker needs a reason.", "")
	}
	return s.tx(ctx, func(t *Tx) error {
		l, err := s.lease(ctx, t, p, mcpSession, token)
		if err != nil {
			return err
		}
		if _, err := s.transition(ctx, t, "task", l.task, "Blocked", Actor{Scope: "agent", Ref: p.ActorRef()}, "blocker reported", ""); err != nil {
			return err
		}
		if _, err := t.ExecContext(ctx, `UPDATE tasks SET blocked_reason = ? WHERE id = ?`, "blocker: "+reason, l.task); err != nil {
			return wrap(err, "record blocker")
		}
		pl, _ := json.Marshal(map[string]string{"reason": reason})
		t.ExecContext(ctx, `INSERT INTO execution_events(execution_id, ts, kind, payload_json) VALUES (?,?, 'blocker', ?)`, l.execution, s.ts(), string(pl))
		t.afterCommit(func() { s.appendLog(l.execution, "blocker  "+reason) })
		t.publish("attention.changed", func() any { return map[string]int{"count": s.AttentionCount(ctx)} })
		return nil
	})
}

// RequestRiskyApproval puts the execution in Waiting Approval and opens a
// risky_action approval. The agent reads the decision on its next heartbeat.
func (s *Service) RequestRiskyApproval(ctx context.Context, p Principal, mcpSession, token, action, reason, impact string) (string, error) {
	var apr string
	err := s.tx(ctx, func(t *Tx) error {
		l, err := s.lease(ctx, t, p, mcpSession, token)
		if err != nil {
			return err
		}
		if _, err := s.transition(ctx, t, "execution", l.execution, "Waiting Approval", Actor{Scope: "agent", Ref: p.ActorRef()}, "risky action requested", ""); err != nil {
			return err
		}
		im := map[string]any{"action": truncate(action, 500), "reason": truncate(reason, 2000), "impact": truncate(impact, 2000)}
		if apr, err = s.requestApproval(ctx, t, "risky_action", "execution", l.execution, l.project, "agent", "", im); err != nil {
			return err
		}
		return s.refreshSubjectHash(ctx, t, apr)
	})
	return apr, err
}

// SubmitResult ends the execution with the agent's result. A completed
// code-changing task goes to review (Result approval); a read-only task is done.
func (s *Service) SubmitResult(ctx context.Context, p Principal, mcpSession, token, summary, status string, artifacts []string) error {
	if status != "completed" && status != "failed" {
		return errValidation("status must be completed or failed.", "")
	}
	l, err := s.lease(ctx, s.db.R, p, mcpSession, token)
	if err != nil {
		return err
	}
	diffPath, files := s.captureDiff(ctx, l.execution)
	return s.tx(ctx, func(t *Tx) error {
		if l, err = s.lease(ctx, t, p, mcpSession, token); err != nil {
			return err
		}
		res, _ := json.Marshal(map[string]any{"summary": truncate(summary, 8000), "status": status, "artifacts": artifacts, "files_changed": files})
		if _, err := t.ExecContext(ctx, `UPDATE executions SET result_json = ?, diff_path = ?, end_commit = ? WHERE id = ?`, string(res), store.NullStr(diffPath), store.NullStr(s.headOf(ctx, t, l.project)), l.execution); err != nil {
			return wrap(err, "store result")
		}
		agent := Actor{Scope: "agent", Ref: p.ActorRef()}
		if status == "failed" {
			if _, err := s.transition(ctx, t, "execution", l.execution, "Failed", sysActor, "agent reported failure", ""); err != nil {
				return err
			}
			if _, err := s.transition(ctx, t, "task", l.task, "Blocked", agent, "execution failed", ""); err != nil {
				return err
			}
			_, err := t.ExecContext(ctx, `UPDATE tasks SET blocked_reason = 'execution_failed' WHERE id = ?`, l.task)
			return wrap(err, "record reason")
		}
		if _, err := s.transition(ctx, t, "execution", l.execution, "Completed", sysActor, "result submitted", ""); err != nil {
			return err
		}
		var mode string
		t.QueryRowContext(ctx, `SELECT workspace_mode FROM tasks WHERE id = ?`, l.task).Scan(&mode)
		if mode == "read_only" {
			if _, err := s.transition(ctx, t, "task", l.task, "Done", sysActor, "read-only result accepted", ""); err != nil {
				return err
			}
			return s.afterTaskTerminal(ctx, t, l.task)
		}
		if _, err := s.transition(ctx, t, "task", l.task, "In Review", sysActor, "result needs review", ""); err != nil {
			return err
		}
		im := map[string]any{"summary": truncate(summary, 500), "files_changed": files, "impact": "Accepting marks the task done and may let the phase move to validation."}
		apr, err := s.requestApproval(ctx, t, "result", "task", l.task, l.project, "agent", "", im)
		if err != nil {
			return err
		}
		return s.refreshSubjectHash(ctx, t, apr)
	})
}

// refreshSubjectHash stores the hash of a just-created approval.
func (s *Service) refreshSubjectHash(ctx context.Context, t *Tx, apr string) error {
	var cat, stype, sid string
	t.QueryRowContext(ctx, `SELECT category, subject_type, subject_id FROM approvals WHERE id = ?`, apr).Scan(&cat, &stype, &sid)
	h, err := s.subjectHash(ctx, t, cat, stype, sid)
	if err != nil {
		return err
	}
	_, err = t.ExecContext(ctx, `UPDATE approvals SET subject_hash = ? WHERE id = ? AND state = 'pending'`, h, apr)
	return wrap(err, "store subject hash")
}

func (s *Service) headOf(ctx context.Context, q store.Execer, project string) string {
	root, err := s.projectPath(ctx, q, project)
	if err != nil {
		return ""
	}
	return gitHead(ctx, root)
}

// ReleaseTask gives the task back. The task returns to Ready only when the
// workspace is unchanged since the start commit; otherwise it is Blocked
// with reason released_with_changes.
func (s *Service) ReleaseTask(ctx context.Context, p Principal, mcpSession, token, reason string) (string, error) {
	l, err := s.lease(ctx, s.db.R, p, mcpSession, token)
	if err != nil {
		return "", err
	}
	unchanged := s.workspaceUnchanged(ctx, l.execution)
	var to string
	err = s.tx(ctx, func(t *Tx) error {
		if l, err = s.lease(ctx, t, p, mcpSession, token); err != nil {
			return err
		}
		if _, err := s.transition(ctx, t, "execution", l.execution, "Cancelled", Actor{Scope: "agent", Ref: p.ActorRef()}, "released: "+truncate(reason, 200), ""); err != nil {
			return err
		}
		if _, err := t.ExecContext(ctx, `UPDATE leases SET revoked_at = ?, revoke_reason = 'released' WHERE id = ? AND revoked_at IS NULL`, s.ts(), l.id); err != nil {
			return wrap(err, "revoke lease")
		}
		to = "Ready"
		if !unchanged {
			to = "Blocked"
		}
		if _, err := s.transition(ctx, t, "task", l.task, to, sysActor, "lease released", ""); err != nil {
			return err
		}
		if to == "Blocked" {
			if _, err := t.ExecContext(ctx, `UPDATE tasks SET blocked_reason = 'released_with_changes' WHERE id = ?`, l.task); err != nil {
				return wrap(err, "record reason")
			}
		}
		return t.audit(ctx, store.AuditRecord{Event: "lease.revoked", ActorScope: "agent", ActorRef: p.ActorRef(), ProjectID: l.project, Subject: l.id, Detail: map[string]any{"reason": "released", "task_to": to}})
	})
	return snake(to), err
}

// workspaceUnchanged compares the workspace with its state at claim time:
// same HEAD, same changes against HEAD, same untracked files. A folder that
// is not a Git repository cannot be checked and counts as changed.
func (s *Service) workspaceUnchanged(ctx context.Context, execution string) bool {
	var root, payload string
	if s.db.R.QueryRowContext(ctx, `SELECT w.path FROM executions e JOIN workspaces w ON w.id = e.workspace_id WHERE e.id = ?`, execution).Scan(&root) != nil {
		return false
	}
	if s.db.R.QueryRowContext(ctx, `SELECT payload_json FROM execution_events WHERE execution_id = ? AND kind = 'workspace' ORDER BY id LIMIT 1`, execution).Scan(&payload) != nil {
		return false
	}
	var v struct {
		Fingerprint string `json:"fingerprint"`
	}
	json.Unmarshal([]byte(payload), &v)
	now := workspaceFingerprint(ctx, root)
	return v.Fingerprint != "" && now != "" && now == v.Fingerprint
}

// workspaceFingerprint hashes HEAD, the binary diff against HEAD and every
// untracked file's content. "" when the folder is not a Git work tree.
func workspaceFingerprint(ctx context.Context, root string) string {
	head := gitHead(ctx, root)
	// Without a usable comparison the workspace counts as changed (safe side).
	if head == "" || len(gitFilters(ctx, root)) > 0 {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(head))
	d := git(ctx, root, "diff", "HEAD", "--binary", "--text")
	d.Stdout = h
	if d.Run() != nil {
		return ""
	}
	list, err := untrackedFiles(ctx, root)
	if err != nil {
		return ""
	}
	for _, f := range list {
		fh, _ := fileHash(filepath.Join(root, f))
		h.Write([]byte(f + "\x00" + fh + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// appendLog adds one timestamped line to an execution log and announces it.
func (s *Service) appendLog(execution, line string) {
	path := filepath.Join(s.dirs.Logs(), execution+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		s.log.Error("execution log", "err", err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", s.now().Local().Format("15:04:05"), strings.ReplaceAll(line, "\n", " "))
	if st, err := f.Stat(); err == nil {
		s.bus.Publish("execution.log", map[string]any{"execution_id": execution, "offset": st.Size()})
	}
}

// ---- expiry ----

// pokeLeases wakes the expiry loop after a lease changed.
func (s *Service) pokeLeases() {
	select {
	case s.leasePoke <- struct{}{}:
	default:
	}
}

// leaseLoop sleeps until the next lease deadline (or a change), then marks
// silent executions Disconnected and expired leases ended. It never reruns anything.
func (s *Service) leaseLoop(ctx context.Context) {
	for {
		next := s.expireLeases(ctx)
		wait := time.Hour
		if !next.IsZero() {
			wait = max(next.Sub(s.now()), 100*time.Millisecond)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.leasePoke:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// expireLeases handles overdue leases and returns the next deadline.
func (s *Service) expireLeases(ctx context.Context) time.Time {
	hb := s.heartbeatInterval(ctx)
	now := s.now()
	rows, err := s.db.R.QueryContext(ctx, `SELECT l.id, l.execution_id, l.task_id, l.last_heartbeat_at, l.expires_at, e.state FROM leases l JOIN executions e ON e.id = l.execution_id WHERE l.revoked_at IS NULL`)
	if err != nil {
		s.log.Error("read leases", "err", err)
		return now.Add(time.Minute)
	}
	type lr struct{ id, exe, task, last, exp, state string }
	var all []lr
	for rows.Next() {
		var l lr
		rows.Scan(&l.id, &l.exe, &l.task, &l.last, &l.exp, &l.state)
		all = append(all, l)
	}
	rows.Close()
	var next time.Time
	soonest := func(t time.Time) {
		if next.IsZero() || t.Before(next) {
			next = t
		}
	}
	for _, l := range all {
		exp := store.ParseTime(l.exp)
		disc := store.ParseTime(l.last).Add(disconnectAfter * hb)
		switch {
		case !exp.After(now):
			if err := s.expireLease(ctx, l.id, l.exe, l.task); err != nil {
				s.log.Error("expire lease", "lease", l.id, "err", err)
			}
		case l.state == "Running" && !disc.After(now):
			err := s.tx(ctx, func(t *Tx) error {
				_, err := s.transition(ctx, t, "execution", l.exe, "Disconnected", sysActor, "heartbeat missed", "")
				return err
			})
			if err != nil {
				s.log.Error("mark disconnected", "execution", l.exe, "err", err)
			}
			soonest(exp)
		case l.state == "Running":
			soonest(disc)
		default:
			soonest(exp)
		}
	}
	return next
}

// expireLease ends a lease whose heartbeat stopped: the execution is
// Interrupted and the task Blocked with recovery_required. Nothing reruns.
func (s *Service) expireLease(ctx context.Context, lease, exe, task string) error {
	return s.tx(ctx, func(t *Tx) error {
		if _, err := t.ExecContext(ctx, `UPDATE leases SET revoked_at = ?, revoke_reason = 'expired' WHERE id = ? AND revoked_at IS NULL`, s.ts(), lease); err != nil {
			return wrap(err, "expire lease")
		}
		var st string
		t.QueryRowContext(ctx, `SELECT state FROM executions WHERE id = ?`, exe).Scan(&st)
		if st != "Completed" && st != "Failed" && st != "Cancelled" && st != "Interrupted" {
			if _, err := s.transition(ctx, t, "execution", exe, "Interrupted", sysActor, "lease expired", ""); err != nil {
				return err
			}
		}
		if err := s.blockForRecovery(ctx, t, task); err != nil {
			return err
		}
		return t.audit(ctx, store.AuditRecord{Event: "recovery.lease_expired", ActorScope: "system", Subject: lease, Detail: map[string]any{"execution_id": exe}})
	})
}

// blockForRecovery moves a task that was being worked on to Blocked with
// reason recovery_required. It is never moved to Ready.
func (s *Service) blockForRecovery(ctx context.Context, t *Tx, task string) error {
	var st string
	if err := t.QueryRowContext(ctx, `SELECT state FROM tasks WHERE id = ?`, task).Scan(&st); err != nil {
		return wrap(err, "read task for recovery")
	}
	if st != "In Progress" {
		return nil
	}
	if _, err := s.transition(ctx, t, "task", task, "Blocked", sysActor, "recovery required", ""); err != nil {
		return err
	}
	_, err := t.ExecContext(ctx, `UPDATE tasks SET blocked_reason = 'recovery_required' WHERE id = ?`, task)
	return wrap(err, "record reason")
}

// recoverExecutions runs at startup: every execution that was active when
// the service stopped becomes Interrupted, its lease ends with reason
// recovery and its task is Blocked with recovery_required.
func (s *Service) recoverExecutions(ctx context.Context) error {
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, task_id FROM executions WHERE state IN ('Starting','Running','Waiting Approval','Paused','Disconnected')`)
	if err != nil {
		return wrap(err, "find interrupted executions")
	}
	type ex struct{ id, task string }
	var list []ex
	for rows.Next() {
		var e ex
		rows.Scan(&e.id, &e.task)
		list = append(list, e)
	}
	rows.Close()
	for _, e := range list {
		err := s.tx(ctx, func(t *Tx) error {
			var st string
			t.QueryRowContext(ctx, `SELECT state FROM executions WHERE id = ?`, e.id).Scan(&st)
			if _, err := s.transition(ctx, t, "execution", e.id, "Interrupted", sysActor, "service restart", ""); err != nil {
				return err
			}
			if err := s.blockForRecovery(ctx, t, e.task); err != nil {
				return err
			}
			return t.audit(ctx, store.AuditRecord{Event: "recovery.execution_interrupted", ActorScope: "system", Subject: e.id, Detail: map[string]any{"from": st}})
		})
		if err != nil {
			return err
		}
	}
	if len(list) > 0 {
		s.Audit(ctx, store.AuditRecord{Event: "recovery.started", ActorScope: "system", Detail: map[string]any{"executions": len(list)}})
	}
	return nil
}
