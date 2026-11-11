package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"waves/internal/api/contract"
	"waves/internal/store"
)

const contextTemplate = "v1"

const leaseRules = `Work only on this task, inside the project folder. Heartbeat at the given interval.
Use waves_request_approval before a risky action (deleting data, changing CI, touching secrets) and wait for the decision.
If a heartbeat shows cancel_requested, stop and call waves_release_task. If your risky action is rejected, do not do it; release the task or report a blocker.
A read_only task must not write files. Writes to .git, the Claude settings and .mcp.json are refused.
Finish with waves_submit_result, or give the task back with waves_release_task. You cannot approve anything; a human does.`

// TaskContext builds, stores and returns the context package of a leased
// task. The exact delivered text is kept for audit (GET /executions/{id}/context).
func (s *Service) TaskContext(ctx context.Context, p Principal, mcpSession, token string) (contract.ContextPackage, error) {
	l, err := s.lease(ctx, s.db.R, p, mcpSession, token)
	if err != nil {
		return contract.ContextPackage{}, err
	}
	var key, title, desc, acc, mode, phKey, phTitle, objective, del, root string
	err = s.db.R.QueryRowContext(ctx, `SELECT t.key, t.title, t.description, t.acceptance_json, t.workspace_mode, ph.key, ph.title, ph.objective, ph.deliverables_json, pr.path
		FROM tasks t JOIN phases ph ON ph.id = t.phase_id JOIN projects pr ON pr.id = ? WHERE t.id = ?`, l.project, l.task).
		Scan(&key, &title, &desc, &acc, &mode, &phKey, &phTitle, &objective, &del, &root)
	if err != nil {
		return contract.ContextPackage{}, wrap(err, "read task")
	}
	var accList, delList []string
	json.Unmarshal([]byte(acc), &accList)
	json.Unmarshal([]byte(del), &delList)
	bullets := func(xs []string) string {
		if len(xs) == 0 {
			return "(none)"
		}
		return "- " + strings.Join(xs, "\n- ")
	}
	sections := []contract.ContextSection{
		{Title: "Task", Content: fmt.Sprintf("%s %s\nMode: %s\n\n%s", strings.ToUpper(key), title, mode, strings.TrimSpace(desc))},
		{Title: "Acceptance criteria", Content: bullets(accList)},
		{Title: "Phase", Content: fmt.Sprintf("%s %s\nObjective: %s\nDeliverables:\n%s", strings.ToUpper(phKey), phTitle, objective, bullets(delList))},
	}
	// What the human asked to change when rejecting a result of this task or
	// the review of its phase: without it a retry repeats the same work.
	fb, err := s.db.R.QueryContext(ctx, `SELECT a.category, a.decided_at, a.feedback, COALESCE(a.decided_by_scope,'') FROM approvals a
		WHERE a.state = 'rejected' AND COALESCE(a.feedback,'') <> '' AND (
		  (a.subject_type = 'task' AND a.subject_id = ?) OR
		  (a.subject_type = 'execution' AND a.subject_id IN (SELECT id FROM executions WHERE task_id = ?)) OR
		  (a.subject_type = 'phase' AND a.category = 'phase_completion' AND a.subject_id = (SELECT phase_id FROM tasks WHERE id = ?)))
		ORDER BY a.decided_at`, l.task, l.task, l.task)
	if err != nil {
		return contract.ContextPackage{}, wrap(err, "read review feedback")
	}
	var notes []string
	for fb.Next() {
		var cat, at, text, scope string
		if err := fb.Scan(&cat, &at, &text, &scope); err != nil {
			fb.Close()
			return contract.ContextPackage{}, wrap(err, "read review feedback")
		}
		// Only a passphrase-confirmed decision speaks for the human; an operator
		// rejection (no step-up) could come from anything holding the operator token.
		by := "by the human (passphrase confirmed)"
		if scope != "human" {
			by = "with the operator credential (not passphrase confirmed)"
		}
		notes = append(notes, fmt.Sprintf("%s rejected %s (%s): %s", CategoryLabel[cat], by, at, truncate(text, 2000)))
	}
	fb.Close()
	if len(notes) > 0 {
		sections = append(sections, contract.ContextSection{Title: "Changes requested at review", Content: bullets(notes) + "\nOldest first. Address every point that still applies; the work already in the workspace is what was rejected."})
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT d.key, d.title, COALESCE((SELECT json_extract(e.result_json,'$.summary') FROM executions e WHERE e.task_id = d.id AND e.state = 'Completed' ORDER BY e.created_at DESC LIMIT 1),'')
		FROM task_dependencies td JOIN tasks d ON d.id = td.depends_on_task_id WHERE td.task_id = ?`, l.task)
	if err == nil {
		var deps []string
		for rows.Next() {
			var k, t, sum string
			rows.Scan(&k, &t, &sum)
			deps = append(deps, fmt.Sprintf("%s %s: %s", strings.ToUpper(k), t, truncate(sum, 400)))
		}
		rows.Close()
		if len(deps) > 0 {
			sections = append(sections, contract.ContextSection{Title: "Finished dependencies", Content: bullets(deps)})
		}
	}
	budget := 0
	s.db.R.QueryRowContext(ctx, `SELECT context_budget FROM project_settings WHERE project_id = ?`, l.project).Scan(&budget)
	if budget <= 0 {
		budget = 24000
	}
	var req []string
	rr, err := s.db.R.QueryContext(ctx, `SELECT kind, COALESCE(path,''), COALESCE(body,'') FROM requirements WHERE project_id = ? ORDER BY created_at`, l.project)
	if err == nil {
		left := budget / 2
		for rr.Next() {
			var kind, path, body string
			rr.Scan(&kind, &path, &body)
			if kind == "file" {
				if real, err := InsideRoot(root, path); err == nil {
					if b, err := readHead(real, max(left, 0)); err == nil {
						body = string(b)
					}
				}
				body = path + "\n" + body
			}
			if len(body) > left {
				body = body[:max(left, 0)]
			}
			left -= len(body)
			req = append(req, body)
		}
		rr.Close()
	}
	if len(req) > 0 {
		sections = append(sections, contract.ContextSection{Title: "Requirements (excerpts)", Content: strings.Join(req, "\n\n---\n\n")})
	}
	sections = append(sections, contract.ContextSection{Title: "Rules", Content: leaseRules})
	b, _ := json.Marshal(sections)
	sum := sha256.Sum256(b)
	pkg := contract.ContextPackage{GeneratedAt: s.now(), SizeBytes: len(b), Sections: sections}
	path := filepath.Join(s.dirs.Artifacts(), l.execution+".context.json")
	if err := os.MkdirAll(s.dirs.Artifacts(), 0o700); err != nil {
		return pkg, wrap(err, "create artifacts dir")
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return pkg, wrap(err, "store context package")
	}
	err = s.tx(ctx, func(t *Tx) error {
		_, err := t.ExecContext(ctx, `INSERT INTO context_packages(id, execution_id, template_version, content_hash, content_path, size_bytes) VALUES (?,?,?,?,?,?)
			ON CONFLICT(execution_id) DO UPDATE SET content_hash = excluded.content_hash, size_bytes = excluded.size_bytes`, NewID("ctx"), l.execution, contextTemplate, hex.EncodeToString(sum[:]), path, len(b))
		if err != nil {
			return wrap(err, "record context package")
		}
		return t.audit(ctx, store.AuditRecord{Event: "context.delivered", ActorScope: "agent", ActorRef: p.ActorRef(), ProjectID: l.project, Subject: l.execution, Detail: map[string]any{"bytes": len(b), "sha256": hex.EncodeToString(sum[:])}})
	})
	return pkg, err
}

// DeliveredContext returns the stored context package of an execution.
func (s *Service) DeliveredContext(ctx context.Context, execution string) (contract.ContextPackage, error) {
	var path string
	if err := s.db.R.QueryRowContext(ctx, `SELECT content_path FROM context_packages WHERE execution_id = ?`, execution).Scan(&path); err != nil {
		return contract.ContextPackage{}, errNotFound("context package", "Context is built when the agent asks for it after claiming the task.")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return contract.ContextPackage{}, wrap(err, "read context package")
	}
	pkg := contract.ContextPackage{SizeBytes: len(b), Sections: []contract.ContextSection{}}
	json.Unmarshal(b, &pkg.Sections)
	if st, err := os.Stat(path); err == nil {
		pkg.GeneratedAt = st.ModTime().UTC()
	}
	return pkg, nil
}
