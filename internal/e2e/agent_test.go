//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"waves/internal/api/contract"
)

// Fake agent scenarios (the scriptable fake harness on the MCP bridge).
// Success is TestHappyPathThroughTheBinary.

func TestAgentFailure(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	_, a := i.planned("failure")
	task, _ := a.claim()
	a.must("waves_submit_result", map[string]any{"summary": "tests do not pass", "status": "failed"}, nil)
	exe, st := i.execOf(task)
	if st != "Failed" || i.taskState(task) != "Blocked" {
		t.Fatalf("execution %s task %s, want Failed and Blocked", st, i.taskState(task))
	}
	if r := i.str(`SELECT blocked_reason FROM tasks WHERE id = ?`, task); r != "execution_failed" {
		t.Fatalf("blocked reason %q", r)
	}
	if text, isErr := a.call("waves_heartbeat", map[string]any{}); !isErr {
		t.Fatalf("the lease outlived the failed result: %s", text)
	}
	c := i.dashboard()
	if !attention(c, "execution_failed", exe) {
		t.Fatal("the failed execution is not in needs-attention")
	}
	// The human retries: the task is Ready, and the old failure drops off.
	c.must("POST", "/tasks/"+task+"/retry", map[string]string{"reason": "flaky test"}, nil)
	if i.taskState(task) != "Ready" || attention(c, "execution_failed", exe) {
		t.Fatalf("after retry: task %s, still listed %v", i.taskState(task), attention(c, "execution_failed", exe))
	}
}

func attention(c *client, kind, ref string) bool {
	var s contract.StatusResponse
	c.must("GET", "/status", nil, &s)
	for _, a := range s.NeedsAttention {
		if a.Kind == kind && a.RefID == ref {
			return true
		}
	}
	return false
}

// Hang: the agent stops heartbeating. After 2 intervals the execution is
// Disconnected; after 6 it is Interrupted and the task Blocked. Crash: the
// bridge process dies; same outcome, and the lease cannot move to a new
// MCP session. Both run on real timers (heartbeat 15 s, the minimum).
func TestAgentHangAndCrash(t *testing.T) {
	if testing.Short() {
		t.Skip("real lease timers take 90 s")
	}
	t.Parallel()
	i := newInst(t)
	i.setHeartbeat(15)
	_, hang := i.planned("hang")
	cp, crash := i.planned("crash")
	hTask, _ := hang.claim()
	cTask, cLease := crash.claim()
	crash.kill()
	start := time.Now()
	waitFor(t, 45*time.Second, "Disconnected", func() bool {
		_, a := i.execOf(hTask)
		_, b := i.execOf(cTask)
		return a == "Disconnected" && b == "Disconnected"
	})
	t.Logf("disconnected after %s", time.Since(start).Round(time.Second))
	// A new MCP session cannot take over the crashed agent's lease.
	other := cp.agent(i)
	if text, isErr := other.call("waves_heartbeat", map[string]any{"lease_token": cLease}); !isErr {
		t.Fatalf("a new session took over the lease: %s", text)
	}
	waitFor(t, 75*time.Second, "lease expiry", func() bool {
		_, a := i.execOf(hTask)
		_, b := i.execOf(cTask)
		return a == "Interrupted" && b == "Interrupted"
	})
	t.Logf("interrupted after %s", time.Since(start).Round(time.Second))
	for _, task := range []string{hTask, cTask} {
		if s := i.taskState(task); s != "Blocked" {
			t.Fatalf("task %s, want Blocked", s)
		}
		if n := i.count(`SELECT count(*) FROM executions WHERE task_id = ?`, task); n != 1 {
			t.Fatalf("%d executions: the task was rerun", n)
		}
	}
	// The hung agent wakes up: its lease is gone and it is told to stop.
	text, isErr := hang.call("waves_heartbeat", map[string]any{})
	if !isErr || !strings.Contains(text, "lease_invalid") {
		t.Fatalf("hung agent heartbeat after expiry: %s", text)
	}
}

