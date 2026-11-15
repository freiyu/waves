package core

import (
	"os"
	"path/filepath"
	"testing"
)

// doneTask1 completes p1-t1 (a code change) so that p1-t2, the read-only task, becomes Ready.
func (a *agentFlow) doneTask1(t *testing.T) {
	t.Helper()
	t1 := a.task(t, "p1-t1")
	r, err := a.s.ClaimTask(a.ctx, a.agent, "s1", t1, "")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(a.root, "health.go"), []byte("package main\n"), 0o600)
	if err := a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "added handler", "completed", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.decide(t, a.p, a.pendingApproval(t, "result", t1), "approve", false); err != nil {
		t.Fatal(err)
	}
}

// A task declared read-only that left a file behind is not read-only: its result is reviewed like any other.
func TestReadOnlyTaskThatChangedFilesIsReviewed(t *testing.T) {
	a := startedFlow(t)
	a.doneTask1(t)
	t2 := a.task(t, "p1-t2")
	r, err := a.s.ClaimTask(a.ctx, a.agent, "s1", t2, "")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(a.root, "leak.txt"), []byte("not read-only\n"), 0o600)
	if err := a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "docs", "completed", nil); err != nil {
		t.Fatal(err)
	}
	if a.state(t, "tasks", t2) != "In Review" {
		t.Fatalf("a read-only task that wrote a file is %s, want In Review", a.state(t, "tasks", t2))
	}
	if a.pendingApproval(t, "result", t2) == "" {
		t.Fatal("no result approval was requested")
	}
}

// A session that holds a lease is judged by that lease's project even when its working folder is in no project, so a
// write outside the workspace is denied instead of passing as "not a project". A session with no lease is unchanged.
func TestGuardJudgesALeasedSessionWhoseCwdDrifted(t *testing.T) {
	a := startedFlow(t)
	if err := a.s.SetProjectGuard(a.ctx, a.p, mustStepUp(t, a.env, a.p), a.project, "enforce", "deny"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t1"), "tu_d"); err != nil {
		t.Fatal(err)
	}
	a.hook(t, "cc", "mcp__waves__waves_claim_task", "tu_d", nil)
	elsewhere := t.TempDir() // a folder that belongs to no project
	hook := func(session, tool, target string) GuardDecision {
		in := []byte(`{"file_path":"` + target + `"}`)
		d, err := a.s.DecideGuard(a.ctx, a.guard, HookEvent{SessionID: session, Cwd: elsewhere, ToolName: tool, ToolUseID: "x", ToolInput: in})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if d := hook("cc", "Write", filepath.Join(elsewhere, "x.txt")); d.Decision != "deny" || d.Reason != "outside_workspace" {
		t.Fatalf("a leased session writing outside its workspace: %+v", d)
	}
	if d := hook("cc", "Write", filepath.Join(a.root, "ok.go")); d.Decision != "allow" {
		t.Fatalf("a leased session writing inside its workspace: %+v", d)
	}
	if d := hook("nobody", "Write", filepath.Join(elsewhere, "x.txt")); d.Decision != "allow" || d.Reason != "not_a_project" {
		t.Fatalf("a session with no lease, outside every project: %+v", d)
	}
}

// Resolving an execution that is not stopped is refused before anything is written: its stored diff is not rewritten.
func TestResolveRefusedBeforeTheDiffIsTouched(t *testing.T) {
	a := startedFlow(t)
	r, err := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t1"), "")
	if err != nil {
		t.Fatal(err)
	}
	diff := filepath.Join(a.s.dirs.Artifacts(), r.ExecutionID+".diff")
	os.MkdirAll(filepath.Dir(diff), 0o700)
	os.WriteFile(diff, []byte("the diff as it was\n"), 0o600)
	if err := a.s.ResolveExecution(a.ctx, a.p, r.ExecutionID, "fail"); code(err) != "invalid_state" {
		t.Fatalf("resolving a running execution: %v", err)
	}
	if b, _ := os.ReadFile(diff); string(b) != "the diff as it was\n" {
		t.Fatalf("a refused resolve rewrote the diff: %q", b)
	}
}
