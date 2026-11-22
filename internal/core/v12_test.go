package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// legacyResult puts a claimed task in the state a Waves 1.0.1 database holds for a submitted result: the execution Completed,
// the task In Review, a result approval pending. Waves 1.2 no longer creates this state, but it still decides it.
func (a *agentFlow) legacyResult(t *testing.T, exe, task string) {
	t.Helper()
	err := a.s.tx(a.ctx, func(tx *Tx) error {
		if _, err := a.s.transition(a.ctx, tx, "execution", exe, "Completed", sysActor, "result submitted", ""); err != nil {
			return err
		}
		if _, err := a.s.transition(a.ctx, tx, "task", task, "In Review", sysActor, "result needs review", ""); err != nil {
			return err
		}
		apr, err := a.s.requestApproval(a.ctx, tx, "result", "task", task, a.project, "agent", "", map[string]any{"summary": "legacy"})
		if err != nil {
			return err
		}
		return a.s.refreshSubjectHash(a.ctx, tx, apr)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// pending reports whether an approval of the category waits on the subject, without failing when there is none.
func (a *agentFlow) pending(category, subject string) bool {
	var n int
	a.db.R.QueryRow(`SELECT count(*) FROM approvals WHERE category = ? AND subject_id = ? AND state = 'pending'`, category, subject).Scan(&n)
	return n > 0
}

func (a *agentFlow) stateOfExecution(exe string) (state string, cancel int, revoked string) {
	a.db.R.QueryRow(`SELECT e.state, e.cancel_requested, COALESCE((SELECT l.revoke_reason FROM leases l WHERE l.execution_id = e.id ORDER BY l.rowid DESC LIMIT 1),'') FROM executions e WHERE e.id = ?`, exe).Scan(&state, &cancel, &revoked)
	return
}

// newVersion drafts and submits a second workflow version and returns its approval.
func (a *agentFlow) newVersion(t *testing.T) (version, approval string) {
	t.Helper()
	version = a.draft(t, a.p, a.project, goodProposal)
	apr, err := a.s.SubmitVersion(a.ctx, a.p, version)
	if err != nil {
		t.Fatal(err)
	}
	return version, apr
}

// the reason of a block does not outlive the block.
func TestBlockedReasonEndsWithTheBlock(t *testing.T) {
	a := startedFlow(t)
	t1 := a.task(t, "p1-t1")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", t1, "")
	if err := a.s.CancelExecution(a.ctx, a.p, r.ExecutionID); err != nil {
		t.Fatal(err)
	}
	reason := func() string {
		var x *string
		a.db.R.QueryRow(`SELECT blocked_reason FROM tasks WHERE id = ?`, t1).Scan(&x)
		if x == nil {
			return ""
		}
		return *x
	}
	if a.state(t, "tasks", t1) != "Blocked" || reason() != "interrupted" {
		t.Fatalf("after the cancel: %s %q", a.state(t, "tasks", t1), reason())
	}
	if err := a.s.RetryTask(a.ctx, a.p, t1); err != nil {
		t.Fatal(err)
	}
	if reason() != "" {
		t.Fatalf("the task is %s and still carries %q", a.state(t, "tasks", t1), reason())
	}
	if _, err := a.s.ClaimTask(a.ctx, a.agent, "s2", t1, ""); err != nil {
		t.Fatal(err)
	}
	if reason() != "" {
		t.Fatalf("claimed again and still carries %q", reason())
	}
}

// no approval per task. A failed result is never Done, and a result without a valid lease changes nothing.
func TestFailedResultIsNotDoneAndANeedsALease(t *testing.T) {
	a := startedFlow(t)
	t1, t2 := a.task(t, "p1-t1"), a.task(t, "p1-t2")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", t1, "")
	if err := a.s.SubmitResult(a.ctx, a.agent, "s1", "wls_not-the-token", "done", "completed", nil); code(err) != "lease_invalid" {
		t.Fatalf("result with a wrong lease token: %v", err)
	}
	if a.state(t, "tasks", t1) != "In Progress" {
		t.Fatalf("a refused result moved the task to %s", a.state(t, "tasks", t1))
	}
	if err := a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "it broke", "failed", nil); err != nil {
		t.Fatal(err)
	}
	if a.state(t, "tasks", t1) != "Blocked" || a.state(t, "tasks", t2) == "Ready" || a.pending("result", t1) {
		t.Fatalf("after a failed result: t1 %s, t2 %s", a.state(t, "tasks", t1), a.state(t, "tasks", t2))
	}
}

// the agent cannot approve what ends a phase, and the last task of a phase leads to validation and one phase approval.
func TestPhaseEndsWithOneApprovalAndOnlyAHumanGivesIt(t *testing.T) {
	a := startedFlow(t)
	a.doneTask1(t)
	t2 := a.task(t, "p1-t2")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", t2, "")
	if err := a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "docs", "completed", nil); err != nil {
		t.Fatal(err)
	}
	if a.state(t, "phases", a.phase1) != "Validation" {
		t.Fatalf("phase is %s", a.state(t, "phases", a.phase1))
	}
	if err := a.s.RunValidation(a.ctx, a.phase1); err != nil {
		t.Fatal(err)
	}
	comp := a.pendingApproval(t, "phase_completion", a.phase1)
	d, _ := a.s.ApprovalDetail(a.ctx, comp)
	for _, who := range []Principal{a.agent, a.guard} {
		if err := a.s.Decide(a.ctx, who, Decision{ApprovalID: comp, Decision: "approve", SubjectHash: d.SubjectHash, Override: "x"}); err == nil {
			t.Fatalf("%v approved a phase", who.Kind)
		}
	}
	if a.state(t, "phases", a.phase1) != "Awaiting Phase Review" {
		t.Fatalf("phase is %s", a.state(t, "phases", a.phase1))
	}
}

// approving a new workflow version ends the work under the old one at once, and nothing is rerun.
func TestApprovingANewVersionCancelsWhatRuns(t *testing.T) {
	a := startedFlow(t)
	t1 := a.task(t, "p1-t1")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", t1, "")
	// A risky action waiting for a decision is part of what runs.
	if _, err := a.s.RequestRiskyApproval(a.ctx, a.agent, "s1", r.LeaseToken, "rm -rf build", "clean", "build folder"); err != nil {
		t.Fatal(err)
	}
	old := a.version
	v2, apr := a.newVersion(t)
	d, _ := a.s.ApprovalDetail(a.ctx, apr)
	// It lists every open task of the old version, the running one with its execution.
	var openTasks int
	a.db.R.QueryRow(`SELECT count(*) FROM tasks t JOIN phases p ON p.id = t.phase_id WHERE p.workflow_version_id = ? AND t.state NOT IN ('Done','Cancelled')`, old).Scan(&openTasks)
	withExe := 0
	for _, w := range d.WillCancel {
		if w.Title == "" {
			t.Fatalf("a task without its title: %+v", w)
		}
		if w.ExecutionID != "" {
			withExe++
			if w.ExecutionID != r.ExecutionID || w.TaskCode != "P1-T1" {
				t.Fatalf("the running work is %+v", w)
			}
		}
	}
	if len(d.WillCancel) != openTasks || openTasks < 2 || withExe != 1 {
		t.Fatalf("the approval says it cancels %d tasks (%d running), the old version has %d open: %+v", len(d.WillCancel), withExe, openTasks, d.WillCancel)
	}
	if err := a.s.Decide(a.ctx, a.agent, Decision{ApprovalID: apr, Decision: "approve", SubjectHash: d.SubjectHash}); err == nil {
		t.Fatal("the agent approved a workflow version")
	}
	if st, _, _ := a.stateOfExecution(r.ExecutionID); st == "Cancelled" {
		t.Fatal("a refused decision cancelled the execution")
	}
	if err := a.decide(t, a.p, apr, "approve", false); err != nil {
		t.Fatalf("approve the new version while a task runs: %v", err)
	}
	if st, cancel, why := a.stateOfExecution(r.ExecutionID); st != "Cancelled" || cancel != 1 || why != "cancelled" {
		t.Fatalf("execution: %s cancel_requested=%d lease %q", st, cancel, why)
	}
	if a.state(t, "tasks", t1) != "Cancelled" {
		t.Fatalf("the old task is %s, want Cancelled (never Ready: nothing is rerun)", a.state(t, "tasks", t1))
	}
	if a.state(t, "workflow_versions", old) != "superseded" || a.state(t, "workflow_versions", v2) != "approved" {
		t.Fatalf("versions: %s %s", a.state(t, "workflow_versions", old), a.state(t, "workflow_versions", v2))
	}
	var recorded int
	a.db.R.QueryRow(`SELECT count(*) FROM audit WHERE event = 'approval.cancelled'`).Scan(&recorded)
	if recorded != 1 {
		t.Fatalf("%d audit records for the withdrawn risky-action approval, want 1", recorded)
	}
	var open int
	a.db.R.QueryRow(`SELECT count(*) FROM approvals WHERE state = 'pending' AND category IN ('risky_action','result','phase_completion')`).Scan(&open)
	if open != 0 {
		t.Fatalf("%d approvals of the old version are still pending", open)
	}
	// The agent's next call is refused: it stops, and the human is not asked anything about the old task.
	if _, err := a.s.Heartbeat(a.ctx, a.agent, "s1", r.LeaseToken); code(err) != "lease_invalid" {
		t.Fatalf("heartbeat after the cancel: %v", err)
	}
	if err := a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "late", "completed", nil); code(err) != "lease_invalid" {
		t.Fatalf("result after the cancel: %v", err)
	}
	// The new version starts at its own first gate.
	var first string
	a.db.R.QueryRow(`SELECT id FROM phases WHERE workflow_version_id = ? AND position = 1`, v2).Scan(&first)
	if a.state(t, "phases", first) != "Awaiting Approval" || !a.pending("phase_start", first) {
		t.Fatalf("new phase 1 is %s", a.state(t, "phases", first))
	}
}

