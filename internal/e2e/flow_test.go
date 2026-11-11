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

// The happy path end to end: plan, approve, claim, result, validation,
// phase review and completion, all through the real binary.
func TestHappyPathThroughTheBinary(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	p, a := i.planned("happy")
	_, st := i.phase(p.id, "p1")
	if st != "Ready for Execution" {
		t.Fatalf("phase 1 is %q", st)
	}
	task, _ := a.claim()
	if s := i.taskState(task); s != "In Progress" {
		t.Fatalf("task is %s after claim", s)
	}
	a.must("waves_get_task_context", map[string]any{}, nil)
	a.must("waves_heartbeat", map[string]any{}, nil)
	os.WriteFile(filepath.Join(p.dir, "hello.txt"), []byte("hello\n"), 0o600)
	a.must("waves_submit_result", map[string]any{"summary": "wrote hello.txt", "status": "completed"}, nil)
	p.dash.mustDecide(p.pending("result"), "approve", nil)
	waitFor(t, 10*time.Second, "phase review", func() bool { _, st := i.phase(p.id, "p1"); return st == "Awaiting Phase Review" })
	p.dash.mustDecide(p.pending("phase_completion"), "approve", nil)
	if _, st := i.phase(p.id, "p1"); st != "Completed" {
		t.Fatalf("phase 1 is %s", st)
	}
	if _, st := i.phase(p.id, "p2"); st != "Awaiting Approval" {
		t.Fatalf("phase 2 is %s, want its start gate", st)
	}
}

// The official CLI path works on a terminal and records how it was approved
// (AH-3, AH-7). The phase key must be typed.
func TestCLIApprovalOnATerminal(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	p, phase, apr := i.gateTarget("cli")
	var d contract.ApprovalDetail
	p.dash.must("GET", "/approvals/"+apr, nil, &d)
	key := strings.Fields(d.Subject)[0]
	if out, err := i.tty([]string{"approval", "review", apr}, "approve\nwrong-key\n"); err == nil || !strings.Contains(out, "did not match") {
		t.Fatalf("wrong phase key: %v %s", err, out)
	}
	out, err := i.tty([]string{"approval", "review", apr}, "approve\n"+key+"\n"+testPass+"\n")
	if err != nil || !strings.Contains(out, "Recorded: approve.") {
		t.Fatalf("CLI approve: %v\n%s", err, out)
	}
	if st := i.str(`SELECT state FROM phases WHERE id = ?`, phase); st != "Ready for Execution" {
		t.Fatalf("phase is %s", st)
	}
	if n := i.count(`SELECT count(*) FROM approvals WHERE id = ? AND decided_by_scope = 'human' AND stepup_method = 'passphrase' AND tty = 1 AND client = 'cli' AND source_addr IS NOT NULL`, apr); n != 1 {
		t.Fatal("approval lacks its AH-7 detail")
	}
}

// The dashboard on the real service: Playwright drives a browser against
// this instance (web/e2e-real). Needs node and the Playwright browsers.
func TestDashboardOnTheRealService(t *testing.T) {
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not on PATH")
	}
	i := newInst(t)
	i.setLimit(3)
	_, _, gate := i.gateTarget("gate")
	// A phase waiting for its review, so the dashboard's review page is walked on real data too.
	rp, ra := i.planned("inreview")
	ra.claim()
	os.WriteFile(filepath.Join(rp.dir, "hello.txt"), []byte("hello\n"), 0o600)
	ra.must("waves_submit_result", map[string]any{"summary": "wrote hello.txt", "status": "completed"}, nil)
	rp.dash.mustDecide(rp.pending("result"), "approve", nil)
	waitFor(t, 10*time.Second, "phase review", func() bool { _, st := i.phase(rp.id, "p1"); return st == "Awaiting Phase Review" })
	// A workflow waiting for its approval.
	wp := i.newProject("review")
	wp.dash.must("POST", "/projects/"+wp.id+"/requirements", map[string]string{"kind": "text", "text": "x"}, nil)
	wp.dash.must("POST", "/projects/"+wp.id+"/planning-requests", nil, nil)
	wa := wp.agent(i)
	var req struct {
		ID string `json:"planning_request_id"`
	}
	wa.must("waves_get_planning_request", map[string]any{}, &req)
	var prop map[string]any
	json.Unmarshal([]byte(sampleProposal), &prop)
	wa.must("waves_submit_workflow_proposal", map[string]any{"planning_request_id": req.ID, "proposal": prop}, nil)
	var d contract.ProjectDetail
	wp.dash.must("GET", "/projects/"+wp.id, nil, &d)
	wp.dash.must("POST", "/workflow-versions/"+d.Planning.LatestProposal.WorkflowVersionID+"/submit", nil, nil)
	// An interrupted execution, then (after the restart) a blocked task.
	_, ia := i.planned("interrupted")
	itask, _ := ia.claim()
	i.kill9()
	i.start()
	_, ba := i.planned("blocked")
	ba.claim()
	ba.must("waves_report_blocker", map[string]any{"reason": "rate limited"}, nil)
	iexe, _ := i.execOf(itask)
	ids, _ := json.Marshal(map[string]string{"gate": gate, "workflow": wp.pending("workflow"), "interrupted": iexe})
	cmd := exec.Command("npx", "playwright", "test", "-c", "playwright.real.config.ts")
	cmd.Dir = "../../web"
	cmd.Env = append(os.Environ(), "WAVES_URL="+i.url(""), "WAVES_OPERATOR_TOKEN="+i.op, "WAVES_PASS="+testPass, "WAVES_IDS="+string(ids))
	out, err := cmd.CombinedOutput()
	t.Logf("playwright:\n%s", out)
	if err != nil {
		t.Fatalf("playwright: %v", err)
	}
	if n := i.count(`SELECT count(*) FROM executions WHERE task_id = ?`, itask); n != 1 {
		t.Fatalf("%d executions after the human's retry: something started on its own", n)
	}
}
