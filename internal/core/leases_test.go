package core

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// agentFlow is an approved, started first phase in a Git project with
// Claude Code connected, plus the agent's MCP principal.
type agentFlow struct {
	*flow
	root  string
	agent Principal
	guard Principal
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, a := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.email", "t@example.invalid"}, {"config", "user.name", "t"}, {"commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", a, err, out)
		}
	}
}

func startedFlow(t *testing.T) *agentFlow {
	t.Helper()
	e := newEnv(t).withPassphrase(t)
	f := &flow{env: e, p: e.dashboard(t)}
	root := filepath.Join(e.dir, "proj")
	os.MkdirAll(root, 0o700)
	gitInit(t, root)
	id, err := e.s.RegisterProject(e.ctx, f.p, root, "demo")
	if err != nil {
		t.Fatal(err)
	}
	f.project = id
	e.s.AddRequirement(e.ctx, f.p, id, "text", "", "Add a health endpoint")
	f.version = e.draft(t, f.p, id, goodProposal)
	apr, _ := e.s.SubmitVersion(e.ctx, f.p, f.version)
	if err := e.decide(t, f.p, apr, "approve", false); err != nil {
		t.Fatal(err)
	}
	e.db.R.QueryRow(`SELECT id FROM phases WHERE workflow_version_id = ? AND position = 1`, f.version).Scan(&f.phase1)
	if err := e.decide(t, f.p, e.pendingApproval(t, "phase_start", f.phase1), "approve", true); err != nil {
		t.Fatal(err)
	}
	a := &agentFlow{flow: f, root: root}
	plan, err := e.s.ConnectPlan(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Add(time.Second)
	if _, err := e.s.Connect(e.ctx, f.p, mustStepUp(t, e, f.p), id, plan.PlanHash); err != nil {
		t.Fatalf("connect: %v", err)
	}
	tok, err := os.ReadFile(e.s.mcpTokenPath(id))
	if err != nil {
		t.Fatal(err)
	}
	a.agent, _ = e.s.Bearer(e.ctx, strings.TrimSpace(string(tok)), "mcp")
	if a.agent.Kind != KindMCP || a.agent.ProjectID != id {
		t.Fatalf("mcp credential resolved to %+v", a.agent)
	}
	gt, _ := e.s.CreateCredential(e.ctx, "guard")
	a.guard, _ = e.s.Bearer(e.ctx, gt, "hook")
	return a
}

func (a *agentFlow) task(t *testing.T, key string) string {
	var id string
	a.db.R.QueryRow(`SELECT t.id FROM tasks t JOIN phases p ON p.id = t.phase_id WHERE p.workflow_version_id = ? AND t.key = ?`, a.version, key).Scan(&id)
	return id
}

func (a *agentFlow) hook(t *testing.T, session, tool, toolUse string, input map[string]any) GuardDecision {
	t.Helper()
	in, _ := json.Marshal(input)
	d, err := a.s.DecideGuard(a.ctx, a.guard, HookEvent{SessionID: session, Cwd: a.root, ToolName: tool, ToolUseID: toolUse, ToolInput: in})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestConnectWritesOnlyProjectFilesAndReverts(t *testing.T) {
	a := startedFlow(t)
	b, err := os.ReadFile(filepath.Join(a.root, ".claude", "settings.local.json"))
	if err != nil || !strings.Contains(string(b), "hook --policy audit") || !strings.Contains(string(b), hookMatcher) {
		t.Fatalf("settings: %s %v", b, err)
	}
	m, _ := os.ReadFile(filepath.Join(a.root, ".mcp.json"))
	if !strings.Contains(string(m), `"--project"`) || strings.Contains(string(m), "wmp_") {
		t.Fatalf(".mcp.json must name the project and hold no secret: %s", m)
	}
	if st, _ := os.Stat(a.s.mcpTokenPath(a.project)); st.Mode().Perm() != 0o600 {
		t.Fatal("token file mode")
	}
	// enforce rewrites the installed hook with the fail-closed wrapper.
	a.clock.Add(time.Second)
	if err := a.s.SetProjectGuard(a.ctx, a.p, mustStepUp(t, a.env, a.p), a.project, "enforce", ""); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(a.root, ".claude", "settings.local.json"))
	if !strings.Contains(string(b), "/bin/sh -c") || !strings.Contains(string(b), "|| exit 2") {
		t.Fatalf("enforce hook: %s", b)
	}
	if err := a.s.SetProjectGuard(a.ctx, a.p, "", a.project, "audit", ""); code(err) != "stepup_required" {
		t.Fatalf("policy change without elevation: %v", err)
	}
	a.clock.Add(time.Second)
	if _, err := a.s.Disconnect(a.ctx, a.p, mustStepUp(t, a.env, a.p), a.project); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".claude/settings.local.json", ".mcp.json"} {
		if _, err := os.Stat(filepath.Join(a.root, f)); !os.IsNotExist(err) {
			t.Fatalf("%s left behind after disconnect", f)
		}
	}
	if p, _ := a.s.Bearer(a.ctx, "x", ""); p.Kind != KindNone {
		t.Fatal("bearer")
	}
	var live int
	a.db.R.QueryRow(`SELECT count(*) FROM credentials WHERE project_id = ? AND revoked_at IS NULL`, a.project).Scan(&live)
	if live != 0 {
		t.Fatal("credential not revoked")
	}
}

