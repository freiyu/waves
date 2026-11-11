//go:build e2e

package e2e

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"waves/internal/api/contract"
)

// afterRestart checks what must hold after any crash: the database is
// intact, the audit chain verifies, and nothing is left running.
func (i *inst) afterRestart() *client {
	i.t.Helper()
	if ic := i.str(`PRAGMA integrity_check`); ic != "ok" {
		i.t.Fatalf("integrity_check: %s", ic)
	}
	c := i.dashboard()
	var s contract.StatusResponse
	c.must("GET", "/status", nil, &s)
	if s.Audit.State != "verified" {
		i.t.Fatalf("audit chain after the crash: %+v", s.Audit)
	}
	if n := i.count(`SELECT count(*) FROM executions WHERE state IN ('Starting','Running','Waiting Approval','Paused','Disconnected')`); n != 0 {
		i.t.Fatalf("%d executions still look alive after the restart", n)
	}
	if n := i.count(`SELECT count(*) FROM leases WHERE revoked_at IS NULL`); n != 0 {
		i.t.Fatalf("%d leases still active after the restart", n)
	}
	return c
}

// Crash point 1: kill -9 while an agent holds a lease. The execution is
// Interrupted, the task Blocked for the human, and nothing reruns.
func TestCrashWhileLeased(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	p, a := i.planned("leased")
	task, lease := a.claim()
	a.must("waves_report_progress", map[string]any{"message": "half way"}, nil)
	i.kill9()
	i.start()
	i.afterRestart()
	exe, st := i.execOf(task)
	if st != "Interrupted" || i.taskState(task) != "Blocked" {
		t.Fatalf("execution %s task %s, want Interrupted and Blocked", st, i.taskState(task))
	}
	if n := i.count(`SELECT count(*) FROM executions WHERE task_id = ?`, task); n != 1 {
		t.Fatalf("%d executions for the task: something reran it", n)
	}
	if n := i.count(`SELECT count(*) FROM execution_events WHERE execution_id = ?`, exe); n == 0 {
		t.Fatal("the progress log was lost")
	}
	// The old agent and a fresh one: the old lease is dead and nothing is offered.
	b := p.agent(i)
	if text, isErr := b.call("waves_heartbeat", map[string]any{"lease_token": lease}); !isErr {
		t.Fatalf("old lease still works after the crash: %s", text)
	}
	var l struct {
		Tasks []any `json:"tasks"`
	}
	b.must("waves_list_available_tasks", map[string]any{}, &l)
	if len(l.Tasks) != 0 {
		t.Fatalf("a blocked task is offered again: %+v", l.Tasks)
	}
	// Nothing starts on its own: still one execution a moment later.
	time.Sleep(200 * time.Millisecond)
	if n := i.count(`SELECT count(*) FROM executions WHERE task_id = ?`, task); n != 1 {
		t.Fatal("an execution started without the human")
	}
}

// Crash point 2: kill -9 while a validator command runs. After the restart
// the run is an error, the phase goes to review, nothing runs again, and the
// validator process does not survive.
func TestCrashDuringValidation(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	marker := fmt.Sprintf("wavese2emarker%d", time.Now().UnixNano())
	prop := strings.Replace(sampleProposal, `"command": "test -f hello.txt", "timeout_s": 30`, `"command": "sleep 25; echo `+marker+`", "timeout_s": 60`, 1)
	p, a := i.plannedWith("validating", prop)
	a.claim()
	os.WriteFile(filepath.Join(p.dir, "hello.txt"), []byte("hello\n"), 0o600)
	a.must("waves_submit_result", map[string]any{"summary": "done", "status": "completed"}, nil)
	p.dash.mustDecide(p.pending("result"), "approve", nil)
	waitFor(t, 10*time.Second, "the validator process", func() bool { return pgrep(marker) })
	i.kill9()
	i.start()
	c := i.afterRestart()
	waitFor(t, 10*time.Second, "phase review", func() bool { _, st := i.phase(p.id, "p1"); return st == "Awaiting Phase Review" })
	if st := i.str(`SELECT status FROM validation_runs ORDER BY rowid DESC LIMIT 1`); st != "error" {
		t.Fatalf("validator run is %q, want error", st)
	}
	if n := i.count(`SELECT count(*) FROM validation_runs`); n != 1 {
		t.Fatalf("%d validation runs: the validator ran again", n)
	}
	var rv contract.PhaseReview
	phase, _ := i.phase(p.id, "p1")
	c.must("GET", "/phases/"+phase+"/review", nil, &rv)
	if !rv.RequiresOverride {
		t.Fatal("an interrupted validator must need an override to approve")
	}
	time.Sleep(300 * time.Millisecond)
	if pgrep(marker) {
		exec.Command("pkill", "-f", marker).Run()
		t.Errorf("the validator process outlived the service crash and was not stopped after the restart")
	}
}