// a validation that finishes after its version was replaced does not ask for a review of the old phase.
func TestValidationOfAReplacedVersionAsksForNothing(t *testing.T) {
	a := startedFlow(t)
	a.doneTask1(t)
	t2 := a.task(t, "p1-t2")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", t2, "")
	a.db.W.Exec(`UPDATE validators SET command = 'touch validation-started && sleep 2' WHERE type = 'command'`)
	if err := a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "docs", "completed", nil); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- a.s.RunValidation(a.ctx, a.phase1) }()
	for i := 0; i < 100; i++ { // the validator is running once it has left its marker
		if _, err := os.Stat(filepath.Join(a.root, "validation-started")); err == nil {
			break
		}
		if i == 99 {
			t.Fatal("the validator never started")
		}
		time.Sleep(50 * time.Millisecond)
	}
	_, apr := a.newVersion(t)
	if err := a.decide(t, a.p, apr, "approve", false); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if a.pending("phase_completion", a.phase1) || a.state(t, "phases", a.phase1) == "Awaiting Phase Review" {
		t.Fatalf("the old phase asked for a review: %s", a.state(t, "phases", a.phase1))
	}
	// A job still queued for the old phase is dropped without an error.
	if err := a.s.RunValidation(a.ctx, a.phase1); err != nil {
		t.Fatalf("queued validation of a replaced version: %v", err)
	}
}