func TestDuplicateClaimsGiveOneLease(t *testing.T) {
	a := startedFlow(t)
	task := a.task(t, "p1-t1")
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = a.s.ClaimTask(a.ctx, a.agent, "sess-"+itoa(int64(i)), task, "")
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		}
	}
	var leases, execs int
	a.db.R.QueryRow(`SELECT count(*) FROM leases WHERE task_id = ?`, task).Scan(&leases)
	a.db.R.QueryRow(`SELECT count(*) FROM executions WHERE task_id = ?`, task).Scan(&execs)
	if ok != 1 || leases != 1 || execs != 1 {
		t.Fatalf("%d claims succeeded, %d leases, %d executions; want 1 each", ok, leases, execs)
	}
	if a.state(t, "phases", a.phase1) != "In Progress" || a.state(t, "tasks", task) != "In Progress" {
		t.Fatal("claim did not start phase and task")
	}
}

func TestLeaseScopeAndBinding(t *testing.T) {
	a := startedFlow(t)
	task := a.task(t, "p1-t1")
	// A task of another project looks like a missing task.
	other := a.agent
	other.ProjectID = "prj_other"
	if _, err := a.s.ClaimTask(a.ctx, other, "s", task, ""); code(err) != "task_not_ready" {
		t.Fatalf("foreign project claim: %v", err)
	}
	if _, err := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t2"), ""); code(err) != "task_not_ready" {
		t.Fatalf("Backlog task claimed: %v", err)
	}
	if _, err := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p2-t1"), ""); code(err) != "phase_not_open" && code(err) != "task_not_ready" {
		t.Fatalf("task of an unapproved phase claimed: %v", err)
	}
	if _, err := a.s.ClaimTask(a.ctx, Principal{Kind: KindOperator}, "s1", task, ""); code(err) != "wrong_scope" {
		t.Fatal("operator claimed a task")
	}
	r, err := a.s.ClaimTask(a.ctx, a.agent, "s1", task, "tu_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.s.Heartbeat(a.ctx, a.agent, "s2", r.LeaseToken); code(err) != "lease_invalid" {
		t.Fatalf("lease used on another MCP session: %v", err)
	}
	if _, err := a.s.Heartbeat(a.ctx, a.agent, "s1", "wls_forged"); code(err) != "lease_invalid" {
		t.Fatalf("forged lease: %v", err)
	}
	if hb, err := a.s.Heartbeat(a.ctx, a.agent, "s1", ""); err != nil || !hb.LeaseOK || hb.PhaseState != "in_progress" {
		t.Fatalf("heartbeat without token on the claiming session: %+v %v", hb, err)
	}
	// AH-4: the lease token presented as a bearer is a lease principal; it cannot approve or step up.
	lp, ok := a.s.Bearer(a.ctx, r.LeaseToken, "x")
	if !ok || lp.Kind != KindLease {
		t.Fatalf("lease bearer: %+v", lp)
	}
	if _, err := a.s.StepUp(a.ctx, lp, testPass, ""); code(err) != "wrong_scope" {
		t.Fatal("lease stepped up")
	}
	if err := a.s.Decide(a.ctx, lp, Decision{ApprovalID: "apr_x", Decision: "approve"}); code(err) != "wrong_scope" {
		t.Fatal("lease decided")
	}
	if a.auditCount(t, "auth.bypass_attempt") < 2 {
		t.Fatal("lease bypass not audited")
	}
	// The guard binds the session through the tool_use_id of the claim call.
	a.hook(t, "cc-session", "mcp__waves__waves_claim_task", "tu_1", nil)
	var sess string
	a.db.R.QueryRow(`SELECT COALESCE(session_id,'') FROM leases WHERE task_id = ?`, task).Scan(&sess)
	if sess != "cc-session" {
		t.Fatalf("lease session = %q", sess)
	}
	// A later tool use cannot move the lease to another session.
	a.db.W.Exec(`INSERT INTO tool_use_bindings(tool_use_id, lease_id) SELECT 'tu_2', id FROM leases WHERE task_id = ?`, task)
	a.hook(t, "evil-session", "mcp__waves__waves_heartbeat", "tu_2", nil)
	a.db.R.QueryRow(`SELECT session_id FROM leases WHERE task_id = ?`, task).Scan(&sess)
	if sess != "cc-session" {
		t.Fatal("lease moved to another session")
	}
}

