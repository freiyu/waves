//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"waves/internal/api/contract"
)

// buildRelease builds the waves binary of an older tag in a throwaway worktree. The dashboard is not needed to test a
// database, so the embedded folder is a stub.
func buildRelease(t *testing.T, tag string) string {
	t.Helper()
	return buildReleaseAs(t, tag, "")
}

// buildReleaseAs is buildRelease with the version the binary reports (empty keeps the tag's own default).
func buildReleaseAs(t *testing.T, tag, version string) string {
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
	flags := "-s -w"
	if version != "" {
		flags += " -X main.version=" + version
	}
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", flags, "-o", out, "./cmd/waves")
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

// A database written by the 1.0.1 binary holds a task waiting in review with a result approval, and a task being worked on. The
// 1.2 binary opens it without a migration: the approval is still decided by a human with the passphrase, the work that was
// running is blocked for the human and not rerun, and the 1.0.1 binary opens the same database again afterwards.
func TestUpgradeFrom101AndBack(t *testing.T) {
	old := buildRelease(t, "v1.01-stage-7-approved")
	current := bin
	bin = old
	defer func() { bin = current }()
	oldBytes, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}

	i := newInst(t) // set up and started by the 1.0.1 binary
	i.setLimit(3)
	rp, ra := i.planned("review")
	rtask, _ := ra.claim()
	os.WriteFile(filepath.Join(rp.dir, "hello.txt"), []byte("hello\n"), 0o600)
	ra.must("waves_submit_result", map[string]any{"summary": "wrote hello.txt", "status": "completed"}, nil)
	if s := i.taskState(rtask); s != "In Review" {
		t.Fatalf("the 1.0.1 binary leaves a submitted result %s, want In Review", s)
	}
	_, wa := i.planned("working")
	wtask, _ := wa.claim()
	before := i.count(`SELECT max(version) FROM schema_migrations`)
	wa.kill()
	ra.kill()
	i.kill9()

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
	i.start() // the 1.2 binary on the 1.0.1 database
	if v := i.count(`SELECT max(version) FROM schema_migrations`); v != before {
		t.Fatalf("schema version %d after the upgrade, was %d: 1.2 must not migrate", v, before)
	}
	if m, _ := filepath.Glob(i.dbPath() + ".pre-v*.bak"); len(m) != 0 {
		t.Fatalf("a migration backup exists although nothing was migrated: %v", m)
	}
	if got := i.taskState(wtask); got != "Blocked" {
		t.Fatalf("the task that was being worked on is %s, want Blocked", got)
	}
	if n := i.count(`SELECT count(*) FROM executions WHERE task_id = ?`, wtask); n != 1 {
		t.Fatalf("%d executions: something started on its own", n)
	}
	// The old result approval is still decided the old way, and only by a human with the passphrase.
	apr := rp.pending("result")
	if st, _ := i.operator().call("POST", "/approvals/"+apr+"/decision", map[string]string{"decision": "approve"}, nil, nil); st < 400 {
		t.Fatalf("the operator token decided the legacy result approval without a passphrase: %d", st)
	}
	rp.dash.mustDecide(apr, "approve", nil)
	if s := i.taskState(rtask); s != "Done" {
		t.Fatalf("after the human approved the legacy result the task is %s", s)
	}

	// Shapes that only 1.2 writes: a task Done by the agent with no approval, a phase waiting for its review whose workflow is
	// then replaced (its approval withdrawn), and a running execution cancelled by the replacement.
	sp, sa := i.planned("shape-done")
	sdone, _ := sa.claim()
	os.WriteFile(filepath.Join(sp.dir, "hello.txt"), []byte("hello\n"), 0o600)
	sa.must("waves_submit_result", map[string]any{"summary": "wrote hello.txt", "status": "completed"}, nil)
	waitFor(t, 15*time.Second, "phase review", func() bool { _, st := i.phase(sp.id, "p1"); return st == "Awaiting Phase Review" })
	replaceWorkflow(t, i, sp, sa)
	cp, ca := i.planned("shape-cancelled")
	ctask, _ := ca.claim()
	replaceWorkflow(t, i, cp, ca)
	if s := i.taskState(sdone); s != "Done" || i.taskState(ctask) != "Cancelled" {
		t.Fatalf("1.2 shapes: %s and %s", s, i.taskState(ctask))
	}
	pendingBefore := i.count(`SELECT count(*) FROM approvals WHERE state = 'pending' AND category IN ('phase_completion','result','risky_action')`)

	// Back to 1.0.1: the same database opens, nothing is refused.
	i.kill9()
	if err := os.WriteFile(old+".new", oldBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(old+".new", old); err != nil {
		t.Fatal(err)
	}
	i.start()
	var st struct {
		Audit struct {
			State string `json:"state"`
		} `json:"audit"`
	}
	i.operator().must("GET", "/status", nil, &st)
	if st.Audit.State != "verified" {
		t.Fatalf("the audit chain under the 1.0.1 binary after 1.2 ran: %q", st.Audit.State)
	}
	if s := i.taskState(rtask); s != "Done" {
		t.Fatalf("under 1.0.1 again the task is %s", s)
	}
	// The shapes 1.2 wrote read the same, and the 1.0.1 binary asks nothing new about the replaced versions.
	if i.taskState(sdone) != "Done" || i.taskState(ctask) != "Cancelled" {
		t.Fatalf("under 1.0.1 the 1.2 shapes read %s and %s", i.taskState(sdone), i.taskState(ctask))
	}
	time.Sleep(3 * time.Second) // 1.0.1 recovers phases in validation at start
	if n := i.count(`SELECT count(*) FROM approvals WHERE state = 'pending' AND category IN ('phase_completion','result','risky_action')`); n != pendingBefore {
		t.Fatalf("%d approvals wait under 1.0.1, %d did under 1.2", n, pendingBefore)
	}
}

// replaceWorkflow submits a new workflow version for the project and approves it as a human: whatever the old version still
// had open is ended.
func replaceWorkflow(t *testing.T, i *inst, p *project, a *agent) {
	t.Helper()
	p.dash.must("POST", "/projects/"+p.id+"/requirements", map[string]string{"kind": "text", "text": "different requirements"}, nil)
	p.dash.must("POST", "/projects/"+p.id+"/planning-requests", nil, nil)
	var req struct {
		ID string `json:"planning_request_id"`
	}
	a.must("waves_get_planning_request", map[string]any{}, &req)
	var prop map[string]any
	json.Unmarshal([]byte(sampleProposal), &prop)
	a.must("waves_submit_workflow_proposal", map[string]any{"planning_request_id": req.ID, "proposal": prop}, nil)
	var d contract.ProjectDetail
	p.dash.must("GET", "/projects/"+p.id, nil, &d)
	p.dash.must("POST", "/workflow-versions/"+d.Planning.LatestProposal.WorkflowVersionID+"/submit", nil, nil)
	p.dash.mustDecide(p.pending("workflow"), "approve", nil)
}