// a lease is held by the connection of its session, not by silence.

func (a *agentFlow) lease(t *testing.T, exe string) (revoked, reason string) {
	t.Helper()
	var r, why *string
	a.db.R.QueryRow(`SELECT revoked_at, revoke_reason FROM leases WHERE execution_id = ?`, exe).Scan(&r, &why)
	if r != nil {
		revoked = *r
	}
	if why != nil {
		reason = *why
	}
	return
}

func TestConnectedLeaseHasNoDeadline(t *testing.T) {
	a := startedFlow(t)
	if err := a.s.StreamOpen(a.ctx, a.agent, "s1"); err != nil {
		t.Fatal(err)
	}
	r, err := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t1"), "")
	if err != nil {
		t.Fatal(err)
	}
	// One tool call that runs for half an hour says nothing to Waves: the heartbeat rules would have ended this lease long ago.
	a.clock.Add(30 * time.Minute)
	a.s.expireLeases(a.ctx)
	if st, _, _ := a.stateOfExecution(r.ExecutionID); st != "Running" {
		t.Fatalf("a connected session's execution is %s after 30 silent minutes", st)
	}
	if rv, _ := a.lease(t, r.ExecutionID); rv != "" {
		t.Fatal("the lease of a connected session ended")
	}
	if _, err := a.s.Heartbeat(a.ctx, a.agent, "s1", r.LeaseToken); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if err := a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "slow build done", "completed", nil); err != nil {
		t.Fatalf("the result of a long task: %v", err)
	}
	if a.state(t, "tasks", a.task(t, "p1-t1")) != "Done" {
		t.Fatal("not done")
	}
}

func TestLeaseWithoutAStreamKeepsTheHeartbeatRules(t *testing.T) {
	a := startedFlow(t)
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t1"), "")
	a.clock.Add(10 * time.Minute)
	a.s.expireLeases(a.ctx)
	if st, _, _ := a.stateOfExecution(r.ExecutionID); st != "Interrupted" {
		t.Fatalf("without a stream the silent execution is %s, want Interrupted", st)
	}
}