func TestHookBindsInEitherOrder(t *testing.T) {
	a := startedFlow(t)
	task := a.task(t, "p1-t1")
	a.hook(t, "cc-early", "mcp__waves__waves_claim_task", "tu_early", nil) // hook first
	if _, err := a.s.ClaimTask(a.ctx, a.agent, "s1", task, "tu_early"); err != nil {
		t.Fatal(err)
	}
	var sess string
	a.db.R.QueryRow(`SELECT COALESCE(session_id,'') FROM leases WHERE task_id = ?`, task).Scan(&sess)
	if sess != "cc-early" {
		t.Fatalf("binding with the hook first: %q", sess)
	}
}

func TestGuardDecisions(t *testing.T) {
	a := startedFlow(t)
	inside := map[string]any{"file_path": filepath.Join(a.root, "main.go")}
	// audit: an unleased write is allowed but recorded as would-deny.
	if d := a.hook(t, "nobody", "Write", "x1", inside); d.Decision != "allow" || d.Policy != "audit" {
		t.Fatalf("audit unleased: %+v", d)
	}
	a.clock.Add(time.Second)
	if err := a.s.SetProjectGuard(a.ctx, a.p, mustStepUp(t, a.env, a.p), a.project, "enforce", "deny"); err != nil {
		t.Fatal(err)
	}
	if d := a.hook(t, "nobody", "Write", "x2", inside); d.Decision != "deny" || d.Reason != "unmanaged" {
		t.Fatalf("enforce unmanaged: %+v", d)
	}
	r, err := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t1"), "tu_c")
	if err != nil {
		t.Fatal(err)
	}
	a.hook(t, "cc", "mcp__waves__waves_claim_task", "tu_c", nil)
	cases := []struct {
		tool  string
		in    map[string]any
		want  string
		cause string
	}{
		{"Write", inside, "allow", "leased"},
		{"Edit", map[string]any{"file_path": "sub/new.go"}, "allow", "leased"},
		{"Write", map[string]any{"file_path": "/etc/passwd"}, "deny", "outside_workspace"},
		{"Write", map[string]any{"file_path": filepath.Join(a.root, "..", "escape.txt")}, "deny", "outside_workspace"},
		{"Write", map[string]any{"file_path": filepath.Join(a.root, ".git", "config")}, "deny", "protected_path"},
		{"Write", map[string]any{"file_path": filepath.Join(a.root, ".claude", "settings.local.json")}, "deny", "protected_path"},
		{"Write", map[string]any{"file_path": filepath.Join(a.root, ".mcp.json")}, "deny", "protected_path"},
		{"Bash", map[string]any{"command": "echo hi > x"}, "allow", "bash_unchecked"},
		{"Read", map[string]any{"file_path": "/etc/hosts"}, "allow", "not_a_file_tool"},
	}
	for _, c := range cases {
		if d := a.hook(t, "cc", c.tool, "t", c.in); d.Decision != c.want || d.Reason != c.cause {
			t.Errorf("%s %v: %+v, want %s %s", c.tool, c.in, d, c.want, c.cause)
		}
	}
	// A symlink inside the project that points outside is resolved.
	os.Symlink("/tmp", filepath.Join(a.root, "link"))
	if d := a.hook(t, "cc", "Write", "t", map[string]any{"file_path": filepath.Join(a.root, "link", "x")}); d.Decision != "deny" {
		t.Fatalf("symlink escape: %+v", d)
	}
	if _, err := a.s.ReleaseTask(a.ctx, a.agent, "s1", r.LeaseToken, "done here"); err != nil {
		t.Fatal(err)
	}
	if d := a.hook(t, "cc", "Write", "t", inside); d.Decision != "deny" || d.Reason != "lease_ended" {
		t.Fatalf("after release: %+v", d)
	}
	if _, err := a.s.DecideGuard(a.ctx, a.agent, HookEvent{SessionID: "cc", Cwd: a.root, ToolName: "Write"}); code(err) != "wrong_scope" {
		t.Fatal("an MCP credential called the guard endpoint")
	}
}