// Rate limit: the harness cannot continue and says so. The task waits for
// the human; nothing retries.
func TestAgentRateLimited(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	_, a := i.planned("ratelimit")
	task, _ := a.claim()
	a.must("waves_report_blocker", map[string]any{"reason": "rate limited by the provider until 14:00"}, nil)
	if s := i.taskState(task); s != "Blocked" {
		t.Fatalf("task %s", s)
	}
	var l struct {
		Tasks []any `json:"tasks"`
	}
	a.must("waves_list_available_tasks", map[string]any{}, &l)
	if len(l.Tasks) != 0 {
		t.Fatal("a blocked task is offered again")
	}
	exe, _ := i.execOf(task)
	if !attention(i.dashboard(), "task_blocked", exe) {
		t.Fatal("the blocker does not show in needs-attention")
	}
}

// Disconnect: the agent gives the task back. Unchanged workspace: Ready
// again. Changed workspace: Blocked for the human.
func TestAgentReleases(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	p, a := i.planned("release")
	task, _ := a.claim()
	var r struct {
		TaskState string `json:"task_state"`
	}
	a.must("waves_release_task", map[string]any{"reason": "switching machines"}, &r)
	if i.taskState(task) != "Ready" {
		t.Fatalf("unchanged release: task %s (%s)", i.taskState(task), r.TaskState)
	}
	task2, _ := a.claim()
	os.WriteFile(filepath.Join(p.dir, "half.txt"), []byte("half done\n"), 0o600)
	a.must("waves_release_task", map[string]any{"reason": "stuck"}, &r)
	if i.taskState(task2) != "Blocked" {
		t.Fatalf("changed release: task %s", i.taskState(task2))
	}
}

// Invalid proposal: every error comes back to the harness, the request stays
// open, and the repaired proposal becomes a draft. Nothing is approved.
func TestAgentInvalidProposal(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	p := i.newProject("invalid")
	p.dash.must("POST", "/projects/"+p.id+"/requirements", map[string]string{"kind": "text", "text": "x"}, nil)
	p.dash.must("POST", "/projects/"+p.id+"/planning-requests", nil, nil)
	a := p.agent(i)
	var req struct {
		ID string `json:"planning_request_id"`
	}
	a.must("waves_get_planning_request", map[string]any{}, &req)
	bad := strings.Replace(sampleProposal, `"acceptance_criteria": ["hello.txt says hello"]`, `"depends_on": ["nope"]`, 1)
	var prop map[string]any
	json.Unmarshal([]byte(bad), &prop)
	var res struct {
		Valid  bool `json:"valid"`
		Errors []struct {
			Code string `json:"code"`
			Path string `json:"path"`
		} `json:"errors"`
	}
	a.must("waves_submit_workflow_proposal", map[string]any{"planning_request_id": req.ID, "proposal": prop}, &res)
	codes := map[string]bool{}
	for _, e := range res.Errors {
		codes[e.Code] = true
	}
	if res.Valid || !codes["unknown_dependency"] || !codes["missing_acceptance"] {
		t.Fatalf("invalid proposal result: %+v", res)
	}
	var again map[string]any
	a.must("waves_get_planning_request", map[string]any{}, &again)
	if again["previous_errors"] == nil {
		t.Fatal("the errors are not returned to the harness")
	}
	json.Unmarshal([]byte(sampleProposal), &prop)
	a.must("waves_submit_workflow_proposal", map[string]any{"planning_request_id": req.ID, "proposal": prop}, &res)
	if !res.Valid {
		t.Fatalf("repaired proposal: %+v", res)
	}
	if n := i.count(`SELECT count(*) FROM workflow_versions WHERE state <> 'draft'`); n != 0 {
		t.Fatal("a proposal became more than a draft without the human")
	}
}