func TestSessionLeavingStartsTheGraceAndNothingIsRerun(t *testing.T) {
	a := startedFlow(t)
	a.s.StreamOpen(a.ctx, a.agent, "s1")
	t1 := a.task(t, "p1-t1")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", t1, "")
	if err := a.s.StreamClose(a.ctx, a.agent, "s1"); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := a.stateOfExecution(r.ExecutionID); st != "Disconnected" {
		t.Fatalf("right after the session left: %s", st)
	}
	a.clock.Add(presenceGrace - time.Second)
	a.s.expireLeases(a.ctx)
	if st, _, _ := a.stateOfExecution(r.ExecutionID); st != "Disconnected" {
		t.Fatalf("inside the grace period: %s", st)
	}
	a.clock.Add(2 * time.Second)
	a.s.expireLeases(a.ctx)
	st, _, _ := a.stateOfExecution(r.ExecutionID)
	if rv, why := a.lease(t, r.ExecutionID); st != "Interrupted" || rv == "" || why != "expired" {
		t.Fatalf("after the grace period: %s, lease revoked=%q reason=%q", st, rv, why)
	}
	if a.state(t, "tasks", t1) != "Blocked" {
		t.Fatalf("the task is %s, want Blocked for the human; it is never Ready on its own", a.state(t, "tasks", t1))
	}
}

func TestSessionBackWithinTheGraceIsRunningAgain(t *testing.T) {
	a := startedFlow(t)
	a.s.StreamOpen(a.ctx, a.agent, "s1")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t1"), "")
	a.s.StreamClose(a.ctx, a.agent, "s1")
	a.clock.Add(10 * time.Second)
	if err := a.s.StreamOpen(a.ctx, a.agent, "s1"); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := a.stateOfExecution(r.ExecutionID); st != "Running" {
		t.Fatalf("after it came back: %s", st)
	}
	a.clock.Add(time.Hour)
	a.s.expireLeases(a.ctx)
	if st, _, _ := a.stateOfExecution(r.ExecutionID); st != "Running" {
		t.Fatalf("an hour later: %s", st)
	}
}

func TestASecondStreamKeepsTheSessionConnected(t *testing.T) {
	a := startedFlow(t)
	a.s.StreamOpen(a.ctx, a.agent, "s1")
	a.s.StreamOpen(a.ctx, a.agent, "s1")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t1"), "")
	a.s.StreamClose(a.ctx, a.agent, "s1")
	if st, _, _ := a.stateOfExecution(r.ExecutionID); st != "Running" {
		t.Fatalf("one of two streams closed: %s", st)
	}
	a.s.StreamClose(a.ctx, a.agent, "s1")
	if st, _, _ := a.stateOfExecution(r.ExecutionID); st != "Disconnected" {
		t.Fatalf("the last stream closed: %s", st)
	}
}

// Gate-bypass: a stream holds a lease only for the credential, project and session it belongs to.
func TestOnlyTheProjectsMCPCredentialCanHoldALease(t *testing.T) {
	a := startedFlow(t)
	for name, who := range map[string]Principal{"guard": a.guard, "operator": a.p} {
		if err := a.s.StreamOpen(a.ctx, who, "s1"); err == nil {
			t.Fatalf("a %s credential registered a stream", name)
		}
	}
	// A credential of another project names this project's session id: it holds nothing of this project.
	foreign := a.agent
	foreign.ProjectID = "prj_somebody_else"
	if err := a.s.StreamOpen(a.ctx, foreign, "s1"); err != nil {
		t.Fatal(err)
	}
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t1"), "")
	a.clock.Add(10 * time.Minute)
	a.s.expireLeases(a.ctx)
	if st, _, _ := a.stateOfExecution(r.ExecutionID); st != "Interrupted" {
		t.Fatalf("a stream of another project held the lease: %s", st)
	}
	// And another session's stream of the same project does not hold it either.
	a2 := startedFlow(t)
	a2.s.StreamOpen(a2.ctx, a2.agent, "other-session")
	r2, _ := a2.s.ClaimTask(a2.ctx, a2.agent, "s1", a2.task(t, "p1-t1"), "")
	a2.clock.Add(10 * time.Minute)
	a2.s.expireLeases(a2.ctx)
	if st, _, _ := a2.stateOfExecution(r2.ExecutionID); st != "Interrupted" {
		t.Fatalf("a stream of another session held the lease: %s", st)
	}
}