func TestReleaseRule(t *testing.T) {
	a := startedFlow(t)
	task := a.task(t, "p1-t1")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", task, "")
	if to, err := a.s.ReleaseTask(a.ctx, a.agent, "s1", r.LeaseToken, "not mine"); err != nil || to != "ready" {
		t.Fatalf("release unchanged: %s %v", to, err)
	}
	r, err := a.s.ClaimTask(a.ctx, a.agent, "s1", task, "")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(a.root, "half.go"), []byte("package x\n"), 0o600)
	if to, err := a.s.ReleaseTask(a.ctx, a.agent, "s1", r.LeaseToken, "gave up"); err != nil || to != "blocked" {
		t.Fatalf("release with changes: %s %v", to, err)
	}
	var reason string
	a.db.R.QueryRow(`SELECT blocked_reason FROM tasks WHERE id = ?`, task).Scan(&reason)
	if reason != "released_with_changes" {
		t.Fatalf("reason %q", reason)
	}
}

func TestExpiryAndRestartNeverRerun(t *testing.T) {
	a := startedFlow(t)
	task := a.task(t, "p1-t1")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", task, "")
	a.clock.Add(61 * time.Second)
	a.s.expireLeases(a.ctx)
	if a.state(t, "executions", r.ExecutionID) != "Disconnected" {
		t.Fatalf("after missed heartbeats: %s", a.state(t, "executions", r.ExecutionID))
	}
	if hb, err := a.s.Heartbeat(a.ctx, a.agent, "s1", r.LeaseToken); err != nil || !hb.LeaseOK || a.state(t, "executions", r.ExecutionID) != "Running" {
		t.Fatalf("reconnect: %+v %v", hb, err)
	}
	a.clock.Add(181 * time.Second)
	a.s.expireLeases(a.ctx)
	if a.state(t, "executions", r.ExecutionID) != "Interrupted" || a.state(t, "tasks", task) != "Blocked" {
		t.Fatalf("after expiry: %s %s", a.state(t, "executions", r.ExecutionID), a.state(t, "tasks", task))
	}
	if _, err := a.s.Heartbeat(a.ctx, a.agent, "s1", r.LeaseToken); code(err) != "lease_invalid" {
		t.Fatal("expired lease accepted")
	}
	// An interrupted execution keeps the workspace: no new code-changing claim.
	if err := a.s.RetryTask(a.ctx, a.p, task); code(err) != "invalid_state" {
		t.Fatalf("retry before deciding: %v", err)
	}
	if err := a.s.ResolveExecution(a.ctx, a.p, r.ExecutionID, "retry_fresh"); err != nil {
		t.Fatal(err)
	}
	if a.state(t, "tasks", task) != "Ready" {
		t.Fatal("retry did not ready the task")
	}
	// Restart while a task is worked on.
	r2, err := a.s.ClaimTask(a.ctx, a.agent, "s2", task, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.s.recoverExecutions(a.ctx); err != nil {
		t.Fatal(err)
	}
	var reason string
	a.db.R.QueryRow(`SELECT blocked_reason FROM tasks WHERE id = ?`, task).Scan(&reason)
	if a.state(t, "executions", r2.ExecutionID) != "Interrupted" || a.state(t, "tasks", task) != "Blocked" || reason != "recovery_required" {
		t.Fatalf("after restart: %s %s %s", a.state(t, "executions", r2.ExecutionID), a.state(t, "tasks", task), reason)
	}
	var system int
	a.db.R.QueryRow(`SELECT count(*) FROM state_transitions WHERE entity_type = 'task' AND entity_id = ? AND from_state = 'Blocked' AND actor_scope = 'system'`, task).Scan(&system)
	if system != 0 {
		t.Fatal("the system moved a blocked task")
	}
}

func TestWorkspaceLockAllowsOneCodeChange(t *testing.T) {
	a := startedFlow(t)
	// A second Ready code_change task in the same workspace.
	a.db.W.Exec(`INSERT INTO tasks(id, phase_id, key, position, title, workspace_mode, acceptance_json, updated_at) VALUES ('tsk_extra', ?, 'p1-t9', 9, 'Extra', 'code_change', '["x"]', 't')`, a.phase1)
	a.s.tx(a.ctx, func(tx *Tx) error {
		_, err := a.s.transition(a.ctx, tx, "task", "tsk_extra", "Ready", sysActor, "test", "")
		return err
	})
	if _, err := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t1"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.s.ClaimTask(a.ctx, a.agent, "s2", "tsk_extra", ""); code(err) != "workspace_locked" {
		t.Fatalf("second code change in one workspace: %v", err)
	}
}

