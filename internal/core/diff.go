package core

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"waves/internal/api/contract"
)

// captureDiff stores the changes of an execution since its start commit as
// a unified diff file (tracked changes plus new untracked files) and returns
// its path and the number of changed files. Non-Git projects have no diff.
func (s *Service) captureDiff(ctx context.Context, execution string) (string, int) {
	var root string
	var start sql.NullString
	if s.db.R.QueryRowContext(ctx, `SELECT w.path, e.start_commit FROM executions e JOIN workspaces w ON w.id = e.workspace_id WHERE e.id = ?`, execution).Scan(&root, &start) != nil || !start.Valid {
		return "", 0
	}
	if err := os.MkdirAll(s.dirs.Artifacts(), 0o700); err != nil {
		return "", 0
	}
	path := filepath.Join(s.dirs.Artifacts(), execution+".diff")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", 0
	}
	defer f.Close()
	files := map[string]bool{}
	g := func(args ...string) *exec.Cmd { return git(ctx, root, args...) }
	if fl := gitFilters(ctx, root); len(fl) > 0 {
		fmt.Fprintf(f, "Waves did not compute this diff: the repository configures a git filter (%s), which git would run. Review the changes by hand.\n", strings.Join(fl, ", "))
		return path, -1
	}
	if out, err := g("diff", "--name-only", start.String).Output(); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if l != "" {
				files[l] = true
			}
		}
	}
	// --text: attributes such as -diff must not turn changes into "Binary files differ".
	d := g("diff", "--no-color", "--text", start.String)
	d.Stdout = f
	d.Run()
	if list, err := untrackedFiles(ctx, root); err == nil {
		for _, l := range list {
			files[l] = true
			n := g("diff", "--no-color", "--text", "--no-index", "--", "/dev/null", l)
			n.Stdout = f
			n.Run() // exit 1 means "differs"
		}
	}
	return path, len(files)
}

// diffStats counts added and removed lines in a stored diff.
func diffStats(path string) (files, added, removed int, names []string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		b := sc.Bytes()
		switch {
		case bytes.HasPrefix(b, []byte("+++ ")):
			if n := strings.TrimPrefix(string(b[4:]), "b/"); n != "/dev/null" {
				names = append(names, n)
			}
			files++
		case bytes.HasPrefix(b, []byte("--- ")):
		case len(b) > 0 && b[0] == '+':
			added++
		case len(b) > 0 && b[0] == '-':
			removed++
		}
	}
	return
}

// phaseDiff summarizes the stored diffs of a phase's executions and flags
// changes in the workspace no execution of this phase accounts for.
func (s *Service) phaseDiff(ctx context.Context, phaseID string, tasks []contract.ReviewTask) (contract.DiffSummary, []contract.Finding) {
	sum := contract.DiffSummary{}
	findings := []contract.Finding{}
	rows, err := s.db.R.QueryContext(ctx, `SELECT e.diff_path, t.key FROM executions e JOIN tasks t ON t.id = e.task_id WHERE t.phase_id = ? AND e.state = 'Completed'`, phaseID)
	if err != nil {
		return sum, findings
	}
	seen := map[string]bool{}
	perTask := map[string]int{}
	for rows.Next() {
		var p sql.NullString
		var key string
		rows.Scan(&p, &key)
		sum.Executions++
		if !p.Valid {
			continue
		}
		f, a, r, names := diffStats(p.String)
		sum.Added += a
		sum.Removed += r
		perTask[strings.ToUpper(key)] += f
		for _, n := range names {
			seen[n] = true
		}
	}
	rows.Close()
	sum.Files = len(seen)
	for i := range tasks {
		tasks[i].FilesChanged = perTask[tasks[i].Code]
	}
	// Unattributed: files that differ from the newest start commit of the phase but no execution diff names.
	var root string
	var start sql.NullString
	s.db.R.QueryRowContext(ctx, `SELECT w.path, (SELECT e.start_commit FROM executions e JOIN tasks t ON t.id = e.task_id WHERE t.phase_id = ? ORDER BY e.created_at, e.rowid LIMIT 1)
		FROM phases p JOIN workflow_versions v ON v.id = p.workflow_version_id JOIN workflows wf ON wf.id = v.workflow_id JOIN workspaces w ON w.project_id = wf.project_id WHERE p.id = ?`, phaseID, phaseID).Scan(&root, &start)
	if fl := gitFilters(ctx, root); start.Valid && len(fl) > 0 {
		findings = append(findings, contract.Finding{Kind: "unattributed", Path: ".git/config", Detail: "The repository configures a git filter (" + strings.Join(fl, ", ") + "); Waves did not compare the work tree. Review the changes by hand."})
	} else if start.Valid {
		out, err := git(ctx, root, "diff", "--name-only", start.String).Output()
		if err == nil {
			for _, n := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if n != "" && !seen[n] {
					findings = append(findings, contract.Finding{Kind: "unattributed", Path: n, Detail: "Changed in the workspace, but no execution of this phase reported it."})
				}
			}
		}
	}
	return sum, findings
}
