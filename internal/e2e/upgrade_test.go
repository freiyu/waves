//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildRelease builds the waves binary of an older tag in a throwaway worktree. The dashboard is not needed to test a
// database, so the embedded folder is a stub.
func buildRelease(t *testing.T, tag string) string {
	t.Helper()
	root, _ := filepath.Abs("../..")
	wt := filepath.Join(t.TempDir(), "old")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "--detach", wt, tag).CombinedOutput(); err != nil {
		t.Skipf("tag %s is not available here: %v\n%s", tag, err, out)
	}
	t.Cleanup(func() { exec.Command("git", "-C", root, "worktree", "remove", "--force", wt).Run() })
	os.MkdirAll(filepath.Join(wt, "web", "dist"), 0o700)
	os.WriteFile(filepath.Join(wt, "web", "dist", "index.html"), []byte("<!doctype html>"), 0o600)
	out := filepath.Join(t.TempDir(), "waves-old")
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", out, "./cmd/waves")
	cmd.Dir = wt
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", tag, err, b)
	}
	return out
}

// A database written by the 1.0.0 binary (with a project, an approved workflow and a task being worked on) opens with
// the new binary: it is backed up, migrated, recovered the old way, and the new features work on it.
func TestUpgradeFrom100(t *testing.T) {
	old := buildRelease(t, "phase-5-approved")
	current := bin
	bin = old
	restore := func() { bin = current }
	defer restore()

	i := newInst(t) // set up and started by the 1.0.0 binary
	p, a := i.planned("upgrade")
	task, _ := a.claim()
	if v := i.count(`SELECT max(version) FROM schema_migrations`); v != 4 {
		t.Fatalf("the 1.0.0 binary left schema version %d, want 4", v)
	}
	a.kill()
	i.kill9()

	// An upgrade replaces the binary where it is: the hook that 1.0.0 wrote names this path.
	nb, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old+".new", nb, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(old+".new", old); err != nil {
		t.Fatal(err)
	}
	i.start() // the new binary on the old database
	if v := i.count(`SELECT max(version) FROM schema_migrations`); v != 5 {
		t.Fatalf("after the upgrade the schema version is %d, want 5", v)
	}
	if m, _ := filepath.Glob(i.dbPath() + ".pre-v5-*.bak"); len(m) != 1 {
		t.Fatalf("backups before the migration: %v", m)
	}
	// Nothing was lost, and the interrupted work is handled the way it always was: blocked for the human, never rerun.
	if got := i.taskState(task); got != "Blocked" {
		t.Fatalf("the task that was being worked on is %s after the restart, want Blocked", got)
	}
	if n := i.count(`SELECT count(*) FROM executions WHERE task_id = ?`, task); n != 1 {
		t.Fatalf("%d executions: something started on its own", n)
	}
	var st struct {
		Audit struct {
			State string `json:"state"`
		} `json:"audit"`
	}
	i.operator().must("GET", "/status", nil, &st)
	if st.Audit.State != "verified" {
		t.Fatalf("the audit chain after the upgrade: %q", st.Audit.State)
	}
	// The connection made by 1.0.0 still guards, and says the new hooks are missing.
	var g struct {
		Installed    bool `json:"installed"`
		WaitingHooks bool `json:"waiting_hooks"`
	}
	p.dash.must("GET", "/projects/"+p.id+"/guard", nil, &g)
	if !g.Installed || g.WaitingHooks {
		t.Fatalf("a 1.0.0 connection: %+v", g)
	}
	if code, _ := i.runHook(p.installedHook(t), writeEvent("sess-old", p.dir, filepath.Join(p.dir, "x.txt"), "toolu_up")); code != 0 {
		t.Fatalf("the 1.0.0 guard hook under the new binary: exit %d", code)
	}
	// The new routes work on the migrated database.
	p.dash.must("PUT", "/graph-layout", map[string]any{"nodes": map[string]any{"phs_x": map[string]float64{"x": 1, "y": 2}}, "lines": map[string]any{}}, nil)
	var l struct {
		Nodes map[string]any `json:"nodes"`
	}
	p.dash.must("GET", "/graph-layout", nil, &l)
	if len(l.Nodes) != 1 {
		t.Fatalf("graph layout: %v", l)
	}
	if out, _ := i.run("", "version"); !strings.Contains(out, "waves") {
		t.Fatalf("version: %q", out)
	}
}