func TestResultReviewAndValidation(t *testing.T) {
	a := startedFlow(t)
	t1, t2 := a.task(t, "p1-t1"), a.task(t, "p1-t2")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", t1, "")
	if _, err := a.s.TaskContext(a.ctx, a.agent, "s1", ""); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(a.root, "health.go"), []byte("package main\n"), 0o600)
	if err := a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "added handler", "completed", nil); err != nil {
		t.Fatal(err)
	}
	if a.state(t, "tasks", t1) != "In Review" {
		t.Fatalf("t1 is %s", a.state(t, "tasks", t1))
	}
	res := a.pendingApproval(t, "result", t1)
	if err := a.decide(t, a.p, res, "approve", false); err != nil {
		t.Fatalf("result approval: %v", err)
	}
	if a.state(t, "tasks", t1) != "Done" || a.state(t, "tasks", t2) != "Ready" {
		t.Fatalf("after result: %s %s", a.state(t, "tasks", t1), a.state(t, "tasks", t2))
	}
	r2, err := a.s.ClaimTask(a.ctx, a.agent, "s1", t2, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.s.SubmitResult(a.ctx, a.agent, "s1", r2.LeaseToken, "docs", "completed", nil); err != nil {
		t.Fatal(err)
	}
	if a.state(t, "phases", a.phase1) != "Validation" {
		t.Fatalf("phase is %s", a.state(t, "phases", a.phase1))
	}
	// `go test ./...` fails in this bare folder; the review then needs an override.
	if err := a.s.RunValidation(a.ctx, a.phase1); err != nil {
		t.Fatal(err)
	}
	if a.state(t, "phases", a.phase1) != "Awaiting Phase Review" {
		t.Fatal("no review")
	}
	rv, err := a.s.PhaseReview(a.ctx, a.phase1)
	if err != nil || !rv.RequiresOverride || rv.Diff.Executions != 2 {
		t.Fatalf("review: %+v %v", rv, err)
	}
	comp := a.pendingApproval(t, "phase_completion", a.phase1)
	d, _ := a.s.ApprovalDetail(a.ctx, comp)
	a.clock.Add(time.Second)
	el, _ := a.s.StepUp(a.ctx, a.p, testPass, comp)
	if err := a.s.Decide(a.ctx, a.p, Decision{ApprovalID: comp, Decision: "approve", SubjectHash: d.SubjectHash, Elevation: el.Secret}); code(err) != "validation_failed" {
		t.Fatalf("failed validator without override: %v", err)
	}
	// Confirming the checklist changes the subject: the old screen is stale.
	for _, v := range rv.Validators {
		if v.Kind == "checklist" {
			// Without a step-up the confirmation is refused (an agent with the operator token cannot tick it).
			if err := a.s.ConfirmChecklistItem(a.ctx, a.p, "", a.phase1, v.ID, 0); code(err) != "stepup_required" {
				t.Fatalf("checklist confirm without elevation: %v", err)
			}
			a.clock.Add(time.Second)
			win := mustStepUp(t, a.env, a.p)
			if err := a.s.ConfirmChecklistItem(a.ctx, a.p, win, a.phase1, v.ID, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := a.s.Decide(a.ctx, a.p, Decision{ApprovalID: comp, Decision: "approve", SubjectHash: d.SubjectHash, Elevation: el.Secret, Override: "known flaky"}); code(err) != "stale_subject" {
		t.Fatalf("stale review: %v", err)
	}
	d, _ = a.s.ApprovalDetail(a.ctx, comp)
	if err := a.s.Decide(a.ctx, a.p, Decision{ApprovalID: comp, Decision: "approve", SubjectHash: d.SubjectHash, Elevation: el.Secret, Override: "known flaky"}); err != nil {
		t.Fatal(err)
	}
	var p2 string
	a.db.R.QueryRow(`SELECT id FROM phases WHERE workflow_version_id = ? AND position = 2`, a.version).Scan(&p2)
	if a.state(t, "phases", a.phase1) != "Completed" || a.state(t, "phases", p2) != "Awaiting Approval" {
		t.Fatal("completion did not open the next gate")
	}
}

// Review M1: the repository's own git config cannot run programs or hide changes.
func TestGitConfigOfTheRepoIsIgnored(t *testing.T) {
	a := startedFlow(t)
	marker := filepath.Join(a.dir, "ran")
	script := filepath.Join(a.dir, "evil.sh")
	os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\necho forged\n"), 0o700)
	for _, kv := range [][2]string{{"diff.external", script}, {"core.fsmonitor", script}, {"diff.x.textconv", script}} {
		exec.Command("git", "-C", a.root, "config", kv[0], kv[1]).Run()
	}
	os.WriteFile(filepath.Join(a.root, ".gitattributes"), []byte("* diff=x\n"), 0o600)
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t1"), "")
	os.WriteFile(filepath.Join(a.root, "real.go"), []byte("package real\n"), 0o600)
	exec.Command("git", "-C", a.root, "add", "real.go").Run()
	os.Remove(marker) // the test's own plain git call above may run it; only Waves' calls count
	path, files := a.s.captureDiff(a.ctx, r.ExecutionID)
	a.s.workspaceUnchanged(a.ctx, r.ExecutionID)
	gitInfo(a.ctx, a.root)
	b, _ := os.ReadFile(path)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("git ran a program from the repository config")
	}
	if files == 0 || !strings.Contains(string(b), "package real") || strings.Contains(string(b), "forged") {
		t.Fatalf("diff hides the change: files=%d\n%s", files, b)
	}
}

// Review L5: a FIFO in the work tree does not hang the service.
func TestFifoDoesNotHang(t *testing.T) {
	a := startedFlow(t)
	if err := exec.Command("mkfifo", filepath.Join(a.root, "pipe")).Run(); err != nil {
		t.Skip("mkfifo not available")
	}
	done := make(chan struct{})
	go func() { workspaceFingerprint(a.ctx, a.root); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("fingerprint hung on a FIFO")
	}
}

// Review M3: no nested projects, and the target picks the project the guard applies.
func TestNestedProjectsAndCrossProjectWrites(t *testing.T) {
	a := startedFlow(t)
	inner := filepath.Join(a.root, "sub")
	os.MkdirAll(inner, 0o700)
	if _, err := a.s.RegisterProject(a.ctx, a.p, inner, ""); code(err) != "validation_failed" {
		t.Fatalf("nested project registered: %v", err)
	}
	other := filepath.Join(a.dir, "other")
	os.MkdirAll(other, 0o700)
	if _, err := a.s.RegisterProject(a.ctx, a.p, a.dir, ""); code(err) != "validation_failed" {
		t.Fatalf("outer project registered: %v", err)
	}
	ob, err := a.s.RegisterProject(a.ctx, a.p, other, "")
	if err != nil {
		t.Fatal(err)
	}
	a.db.W.Exec(`UPDATE project_settings SET guard_policy = 'enforce' WHERE project_id = ?`, ob)
	a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t1"), "tu_x")
	a.hook(t, "cc", "mcp__waves__waves_claim_task", "tu_x", nil)
	// The session leased in the first project writes into the other one, from its own cwd.
	if d := a.hook(t, "cc", "Write", "t", map[string]any{"file_path": filepath.Join(other, ".claude", "settings.local.json")}); d.Decision != "deny" {
		t.Fatalf("cross-project write: %+v", d)
	}
}

// Review L2: connecting refuses symlinked project files.
func TestConnectRefusesSymlinks(t *testing.T) {
	e := newEnv(t).withPassphrase(t)
	p := e.dashboard(t)
	root := filepath.Join(e.dir, "p2")
	os.MkdirAll(root, 0o700)
	os.Symlink(filepath.Join(e.dir, "elsewhere"), filepath.Join(root, ".claude"))
	id, _ := e.s.RegisterProject(e.ctx, p, root, "p2")
	if _, err := e.s.ConnectPlan(e.ctx, id); code(err) != "validation_failed" {
		t.Fatalf("symlinked .claude accepted: %v", err)
	}
}

func TestDraftEditAndWorkflowFromProposal(t *testing.T) {
	e := newEnv(t).withPassphrase(t)
	p := e.dashboard(t)
	project := e.project(t, p)
	v := e.draft(t, p, project, goodProposal)
	edited := strings.Replace(goodProposal, `"title": "Build"`, `"title": "Build it"`, 1)
	if errs, err := e.s.EditDraft(e.ctx, p, v, []byte(edited)); err != nil || errs != nil {
		t.Fatalf("edit: %v %+v", err, errs)
	}
	wv, _ := e.s.WorkflowVersion(e.ctx, v)
	if wv.Phases[0].Title != "Build it" || wv.Number != 1 {
		t.Fatalf("after edit: %+v", wv)
	}
	if errs, _ := e.s.EditDraft(e.ctx, p, v, []byte(`{"schema_version":1}`)); len(errs) == 0 {
		t.Fatal("invalid edit accepted")
	}
	if _, err := e.s.SubmitVersion(e.ctx, p, v); err != nil {
		t.Fatalf("submit after edit (hash must match): %v", err)
	}
	if _, err := e.s.EditDraft(e.ctx, p, v, []byte(edited)); code(err) != "invalid_state" {
		t.Fatalf("edit of a submitted version: %v", err)
	}
	props, _ := e.s.Proposals(e.ctx, project)
	v2, err := e.s.CreateWorkflowFromProposal(e.ctx, p, props[0].ID)
	if err != nil || v2 == v {
		t.Fatalf("workflow from proposal: %s %v", v2, err)
	}
}

func TestBlockerResumeAndRetryRules(t *testing.T) {
	a := startedFlow(t)
	task := a.task(t, "p1-t1")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", task, "")
	if err := a.s.ReportBlocker(a.ctx, a.agent, "s1", r.LeaseToken, "need a decision on the API shape"); err != nil {
		t.Fatal(err)
	}
	if err := a.s.RetryTask(a.ctx, a.p, task); code(err) != "invalid_state" {
		t.Fatalf("retry while the agent still holds the task: %v", err)
	}
	if err := a.s.ResumeTask(a.ctx, a.p, task); err != nil {
		t.Fatalf("resume after a blocker: %v", err)
	}
	if a.state(t, "tasks", task) != "In Progress" {
		t.Fatal("not resumed")
	}
	if err := a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "done", "completed", nil); err != nil {
		t.Fatalf("submit after resume: %v", err)
	}
}

// Review: after a restart no validator command runs on its own.
func TestRestartDoesNotRunValidators(t *testing.T) {
	a := startedFlow(t)
	marker := filepath.Join(a.root, "validator-ran")
	a.db.W.Exec(`UPDATE validators SET command = 'touch validator-ran' WHERE type = 'command'`)
	for _, k := range []string{"p1-t1", "p1-t2"} {
		r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, k), "")
		a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "x", "completed", nil)
		if k == "p1-t1" {
			a.decide(t, a.p, a.pendingApproval(t, "result", a.task(t, k)), "approve", false)
		}
	}
	if a.state(t, "phases", a.phase1) != "Validation" {
		t.Fatalf("phase is %s", a.state(t, "phases", a.phase1))
	}
	if err := a.s.recover(a.ctx); err != nil { // the queued run is dropped by the restart
		t.Fatal(err)
	}
	if err := a.s.RunValidation(a.ctx, a.phase1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a validator ran after the restart")
	}
	rv, _ := a.s.PhaseReview(a.ctx, a.phase1)
	if a.state(t, "phases", a.phase1) != "Awaiting Phase Review" || !rv.RequiresOverride {
		t.Fatalf("after restart: %s override=%v", a.state(t, "phases", a.phase1), rv.RequiresOverride)
	}
}

