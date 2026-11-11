package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"waves/internal/store"
)

// NotDefended is shown by the dashboard and the CLI wherever the guard is.
const NotDefended = "The guard hook only sees tool calls of Claude Code sessions that run it. It does not stop other programs, shell commands that write files (Bash is allowed and recorded as unchecked), or anyone who removes the hook. It is not a sandbox. Waves records each execution's diff and flags changes no execution accounts for."

// HookEvent is the part of a Claude Code PreToolUse event the guard reads.
type HookEvent struct {
	SessionID string          `json:"session_id"`
	Cwd       string          `json:"cwd"`
	ToolName  string          `json:"tool_name"`
	ToolUseID string          `json:"tool_use_id"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// GuardDecision is the only thing the hook endpoint returns.
type GuardDecision struct {
	Decision string `json:"decision"` // allow | deny
	Reason   string `json:"reason_code"`
	Policy   string `json:"policy"`
}

var fileTools = map[string]bool{"Write": true, "Edit": true, "MultiEdit": true, "NotebookEdit": true}

// targetPath extracts the path a file tool writes.
func targetPath(tool string, in json.RawMessage) string {
	var v struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	json.Unmarshal(in, &v)
	if tool == "NotebookEdit" && v.NotebookPath != "" {
		return v.NotebookPath
	}
	return v.FilePath
}

// projectForCwd finds the registered project whose root contains cwd (after
// symlink resolution), preferring the deepest root.
func (s *Service) projectForCwd(ctx context.Context, cwd string) (id, root, policy, unmanaged string) {
	real, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT p.id, p.path, ps.guard_policy, ps.unmanaged_sessions FROM projects p JOIN project_settings ps ON ps.project_id = p.id WHERE p.removed_at IS NULL`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var i, r, pol, un string
		rows.Scan(&i, &r, &pol, &un)
		if rel, err := filepath.Rel(r, real); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && len(r) > len(root) {
			id, root, policy, unmanaged = i, r, pol, un
		}
	}
	return
}

// DecideGuard is the guard hook decision. In audit policy it always allows
// and records what enforce would have denied. It never returns secrets,
// other sessions or file contents.
func (s *Service) DecideGuard(ctx context.Context, p Principal, ev HookEvent) (GuardDecision, error) {
	if p.Kind != KindGuard {
		return GuardDecision{}, errWrongScope
	}
	// For file tools the target decides the project, so a session whose cwd is
	// elsewhere still meets the guard of the project it writes into.
	where := ev.Cwd
	if t := targetPath(ev.ToolName, ev.ToolInput); fileTools[ev.ToolName] && t != "" {
		if !filepath.IsAbs(t) {
			t = filepath.Join(ev.Cwd, t)
		}
		if id, _, _, _ := s.projectForCwd(ctx, nearestExisting(t)); id != "" {
			where = nearestExisting(t)
		}
	}
	project, root, policy, unmanaged := s.projectForCwd(ctx, where)
	if project == "" {
		return GuardDecision{Decision: "allow", Reason: "not_a_project", Policy: "off"}, nil
	}
	if policy == "off" {
		return GuardDecision{Decision: "allow", Reason: "policy_off", Policy: policy}, nil
	}
	deny, reason, lease, target := s.wouldDeny(ctx, ev, project, root, unmanaged)
	d := GuardDecision{Decision: "allow", Reason: reason, Policy: policy}
	if deny && policy == "enforce" {
		d.Decision = "deny"
	}
	// shortcut: one insert per decision instead of a batched flush; batch if the hook p95 grows past a few ms.
	err := s.tx(ctx, func(t *Tx) error {
		if strings.HasPrefix(ev.ToolName, "mcp__waves__") && ev.ToolUseID != "" && ev.SessionID != "" {
			if err := s.bindToolUse(ctx, t, ev.ToolUseID, ev.SessionID, ""); err != nil {
				return err
			}
		}
		if _, err := t.ExecContext(ctx, `INSERT INTO guard_decisions(ts, project_id, session_id, lease_id, tool_name, tool_use_id, target_path, policy, decision, would_deny, reason) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			s.ts(), project, ev.SessionID, store.NullStr(lease), truncate(ev.ToolName, 200), ev.ToolUseID, store.NullStr(truncate(target, 1000)), policy, d.Decision, b2i(deny), reason); err != nil {
			return wrap(err, "record guard decision")
		}
		if deny {
			t.publish("guard.denial", map[string]any{"project_id": project, "would_deny": b2i(policy == "audit")})
			return t.audit(ctx, store.AuditRecord{Event: "guard.decision", ActorScope: "guard", ProjectID: project, Subject: ev.SessionID,
				Detail: map[string]any{"tool": ev.ToolName, "decision": d.Decision, "would_deny": policy == "audit", "reason": reason, "target": truncate(target, 300)}})
		}
		return nil
	})
	return d, err
}

// wouldDeny is the enforce rule: file tools need an active lease of this
// project for the session and a target inside its workspace.
func (s *Service) wouldDeny(ctx context.Context, ev HookEvent, project, root, unmanaged string) (bool, string, string, string) {
	if strings.HasPrefix(ev.ToolName, "mcp__waves__") {
		return false, "waves_tool", "", ""
	}
	var lease, mode string
	err := s.db.R.QueryRowContext(ctx, `SELECT l.id, t.workspace_mode FROM leases l JOIN tasks t ON t.id = l.task_id WHERE l.session_id = ? AND l.project_id = ? AND l.revoked_at IS NULL AND l.expires_at > ?`,
		ev.SessionID, project, s.ts()).Scan(&lease, &mode)
	if err == sql.ErrNoRows {
		var ever int
		s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM leases WHERE session_id = ?`, ev.SessionID).Scan(&ever)
		if ever > 0 {
			return fileTools[ev.ToolName] || ev.ToolName == "Bash", "lease_ended", "", targetPath(ev.ToolName, ev.ToolInput)
		}
		return unmanaged == "deny" && (fileTools[ev.ToolName] || ev.ToolName == "Bash"), "unmanaged", "", targetPath(ev.ToolName, ev.ToolInput)
	}
	if err != nil {
		return true, "error", "", ""
	}
	if ev.ToolName == "Bash" {
		return false, "bash_unchecked", lease, ""
	}
	if !fileTools[ev.ToolName] {
		return false, "not_a_file_tool", lease, ""
	}
	target := targetPath(ev.ToolName, ev.ToolInput)
	if mode == "read_only" {
		return true, "read_only_task", lease, target
	}
	if target == "" {
		return true, "no_target", lease, target
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(ev.Cwd, target)
	}
	real, err := InsideRoot(root, target)
	if err != nil {
		return true, "outside_workspace", lease, target
	}
	rel, _ := filepath.Rel(root, real)
	first := strings.SplitN(rel, string(filepath.Separator), 2)[0]
	if first == ".git" || first == ".waves" || strings.HasPrefix(real, s.dirs.Config) || strings.HasPrefix(real, s.dirs.Data) || strings.HasPrefix(real, s.dirs.State) {
		return true, "protected_path", lease, real
	}
	// The hook and MCP entries live here; an agent must not rewrite its own guard.
	if rel == filepath.Join(".claude", "settings.local.json") || rel == filepath.Join(".claude", "settings.json") || rel == ".mcp.json" {
		return true, "protected_path", lease, real
	}
	return false, "leased", lease, real
}

// nearestExisting is path or its closest existing parent.
func nearestExisting(path string) string {
	p := filepath.Clean(path)
	for {
		if _, err := os.Lstat(p); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}
