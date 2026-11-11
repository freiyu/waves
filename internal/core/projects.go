package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"waves/internal/store"
)

// RegisterProject registers an existing folder. The path is resolved with
// symlinks; nothing in the folder is created or deleted.
func (s *Service) RegisterProject(ctx context.Context, p Principal, path, name string) (string, error) {
	if !p.IsOperator() {
		return "", errWrongScope
	}
	if strings.HasPrefix(path, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(h, path[2:])
		}
	}
	if !filepath.IsAbs(path) {
		return "", errValidation("The path must be absolute.", "Use a path such as ~/work/my-project.")
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", errValidation("The folder does not exist.", "Check the path.")
	}
	st, err := os.Stat(real)
	if err != nil || !st.IsDir() {
		return "", errValidation("The path is not a folder.", "Register the project folder, not a file.")
	}
	if name == "" {
		name = filepath.Base(real)
	}
	isGit, branch := gitInfo(ctx, real)
	id := NewID("prj")
	var conflict bool
	err = s.tx(ctx, func(t *Tx) error {
		var n int
		t.QueryRowContext(ctx, `SELECT count(*) FROM projects WHERE path = ? AND removed_at IS NULL`, real).Scan(&n)
		if n > 0 {
			conflict = true
			return nil
		}
		// Nested roots would let a lease of the outer project write the inner project's guard files.
		var nested string
		t.QueryRowContext(ctx, `SELECT path FROM projects WHERE removed_at IS NULL AND (? LIKE path || '/%' OR path LIKE ? || '/%') LIMIT 1`, real, real).Scan(&nested)
		if nested != "" {
			return errValidation("Projects cannot be nested; "+nested+" is already registered.", "Register the outer or the inner folder, not both.")
		}
		// A removed project registered again comes back under a new id; its path must stay unique.
		if _, err := t.ExecContext(ctx, `UPDATE projects SET path = path || '#removed-' || id WHERE path = ? AND removed_at IS NOT NULL`, real); err != nil {
			return wrap(err, "release removed path")
		}
		var active int
		t.QueryRowContext(ctx, `SELECT count(*) FROM projects WHERE is_active = 1`).Scan(&active)
		if _, err := t.ExecContext(ctx, `INSERT INTO projects(id, name, path, is_git, base_branch, is_active, created_at) VALUES (?,?,?,?,?,?,?)`,
			id, name, real, b2i(isGit), store.NullStr(branch), b2i(active == 0), s.ts()); err != nil {
			return wrap(err, "store project")
		}
		if _, err := t.ExecContext(ctx, `INSERT INTO project_settings(project_id, updated_at) VALUES (?,?)`, id, s.ts()); err != nil {
			return wrap(err, "store project settings")
		}
		if _, err := t.ExecContext(ctx, `INSERT INTO workspaces(id, project_id, path, mode) VALUES (?,?,?,'in_place')`, NewID("wsp"), id, real); err != nil {
			return wrap(err, "store workspace")
		}
		t.publish("project.changed", map[string]string{"project_id": id})
		return t.audit(ctx, store.AuditRecord{Event: "project.registered", ActorScope: "operator", ActorRef: p.ActorRef(), ProjectID: id, Subject: name, Detail: map[string]any{"path": real, "git": isGit}})
	})
	if err != nil {
		return "", err
	}
	if conflict {
		return "", fail(http.StatusConflict, "conflict", "This folder is already registered.", "Open it from the Projects list.")
	}
	return id, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// gitInfo reports whether dir is a Git work tree and its current branch.
func gitInfo(ctx context.Context, dir string) (bool, string) {
	out, err := git(ctx, dir, "rev-parse", "--is-inside-work-tree").Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return false, ""
	}
	b, err := git(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD").Output()
	if err != nil {
		return true, ""
	}
	return true, strings.TrimSpace(string(b))
}

// ActivateProject sets the Active Context. Executions of other projects continue.
func (s *Service) ActivateProject(ctx context.Context, p Principal, id string) error {
	if !p.IsOperator() {
		return errWrongScope
	}
	return s.tx(ctx, func(t *Tx) error {
		if err := s.mustProject(ctx, t, id); err != nil {
			return err
		}
		if _, err := t.ExecContext(ctx, `UPDATE projects SET is_active = 0 WHERE is_active = 1 AND id <> ?`, id); err != nil {
			return wrap(err, "clear active context")
		}
		if _, err := t.ExecContext(ctx, `UPDATE projects SET is_active = 1 WHERE id = ?`, id); err != nil {
			return wrap(err, "set active context")
		}
		t.publish("project.changed", map[string]string{"project_id": id})
		return t.audit(ctx, store.AuditRecord{Event: "project.activated", ActorScope: "operator", ActorRef: p.ActorRef(), ProjectID: id})
	})
}