// Slow output: progress trickles in with heartbeats; the execution stays
// Running and every line reaches the log.
func TestAgentSlowOutput(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	_, a := i.planned("slow")
	task, _ := a.claim()
	for n := range 6 {
		a.must("waves_report_progress", map[string]any{"message": "step " + string(rune('a'+n))}, nil)
		a.must("waves_heartbeat", map[string]any{}, nil)
		time.Sleep(400 * time.Millisecond)
	}
	exe, st := i.execOf(task)
	if st != "Running" {
		t.Fatalf("execution %s", st)
	}
	c := i.dashboard()
	var d contract.ExecutionDetail
	c.must("GET", "/executions/"+exe, nil, &d)
	n := 0
	for _, e := range d.Events {
		if strings.HasPrefix(e.Message, "step ") {
			n++
		}
	}
	if n != 6 {
		t.Fatalf("%d of 6 progress lines in the execution detail: %+v", n, d.Events)
	}
}

// The CLI path out of an interruption, as found in the real run: cancel the
// execution, then retry the task. The messages say what really happened.
func TestCLIRecoversAnInterruption(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	_, a := i.planned("clirecover")
	task, _ := a.claim()
	i.kill9()
	i.start()
	exe, _ := i.execOf(task)
	if out, code := i.run("", "task", "retry", task, "crash"); code == 0 {
		t.Fatalf("retry with the interrupted execution still open: %s", out)
	}
	out, code := i.run("", "execution", "cancel", exe)
	if code != 0 || !strings.Contains(out, "is cancelled") || !strings.Contains(out, "Retry it with: waves task retry "+task) {
		t.Fatalf("cancel: exit %d: %s", code, out)
	}
	if out, code := i.run("", "task", "retry", task, "crash"); code != 0 || i.taskState(task) != "Ready" {
		t.Fatalf("retry: exit %d: %s (task %s)", code, out, i.taskState(task))
	}
}