// Final review M-A, M-B: a configured filter never runs; -diff cannot hide content.
func TestGitFiltersAndAttributes(t *testing.T) {
	a := startedFlow(t)
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t1"), "")
	os.WriteFile(filepath.Join(a.root, ".git", "info", "attributes"), []byte("* -diff\n"), 0o600)
	os.WriteFile(filepath.Join(a.root, "code.go"), []byte("package code\n"), 0o600)
	path, _ := a.s.captureDiff(a.ctx, r.ExecutionID)
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "package code") {
		t.Fatalf("-diff attribute hid the content:\n%s", b)
	}
	marker := filepath.Join(a.dir, "filter-ran")
	exec.Command("git", "-C", a.root, "config", "filter.x.clean", "touch "+marker+"; cat").Run()
	os.WriteFile(filepath.Join(a.root, ".git", "info", "attributes"), []byte("* filter=x\n"), 0o600)
	os.Remove(marker)
	path, n := a.s.captureDiff(a.ctx, r.ExecutionID)
	workspaceFingerprint(a.ctx, a.root)
	a.s.PhaseReview(a.ctx, a.phase1)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a repository filter ran")
	}
	b, _ = os.ReadFile(path)
	if n != -1 || !strings.Contains(string(b), "did not compute this diff") {
		t.Fatalf("filter not reported: n=%d %s", n, b)
	}
}

