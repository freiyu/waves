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

// Crash: the bridge process dies. Waves sees the connection go at once: the execution is Disconnected, and when the session
// does not come back within the grace period it is Interrupted and the task Blocked. The lease cannot move to a new MCP
// session and nothing is rerun. Real timers: the grace period is 30 s.
func TestAgentCrash(t *testing.T) {
	if testing.Short() {
		t.Skip("the grace period is 30 s")
	}
	t.Parallel()
	i := newInst(t)
	cp, crash := i.planned("crash")
	cTask, cLease := crash.claim()
	start := time.Now()
	crash.kill()
	waitFor(t, 10*time.Second, "Disconnected", func() bool { _, st := i.execOf(cTask); return st == "Disconnected" })
	t.Logf("disconnected after %s", time.Since(start).Round(time.Second))
	// A new MCP session cannot take over the crashed agent's lease.
	other := cp.agent(i)
	if text, isErr := other.call("waves_heartbeat", map[string]any{"lease_token": cLease}); !isErr {
		t.Fatalf("a new session took over the lease: %s", text)
	}
	waitFor(t, 60*time.Second, "lease expiry", func() bool { _, st := i.execOf(cTask); return st == "Interrupted" })
	t.Logf("interrupted after %s", time.Since(start).Round(time.Second))
	if s := i.taskState(cTask); s != "Blocked" {
		t.Fatalf("task %s, want Blocked", s)
	}
	if n := i.count(`SELECT count(*) FROM executions WHERE task_id = ?`, cTask); n != 1 {
		t.Fatalf("%d executions: the task was rerun", n)
	}
	if text, isErr := other.call("waves_submit_result", map[string]any{"lease_token": cLease, "summary": "late", "status": "completed"}); !isErr || !strings.Contains(text, "lease_invalid") {
		t.Fatalf("a result after the lease ended: %s", text)
	}
}