// The human decides on what the approval shows: work that changes after the screen was opened makes the approval stale.
func TestNewVersionApprovalGoesStaleWhenOpenWorkChanges(t *testing.T) {
	a := startedFlow(t)
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t1"), "")
	_, apr := a.newVersion(t)
	d, _ := a.s.ApprovalDetail(a.ctx, apr)
	if err := a.s.CancelExecution(a.ctx, a.p, r.ExecutionID); err != nil {
		t.Fatal(err)
	}
	a.clock.Add(time.Second)
	el, _ := a.s.StepUp(a.ctx, a.p, testPass, apr)
	if err := a.s.Decide(a.ctx, a.p, Decision{ApprovalID: apr, Decision: "approve", SubjectHash: d.SubjectHash, Elevation: el.Secret}); code(err) != "stale_subject" {
		t.Fatalf("approving a stale screen: %v", err)
	}
	if err := a.decide(t, a.p, apr, "approve", false); err != nil {
		t.Fatalf("approving after a reload: %v", err)
	}
}

// Withdrawing the approvals of a replaced version is recorded and shown, also when no task or execution was open.
func TestReplacedVersionsApprovalsAreWithdrawnAndRecorded(t *testing.T) {
	a := startedFlow(t)
	a.doneTask1(t)
	t2 := a.task(t, "p1-t2")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", t2, "")
	a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "docs", "completed", nil)
	a.db.W.Exec(`UPDATE validators SET command = 'true' WHERE type = 'command'`)
	if err := a.s.RunValidation(a.ctx, a.phase1); err != nil {
		t.Fatal(err)
	}
	comp := a.pendingApproval(t, "phase_completion", a.phase1)
	_, apr := a.newVersion(t)
	if err := a.decide(t, a.p, apr, "approve", false); err != nil {
		t.Fatal(err)
	}
	var st string
	a.db.R.QueryRow(`SELECT state FROM approvals WHERE id = ?`, comp).Scan(&st)
	if st != "cancelled" {
		t.Fatalf("the review approval of the old phase is %s, want cancelled", st)
	}
	var n int
	a.db.R.QueryRow(`SELECT count(*) FROM audit WHERE event = 'approval.cancelled' AND subject = ?`, comp).Scan(&n)
	if n != 1 {
		t.Fatalf("%d audit records for the withdrawn approval", n)
	}
	if err := a.s.Decide(a.ctx, a.p, Decision{ApprovalID: comp, Decision: "approve", Override: "x"}); err == nil {
		t.Fatal("a withdrawn approval was decided")
	}
}

// Revoking the project's credential lets go of its connected sessions: a stream opened with it holds nothing afterwards.
func TestRevokingTheCredentialReleasesConnectedSessions(t *testing.T) {
	a := startedFlow(t)
	a.s.StreamOpen(a.ctx, a.agent, "s1")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t1"), "")
	err := a.s.tx(a.ctx, func(tx *Tx) error { return a.s.endPresence(a.ctx, tx, a.project) })
	if err != nil {
		t.Fatal(err)
	}
	if a.s.SessionConnected(a.project, "s1") {
		t.Fatal("the session is still counted")
	}
	if st, _, _ := a.stateOfExecution(r.ExecutionID); st != "Disconnected" {
		t.Fatalf("after the release: %s", st)
	}
	a.s.StreamClose(a.ctx, a.agent, "s1") // the old stream ends later: nothing more happens
	a.clock.Add(presenceGrace + time.Second)
	a.s.expireLeases(a.ctx)
	if st, _, _ := a.stateOfExecution(r.ExecutionID); st != "Interrupted" {
		t.Fatalf("after the grace period: %s", st)
	}
}

// A stream whose registration failed is not counted, and a lease held by a connection that is gone is released by the loop.
func TestLostStreamEventsDoNotHoldALeaseForever(t *testing.T) {
	a := startedFlow(t)
	cancelled, cancel := context.WithCancel(a.ctx)
	cancel()
	if err := a.s.StreamOpen(cancelled, a.agent, "s1"); err == nil {
		t.Fatal("a stream opened on a cancelled context")
	}
	if a.s.SessionConnected(a.project, "s1") {
		t.Fatal("a stream that failed to open is counted")
	}
	// The count is lost while the lease carries the deadline of a connected session (the close failed).
	a.s.StreamOpen(a.ctx, a.agent, "s2")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s2", a.task(t, "p1-t1"), "")
	a.s.pres.done(a.project, "s2")
	a.s.expireLeases(a.ctx)
	if st, _, _ := a.stateOfExecution(r.ExecutionID); st != "Disconnected" {
		t.Fatalf("a lease held by a lost connection: %s", st)
	}
	a.clock.Add(presenceGrace + time.Second)
	a.s.expireLeases(a.ctx)
	if st, _, _ := a.stateOfExecution(r.ExecutionID); st != "Interrupted" {
		t.Fatalf("after the grace period: %s", st)
	}
}