// Crash point 3: kill -9 under a burst of writes from several agents.
func TestCrashUnderWriteLoad(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	i.setLimit(3)
	var agents []*agent
	for n := range 3 {
		_, a := i.planned(fmt.Sprintf("load%d", n))
		a.claim()
		agents = append(agents, a)
	}
	// The workspace lock and the limit allow these claims; now flood.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for _, a := range agents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; ; k++ {
				select {
				case <-stop:
					return
				default:
				}
				a.call("waves_report_progress", map[string]any{"message": fmt.Sprintf("line %d", k)})
			}
		}()
	}
	time.Sleep(400 * time.Millisecond)
	i.kill9()
	close(stop)
	wg.Wait()
	i.start()
	i.afterRestart()
	if n := i.count(`SELECT count(*) FROM executions WHERE state = 'Interrupted'`); n != 3 {
		t.Fatalf("%d interrupted executions, want 3", n)
	}
}

// Crash point 4: kill -9 while a step-up is in flight. Afterwards the
// passphrase works, an elevation from before the crash is good once at most,
// and the chain verifies.
func TestCrashDuringStepUp(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	p, _, apr := i.gateTarget("stepup")
	el := p.dash.stepUp(apr)
	// The request dies with the service; its error is expected.
	go func() {
		req, _ := http.NewRequest("POST", i.url("/api/v1/auth/stepup"), strings.NewReader(`{"passphrase":"`+testPass+`","approval_id":"`+apr+`"}`)) // guard:allow fake test fixture
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", "waves_session="+p.dash.cookie)
		req.Header.Set("X-Waves-CSRF", p.dash.csrf)
		if res, err := http.DefaultClient.Do(req); err == nil {
			res.Body.Close()
		}
	}()
	time.Sleep(15 * time.Millisecond)
	i.kill9()
	i.start()
	c := i.afterRestart()
	var d contract.ApprovalDetail
	c.must("GET", "/approvals/"+apr, nil, &d)
	body := map[string]string{"decision": "approve", "subject_hash": d.SubjectHash}
	// The elevation belonged to the old session (still valid after a restart).
	if st, code := p.dash.call("POST", "/approvals/"+apr+"/decision", body, nil, elev(el)); st != 204 && st != 200 {
		t.Fatalf("decision with the pre-crash elevation: %d %s", st, code)
	}
	if st, _ := p.dash.call("POST", "/approvals/"+apr+"/decision", body, nil, elev(el)); st < 400 {
		t.Fatal("a single-use elevation worked twice across a crash")
	}
}

// Crash point 5: kill -9 of `waves restore` at many moments. The database
// is always there and intact afterwards: the old one or the restored one.
func TestCrashDuringRestore(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	i.newProject("before-backup")
	var b struct {
		Path string `json:"path"`
	}
	i.operator().must("POST", "/backup", map[string]string{}, &b)
	i.newProject("after-backup")
	i.stop()
	stages, restored := map[string]int{}, 0
	for n := range 20 {
		m, sl, err := openPTY()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bin, "restore", b.Path)
		cmd.Env = i.env
		cmd.Stdin, cmd.Stdout, cmd.Stderr = sl, sl, sl
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		sl.Close()
		go io.Copy(io.Discard, m)
		m.Write([]byte("restore\n"))
		time.Sleep(time.Duration(2*n) * time.Millisecond)
		cmd.Process.Kill()
		cmd.Wait()
		m.Close()
		if _, err := os.Stat(i.dbPath()); err != nil {
			t.Fatalf("after a kill at %d ms the database is missing: %v", 2*n, err)
		}
		if ic := i.str(`PRAGMA integrity_check`); ic != "ok" {
			t.Fatalf("after a kill at %d ms: integrity_check %s", 2*n, ic)
		}
		// Which stage the kill hit: before writing, after the temp copy, or after the swap.
		stage := "before"
		if _, err := os.Stat(i.dbPath() + ".restoring"); err == nil {
			stage = "temp written"
		}
		if m, _ := filepath.Glob(i.dbPath() + ".replaced-*"); len(m) > restored {
			stage, restored = "swapped", len(m)
		}
		stages[stage]++
	}
	t.Logf("kill stages over 20 runs: %v", stages)
	// A full restore still works and brings back the backed-up state.
	if out, err := i.tty([]string{"restore", b.Path}, "restore\n"); err != nil || !strings.Contains(out, "Restored.") {
		t.Fatalf("restore: %v %s", err, out)
	}
	if n := i.count(`SELECT count(*) FROM projects WHERE name = 'after-backup'`); n != 0 {
		t.Fatal("the restore did not bring back the backup")
	}
	i.start()
	i.afterRestart()
}

func pgrep(pattern string) bool {
	return exec.Command("pgrep", "-f", pattern).Run() == nil
}