// End to end: the human rejects the phase review, reopens the task
// with the passphrase, the agent sees the feedback and redoes the work, the
// validator runs again and only its latest run counts in the new review.
func TestPhaseRevisionAfterRejectedReview(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	prop := strings.Replace(sampleProposal, `"command": "test -f hello.txt"`, `"command": "grep -q again hello.txt"`, 1)
	p, a := i.plannedWith("revision", prop)
	task, _ := a.claim()
	os.WriteFile(filepath.Join(p.dir, "hello.txt"), []byte("hello\n"), 0o600)
	a.must("waves_submit_result", map[string]any{"summary": "wrote hello.txt", "status": "completed"}, nil)
	p.dash.mustDecide(p.pending("result"), "approve", nil)
	waitFor(t, 10*time.Second, "review", func() bool { _, st := i.phase(p.id, "p1"); return st == "Awaiting Phase Review" })
	phase, _ := i.phase(p.id, "p1")
	var rv contract.PhaseReview
	p.dash.must("GET", "/phases/"+phase+"/review", nil, &rv)
	if !rv.RequiresOverride {
		t.Fatal("the failing validator should need an override")
	}
	p.dash.mustDecide(p.pending("phase_completion"), "reject", map[string]string{"feedback": "hello.txt must say hello again"})
	body := map[string]any{"task_ids": []string{task}, "reason": "validator failed"}
	if st, code := p.dash.call("POST", "/phases/"+phase+"/reopen", body, nil, nil); st != 403 || code != "stepup_required" {
		t.Fatalf("reopen without passphrase: %d %s", st, code)
	}
	// Only tasks of this phase, and only top-level ones.
	other := i.str(`SELECT t.id FROM tasks t JOIN phases ph ON ph.id = t.phase_id WHERE ph.key = 'p2' AND t.phase_id IN (SELECT id FROM phases WHERE workflow_version_id = (SELECT workflow_version_id FROM phases WHERE id = ?))`, phase)
	if st, code := p.dash.call("POST", "/phases/"+phase+"/reopen", map[string]any{"task_ids": []string{other}, "reason": "x"}, nil, elev(p.dash.stepUp(""))); st != 422 {
		t.Fatalf("reopen a task of another phase: %d %s", st, code)
	}
	p.dash.must("POST", "/phases/"+phase+"/reopen", body, nil, elev(p.dash.stepUp("")))
	if _, st := i.phase(p.id, "p1"); st != "In Progress" || i.taskState(task) != "Ready" {
		t.Fatalf("after reopen: phase %s task %s", st, i.taskState(task))
	}
	a.claim()
	var ctx struct {
		Sections []struct{ Title, Content string } `json:"sections"`
	}
	a.must("waves_get_task_context", map[string]any{}, &ctx)
	seen := false
	for _, s := range ctx.Sections {
		seen = seen || (s.Title == "Changes requested at review" && strings.Contains(s.Content, "hello.txt must say hello again") && strings.Contains(s.Content, "passphrase confirmed"))
	}
	if !seen {
		t.Fatalf("the review feedback is not in the context: %+v", ctx.Sections)
	}
	os.WriteFile(filepath.Join(p.dir, "hello.txt"), []byte("hello again\n"), 0o600)
	a.must("waves_submit_result", map[string]any{"summary": "says hello again", "status": "completed"}, nil)
	p.dash.mustDecide(p.pending("result"), "approve", nil)
	waitFor(t, 10*time.Second, "second review", func() bool { _, st := i.phase(p.id, "p1"); return st == "Awaiting Phase Review" })
	p.dash.must("GET", "/phases/"+phase+"/review", nil, &rv)
	if rv.RequiresOverride {
		t.Fatalf("the old failed run still counts: %+v", rv.Validators)
	}
	p.dash.mustDecide(p.pending("phase_completion"), "approve", nil)
	if _, st := i.phase(p.id, "p1"); st != "Completed" {
		t.Fatalf("phase is %s", st)
	}
	if n := i.count(`SELECT count(*) FROM audit WHERE event = 'phase.tasks_reopened' AND actor_scope = 'human'`); n != 1 {
		t.Fatalf("%d reopen audit records", n)
	}
	// A completed phase cannot be reopened.
	if st, code := p.dash.call("POST", "/phases/"+phase+"/reopen", body, nil, elev(p.dash.stepUp(""))); st != 409 {
		t.Fatalf("reopen a completed phase: %d %s", st, code)
	}
}

// Security review M1: cancelling a reopened task (operator credential) must
// not quietly clear the failure that caused the revision, even when the
// validator passes on its next run.
func TestCancelledRedoStillNeedsOverride(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	// Fails the first time, passes afterwards: a flaky check.
	prop := strings.Replace(sampleProposal, `"command": "test -f hello.txt"`, `"command": "test -f .ran || { touch .ran; exit 1; }"`, 1)
	p, a := i.plannedWith("cancelredo", prop)
	task, _ := a.claim()
	a.must("waves_submit_result", map[string]any{"summary": "done", "status": "completed"}, nil)
	p.dash.mustDecide(p.pending("result"), "approve", nil)
	waitFor(t, 10*time.Second, "review", func() bool { _, st := i.phase(p.id, "p1"); return st == "Awaiting Phase Review" })
	phase, _ := i.phase(p.id, "p1")
	p.dash.mustDecide(p.pending("phase_completion"), "reject", nil)
	p.dash.must("POST", "/phases/"+phase+"/reopen", map[string]any{"task_ids": []string{task}, "reason": "redo"}, nil, elev(p.dash.stepUp("")))
	i.operator().must("POST", "/tasks/"+task+"/cancel", map[string]string{"reason": "not needed"}, nil)
	waitFor(t, 10*time.Second, "second review", func() bool { _, st := i.phase(p.id, "p1"); return st == "Awaiting Phase Review" })
	var rv contract.PhaseReview
	p.dash.must("GET", "/phases/"+phase+"/review", nil, &rv)
	if !rv.RequiresOverride {
		t.Fatalf("a cancelled redo cleared the failure: %+v", rv.Validators)
	}
}