// RemoveProject unregisters a project. Files are never touched. Refused
// while an execution of the project still holds its workspace.
func (s *Service) RemoveProject(ctx context.Context, p Principal, id string) error {
	if !p.IsOperator() {
		return errWrongScope
	}
	return s.tx(ctx, func(t *Tx) error {
		if err := s.mustProject(ctx, t, id); err != nil {
			return err
		}
		var n int
		t.QueryRowContext(ctx, `SELECT count(*) FROM executions WHERE project_id = ? AND state NOT IN ('Completed','Failed','Cancelled')`, id).Scan(&n)
		if n > 0 {
			return errInvalidState("An execution of this project is still open.", "Cancel or resolve it first.")
		}
		if _, err := t.ExecContext(ctx, `UPDATE projects SET removed_at = ?, is_active = 0 WHERE id = ?`, s.ts(), id); err != nil {
			return wrap(err, "remove project")
		}
		if _, err := t.ExecContext(ctx, `UPDATE credentials SET revoked_at = ? WHERE project_id = ? AND revoked_at IS NULL`, s.ts(), id); err != nil {
			return wrap(err, "revoke project credentials")
		}
		t.publish("project.changed", map[string]string{"project_id": id})
		return t.audit(ctx, store.AuditRecord{Event: "project.removed", ActorScope: "operator", ActorRef: p.ActorRef(), ProjectID: id, Detail: map[string]any{"files": "untouched"}})
	})
}

func (s *Service) mustProject(ctx context.Context, q store.Execer, id string) error {
	var n int
	q.QueryRowContext(ctx, `SELECT count(*) FROM projects WHERE id = ? AND removed_at IS NULL`, id).Scan(&n)
	if n == 0 {
		return errNotFound("project", "Open the Projects list.")
	}
	return nil
}

// projectPath returns the registered root of a project.
func (s *Service) projectPath(ctx context.Context, q store.Execer, id string) (string, error) {
	var p string
	err := q.QueryRowContext(ctx, `SELECT path FROM projects WHERE id = ? AND removed_at IS NULL`, id).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotFound("project", "Open the Projects list.")
	}
	return p, wrap(err, "read project")
}

// InsideRoot resolves path (relative paths are taken from root) with
// symlinks, and checks that it stays inside root. For a path that does not
// exist yet, its nearest existing parent is resolved.
func InsideRoot(root, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	rest := ""
	cur := path
	for {
		real, err := filepath.EvalSymlinks(cur)
		if err == nil {
			path = filepath.Join(real, rest)
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		// A dangling symlink would let a write create its target anywhere.
		if _, lerr := os.Lstat(cur); lerr == nil {
			return "", fmt.Errorf("%s is a symbolic link to a missing target", cur)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("no existing parent for %s", path)
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%s is outside %s", path, root)
	}
	return path, nil
}

// fileHash streams a file through SHA-256.
func fileHash(path string) (string, error) {
	// FIFOs, devices and symlinks to them would block or never end.
	if st, err := os.Lstat(path); err != nil || !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// AddRequirement adds a file reference (path and hash) or inline text.
func (s *Service) AddRequirement(ctx context.Context, p Principal, projectID, kind, path, text string) (string, error) {
	if !p.IsOperator() {
		return "", errWrongScope
	}
	root, err := s.projectPath(ctx, s.db.R, projectID)
	if err != nil {
		return "", err
	}
	id := NewID("req")
	var hash string
	switch kind {
	case "file":
		if strings.TrimSpace(path) == "" {
			return "", errValidation("A file requirement needs a path.", "Enter the path of the file.")
		}
		real, err := InsideRoot(root, path)
		if err != nil {
			return "", errValidation("The file must be inside the project folder.", "Use a path inside "+root+".")
		}
		if hash, err = fileHash(real); err != nil {
			return "", errValidation("The file cannot be read.", "Check that it exists.")
		}
		path = real
	case "text":
		if strings.TrimSpace(text) == "" {
			return "", errValidation("A text requirement cannot be empty.", "Write the requirement.")
		}
		if len(text) > 64<<10 {
			return "", fail(http.StatusUnprocessableEntity, "limit_exceeded", "Inline text is limited to 64 KiB.", "Reference a file instead.")
		}
	default:
		return "", errValidation("The kind must be file or text.", "")
	}
	err = s.tx(ctx, func(t *Tx) error {
		dbKind := map[string]string{"file": "file", "text": "inline"}[kind]
		if _, err := t.ExecContext(ctx, `INSERT INTO requirements(id, project_id, kind, path, content_hash, body, created_at) VALUES (?,?,?,?,?,?,?)`,
			id, projectID, dbKind, store.NullStr(path), store.NullStr(hash), store.NullStr(text), s.ts()); err != nil {
			return wrap(err, "store requirement")
		}
		t.publish("project.changed", map[string]string{"project_id": projectID})
		return t.audit(ctx, store.AuditRecord{Event: "requirements.added", ActorScope: "operator", ActorRef: p.ActorRef(), ProjectID: projectID, Subject: id, Detail: map[string]any{"kind": kind, "path": path, "sha256": hash}})
	})
	return id, err
}
