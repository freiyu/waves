package core

import (
	"context"
	"strings"
	"time"

	"waves/internal/store"
)

// Attention is one needs-attention item.
type Attention struct {
	Kind        string // approval, execution_failed, execution_disconnected, task_blocked, audit_chain, stepup_failures, passphrase_reset, database_restored, bypass_attempt
	Label       string
	Subject     string
	ProjectID   string
	ProjectName string
	Since       time.Time
	RefType     string // approval, execution, audit
	RefID       string
}

// CategoryLabel is the human name of an approval category.
var CategoryLabel = map[string]string{
	"workflow":         "Workflow",
	"phase_start":      "Phase Start",
	"phase_completion": "Phase Completion",
	"task_execution":   "Task Execution",
	"risky_action":     "Risky Action",
	"result":           "Result",
}

// NeedsAttention lists what waits for the human, newest problems first:
// a broken audit chain, pending approvals, failed and disconnected executions.
func (s *Service) NeedsAttention(ctx context.Context) ([]Attention, error) {
	out := []Attention{}
	if st := s.AuditStatus(ctx); st.State == "broken" {
		out = append(out, Attention{Kind: "audit_chain", Label: "Audit chain broken", Subject: "Verification failed at record " + itoa(st.BrokenAt),
			Since: st.CheckedAt, RefType: "audit", RefID: "chain"})
	}
	// Security alerts stay listed until a human acknowledges them on the Activity page.
	var ackSeq int64
	s.db.R.QueryRowContext(ctx, `SELECT COALESCE(max(seq),0) FROM audit WHERE event = 'audit.acknowledged'`).Scan(&ackSeq)
	var n int
	var last string
	s.db.R.QueryRowContext(ctx, `SELECT count(*), COALESCE(max(ts),'') FROM audit WHERE event = 'stepup.failed' AND seq > ?
		AND seq > COALESCE((SELECT max(seq) FROM audit WHERE event = 'stepup.succeeded'), 0)`, ackSeq).Scan(&n, &last)
	if n >= stepupAlertAt {
		out = append(out, Attention{Kind: "stepup_failures", Label: "Wrong passphrases", Subject: itoa(int64(n)) + " failed step-ups in a row", Since: store.ParseTime(last), RefType: "audit", RefID: "stepup"})
	}
	// A reset needs no old passphrase, so it stays listed until a human acknowledges it.
	s.db.R.QueryRowContext(ctx, `SELECT count(*), COALESCE(max(ts),'') FROM audit WHERE event = 'passphrase.reset' AND seq > ?`, ackSeq).Scan(&n, &last)
	if n > 0 {
		out = append(out, Attention{Kind: "passphrase_reset", Label: "Passphrase was reset", Subject: "Reset from a terminal without the old passphrase. If it was not you, treat approvals since then as untrusted.", Since: store.ParseTime(last), RefType: "audit", RefID: "reset"})
	}
	// A restore brings the backup's passphrase, approvals and audit log with it.
	s.db.R.QueryRowContext(ctx, `SELECT count(*), COALESCE(max(ts),'') FROM audit WHERE event = 'db.restored' AND seq > ?`, ackSeq).Scan(&n, &last)
	if n > 0 {
		out = append(out, Attention{Kind: "database_restored", Label: "Database was restored from a backup", Subject: "Approvals, tokens and the audit log are now those of the backup file. If you did not restore it yourself, treat approvals since then as untrusted.", Since: store.ParseTime(last), RefType: "audit", RefID: "restore"})
	}
	s.db.R.QueryRowContext(ctx, `SELECT count(*), COALESCE(max(ts),'') FROM audit WHERE event = 'auth.bypass_attempt' AND seq > ?`, ackSeq).Scan(&n, &last)
	if n > 0 {
		out = append(out, Attention{Kind: "bypass_attempt", Label: "Bypass attempt", Subject: itoa(int64(n)) + " requests with an agent or guard credential on a human route", Since: store.ParseTime(last), RefType: "audit", RefID: "bypass"})
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT a.id, a.category, a.project_id, p.name, a.requested_at, `+approvalSubjectSQL+`
		FROM approvals a JOIN projects p ON p.id = a.project_id WHERE a.state = 'pending' ORDER BY a.requested_at`)
	if err != nil {
		return nil, wrap(err, "list pending approvals")
	}
	for rows.Next() {
		var a Attention
		var cat, at string
		if err := rows.Scan(&a.RefID, &cat, &a.ProjectID, &a.ProjectName, &at, &a.Subject); err != nil {
			rows.Close()
			return nil, wrap(err, "scan approval")
		}
		a.Kind, a.Label, a.RefType, a.Since = "approval", CategoryLabel[cat], "approval", store.ParseTime(at)
		out = append(out, a)
	}
	rows.Close()
	// The newest execution of each task, while the human has to act on it: it
	// lost its agent, or its task is Blocked (failure, interruption, or a
	// blocker the agent reported). A retried or resolved task drops off.
	rows, err = s.db.R.QueryContext(ctx, `SELECT e.id, e.state, t.state, COALESCE(t.blocked_reason,''), e.project_id, p.name, t.title,
		COALESCE((SELECT max(ts) FROM state_transitions st WHERE st.entity_type IN ('execution','task') AND st.entity_id IN (e.id, t.id)), e.created_at)
		FROM executions e JOIN projects p ON p.id = e.project_id JOIN tasks t ON t.id = e.task_id
		WHERE e.rowid = (SELECT max(x.rowid) FROM executions x WHERE x.task_id = e.task_id)
		  AND (e.state = 'Disconnected' OR (t.state = 'Blocked' AND e.state IN ('Failed','Interrupted','Running','Waiting Approval','Paused','Completed')))
		ORDER BY e.created_at`)
	if err != nil {
		return nil, wrap(err, "list failed executions")
	}
	defer rows.Close()
	for rows.Next() {
		var a Attention
		var state, task, reason, title, at string
		if err := rows.Scan(&a.RefID, &state, &task, &reason, &a.ProjectID, &a.ProjectName, &title, &at); err != nil {
			return nil, wrap(err, "scan execution")
		}
		switch state {
		case "Failed":
			a.Kind, a.Label = "execution_failed", "Failed"
		case "Disconnected", "Interrupted":
			a.Kind, a.Label = "execution_disconnected", state
		default:
			a.Kind, a.Label = "task_blocked", "Blocked · "+blockedLabel(reason)
		}
		a.Subject, a.RefType, a.Since = a.RefID+" "+title, "execution", store.ParseTime(at)
		out = append(out, a)
	}
	return out, rows.Err()
}

// approvalSubjectSQL renders an approval's subject (phase or task title,
// or the workflow version number) for lists.
const approvalSubjectSQL = `CASE a.subject_type
	WHEN 'phase' THEN (SELECT ph.key || ' ' || ph.title FROM phases ph WHERE ph.id = a.subject_id)
	WHEN 'task' THEN (SELECT tk.key || ' ' || tk.title FROM tasks tk WHERE tk.id = a.subject_id)
	WHEN 'execution' THEN (SELECT tk.key || ' ' || tk.title FROM executions ex JOIN tasks tk ON tk.id = ex.task_id WHERE ex.id = a.subject_id)
	WHEN 'workflow_version' THEN (SELECT 'Workflow v' || wv.version_no FROM workflow_versions wv WHERE wv.id = a.subject_id)
	END`

// Counts are the execution counters on the overview.
type Counts struct{ Running, Queued, WaitingApproval, Failed, Disconnected int }

// ExecutionCounts counts executions by state.
func (s *Service) ExecutionCounts(ctx context.Context) (Counts, error) {
	var c Counts
	rows, err := s.db.R.QueryContext(ctx, `SELECT state, count(*) FROM executions GROUP BY state`)
	if err != nil {
		return c, wrap(err, "count executions")
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return c, wrap(err, "scan counts")
		}
		switch st {
		case "Running", "Starting":
			c.Running += n
		case "Queued":
			c.Queued += n
		case "Waiting Approval":
			c.WaitingApproval += n
		case "Failed":
			c.Failed += n
		case "Disconnected", "Interrupted":
			c.Disconnected += n
		}
	}
	return c, rows.Err()
}

// blockedLabel says why a task is blocked, for the needs-attention label.
func blockedLabel(reason string) string {
	switch {
	case strings.HasPrefix(reason, "blocker:"):
		return "agent reported a blocker"
	case reason == "recovery_required" || reason == "interrupted":
		return "recovery required"
	case reason == "released_with_changes":
		return "released with changes"
	case reason == "execution_failed":
		return "execution failed"
	}
	return "needs your decision"
}