// A completed result says what was done.
func TestCompletedResultNeedsASummary(t *testing.T) {
	a := startedFlow(t)
	t1 := a.task(t, "p1-t1")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", t1, "")
	if err := a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "   ", "completed", nil); code(err) != "validation_failed" {
		t.Fatalf("a completed result without a summary: %v", err)
	}
	if a.state(t, "tasks", t1) != "In Progress" {
		t.Fatalf("a refused result moved the task to %s", a.state(t, "tasks", t1))
	}
	if err := a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "looked at it", "completed", nil); err != nil {
		t.Fatal(err)
	}
}

// The commands of a replaced workflow version do not run, and its phase in validation goes back to work.
func TestReplacedVersionRunsNoValidatorCommand(t *testing.T) {
	a := startedFlow(t)
	marker := filepath.Join(a.root, "validator-ran")
	a.db.W.Exec(`UPDATE validators SET command = 'touch validator-ran' WHERE type = 'command'`)
	a.doneTask1(t)
	t2 := a.task(t, "p1-t2")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", t2, "")
	a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "docs", "completed", nil)
	if a.state(t, "phases", a.phase1) != "Validation" {
		t.Fatalf("phase is %s", a.state(t, "phases", a.phase1))
	}
	_, apr := a.newVersion(t)
	if err := a.decide(t, a.p, apr, "approve", false); err != nil {
		t.Fatal(err)
	}
	if a.state(t, "phases", a.phase1) != "In Progress" {
		t.Fatalf("the replaced phase is %s, want In Progress so that nothing queues it again", a.state(t, "phases", a.phase1))
	}
	if err := a.s.RunValidation(a.ctx, a.phase1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a validator command of a replaced version ran")
	}
}

// A heartbeat of a connected session never hands out the deadline that stands for "held".
func TestHeartbeatOfAConnectedSessionHasANormalDeadline(t *testing.T) {
	a := startedFlow(t)
	a.s.StreamOpen(a.ctx, a.agent, "s1")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t1"), "")
	for i := 0; i < 2; i++ { // the second one is too soon to renew
		hb, err := a.s.Heartbeat(a.ctx, a.agent, "s1", r.LeaseToken)
		if err != nil {
			t.Fatal(err)
		}
		if hb.ExpiresAt.After(a.clock.Now().Add(time.Hour)) {
			t.Fatalf("heartbeat %d returned a deadline of %s", i+1, hb.ExpiresAt)
		}
	}
}

// Accepting partial work is the operator's decision, never an agent's, and it does not finish the phase.
func TestOnlyTheOperatorAcceptsPartialWorkAndThePhaseStaysOpen(t *testing.T) {
	a := startedFlow(t)
	t1 := a.task(t, "p1-t1")
	r, _ := a.s.ClaimTask(a.ctx, a.agent, "s1", t1, "")
	a.clock.Add(10 * time.Minute)
	a.s.expireLeases(a.ctx) // no stream: the heartbeat rules interrupt it
	for name, who := range map[string]Principal{"agent": a.agent, "guard": a.guard} {
		if err := a.s.ResolveExecution(a.ctx, who, r.ExecutionID, "complete_partial"); err == nil {
			t.Fatalf("a %s credential accepted partial work", name)
		}
	}
	if a.state(t, "tasks", t1) != "Blocked" {
		t.Fatalf("a refused decision moved the task to %s", a.state(t, "tasks", t1))
	}
	if err := a.s.ResolveExecution(a.ctx, a.p, r.ExecutionID, "complete_partial"); err != nil {
		t.Fatal(err)
	}
	if a.state(t, "tasks", t1) != "Done" || a.state(t, "phases", a.phase1) == "Completed" || a.pending("phase_completion", a.phase1) {
		t.Fatalf("task %s, phase %s", a.state(t, "tasks", t1), a.state(t, "phases", a.phase1))
	}
}