// A connected agent that says nothing keeps its lease: a long tool call (a build, a test run) makes no call to Waves, and
// silence is not how a lease ends any more. With a 15 s heartbeat the old rules marked it Disconnected after 30 s.
func TestConnectedAgentThatIsSilentKeepsItsLease(t *testing.T) {
	if testing.Short() {
		t.Skip("waits 40 s of silence")
	}
	t.Parallel()
	i := newInst(t)
	i.setHeartbeat(15)
	_, a := i.planned("quiet")
	task, _ := a.claim()
	time.Sleep(40 * time.Second)
	if _, st := i.execOf(task); st != "Running" {
		t.Fatalf("a connected, silent agent's execution is %s after 40 s", st)
	}
	var hb struct {
		LeaseOK bool `json:"lease_ok"`
	}
	a.must("waves_heartbeat", map[string]any{}, &hb)
	if !hb.LeaseOK {
		t.Fatal("the lease is not valid after the silence")
	}
	a.must("waves_submit_result", map[string]any{"summary": "the long build is done", "status": "completed"}, nil)
	if s := i.taskState(task); s != "Done" {
		t.Fatalf("task %s after its result, want Done", s)
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

const threeTaskProposal = `{
  "schema_version": 1,
  "summary": "Three small tasks",
  "risks": [{"description": "none", "severity": "low"}],
  "phases": [
    {"key": "p1", "title": "Build", "objective": "Write three files", "deliverables": ["a.txt"],
     "validators": [{"type": "command", "name": "files exist", "command": "test -f a.txt", "timeout_s": 30}],
     "tasks": [{"key": "p1-t1", "title": "A", "acceptance_criteria": ["a.txt"]},
               {"key": "p1-t2", "title": "B", "depends_on": ["p1-t1"], "acceptance_criteria": ["b.txt"]},
               {"key": "p1-t3", "title": "C", "depends_on": ["p1-t2"], "acceptance_criteria": ["c.txt"]}]}
  ]
}`

// Tasks of a phase run one after another with no approval per task; the human decides once, at the end of the phase.
func TestTasksRunBackToBackAndThePhaseIsApprovedOnce(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	p, a := i.plannedWith("seq", threeTaskProposal)
	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		task, _ := a.claim()
		os.WriteFile(filepath.Join(p.dir, f), []byte("x\n"), 0o600)
		a.must("waves_submit_result", map[string]any{"summary": "wrote " + f, "status": "completed"}, nil)
		if s := i.taskState(task); s != "Done" {
			t.Fatalf("%s: task is %s after its result, want Done", f, s)
		}
		if n := i.count(`SELECT count(*) FROM approvals WHERE state = 'pending' AND category <> 'phase_completion'`); n != 0 {
			t.Fatalf("%d approvals wait after %s: the agent is held up by the human", n, f)
		}
	}
	waitFor(t, 10*time.Second, "phase review", func() bool { _, st := i.phase(p.id, "p1"); return st == "Awaiting Phase Review" })
	if n := i.count(`SELECT count(*) FROM approvals WHERE category = 'result'`); n != 0 {
		t.Fatalf("%d result approvals were requested", n)
	}
	// The review lists every task with what the agent said.
	var rv contract.PhaseReview
	phaseID, _ := i.phase(p.id, "p1")
	p.dash.must("GET", "/phases/"+phaseID+"/review", nil, &rv)
	if len(rv.Tasks) != 3 || rv.Tasks[2].Summary != "wrote c.txt" {
		t.Fatalf("review tasks: %+v", rv.Tasks)
	}
	// Neither the guard token nor the operator token without a passphrase step-up can give that one approval.
	for name, c := range map[string]*client{"guard": i.as(i.guard), "operator": i.operator()} {
		if status, _ := c.call("POST", "/approvals/"+p.pending("phase_completion")+"/decision", map[string]string{"decision": "approve", "override": "x"}, nil, nil); status < 400 {
			t.Fatalf("the %s token decided the phase: %d", name, status)
		}
	}
	p.dash.mustDecide(p.pending("phase_completion"), "approve", nil)
	if _, st := i.phase(p.id, "p1"); st != "Completed" {
		t.Fatalf("phase 1 is %s", st)
	}
}

// Approving a new workflow version while a task runs ends that work at once: the execution is cancelled, its lease is gone,
// the old task is Cancelled (not Ready: nothing is rerun) and the agent is told to stop on its next call.
func TestApprovingANewVersionCancelsRunningWork(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	p, a := i.planned("replace")
	task, _ := a.claim()
	exe, _ := i.execOf(task)
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
	v2 := d.Planning.LatestProposal.WorkflowVersionID
	p.dash.must("POST", "/workflow-versions/"+v2+"/submit", nil, nil)
	apr := p.pending("workflow")
	var det contract.ApprovalDetail
	p.dash.must("GET", "/approvals/"+apr, nil, &det)
	running := 0
	for _, w := range det.WillCancel {
		if w.ExecutionID == exe {
			running++
		}
	}
	if running != 1 || len(det.WillCancel) < 2 {
		t.Fatalf("the approval does not say what it cancels: %+v", det.WillCancel)
	}
	p.dash.mustDecide(apr, "approve", nil)
	if _, st := i.execOf(task); st != "Cancelled" {
		t.Fatalf("execution is %s, want Cancelled", st)
	}
	if s := i.taskState(task); s != "Cancelled" {
		t.Fatalf("old task is %s, want Cancelled", s)
	}
	if n := i.count(`SELECT count(*) FROM executions WHERE task_id = ?`, task); n != 1 {
		t.Fatalf("%d executions: the task was rerun", n)
	}
	if text, isErr := a.call("waves_heartbeat", map[string]any{}); !isErr || !strings.Contains(text, "lease_invalid") {
		t.Fatalf("the agent's next call: %s", text)
	}
	if text, isErr := a.call("waves_submit_result", map[string]any{"summary": "late", "status": "completed"}); !isErr || !strings.Contains(text, "lease_invalid") {
		t.Fatalf("a late result: %s", text)
	}
	p.pending("phase_start") // the new version waits at its own first gate
	if n := i.count(`SELECT count(*) FROM leases WHERE revoked_at IS NULL`); n != 0 {
		t.Fatalf("%d leases are still active", n)
	}
}