// Final review M-C: a dangling symlink does not carry a write outside or into .git.
func TestDanglingSymlinkIsRefused(t *testing.T) {
	a := startedFlow(t)
	os.Symlink(filepath.Join(a.root, ".git", "hooks", "pre-commit"), filepath.Join(a.root, "out"))
	os.Symlink("/nonexistent/elsewhere", filepath.Join(a.root, "far"))
	for _, p := range []string{"out", "far", "far/x"} {
		if _, err := InsideRoot(a.root, filepath.Join(a.root, p)); err == nil {
			t.Errorf("%s accepted", p)
		}
	}
}

// A rejected result sends the task back with the human's feedback, and the
// next claim's context package carries it (found in the real Claude Code run:
// without it the agent resubmitted the same work).
func TestRejectedResultFeedbackReachesTheAgent(t *testing.T) {
	a := startedFlow(t)
	t1 := a.task(t, "p1-t1")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", t1, "")
	os.WriteFile(filepath.Join(a.root, "health.go"), []byte("package main\n"), 0o600)
	if err := a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "added handler", "completed", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.decide(t, a.p, a.pendingApproval(t, "result", t1), "reject", false); err != nil {
		t.Fatal(err)
	}
	if a.state(t, "tasks", t1) != "Ready" {
		t.Fatalf("t1 is %s", a.state(t, "tasks", t1))
	}
	if _, err := a.s.ClaimTask(a.ctx, a.agent, "s2", t1, ""); err != nil {
		t.Fatal(err)
	}
	pkg, err := a.s.TaskContext(a.ctx, a.agent, "s2", "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sec := range pkg.Sections {
		found = found || (sec.Title == "Changes requested at review" && strings.Contains(sec.Content, "needs work") && strings.Contains(sec.Content, "operator credential (not passphrase confirmed)"))
	}
	if !found {
		t.Fatalf("the rejection feedback is not in the context: %+v", pkg.Sections)
	}
}
