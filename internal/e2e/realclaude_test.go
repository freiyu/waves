//go:build e2e && realclaude

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"waves/internal/api/contract"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][A-Z0-9]`)

// screen collects what a terminal program prints, without the escape sequences.
type screen struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *screen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}
func (s *screen) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ansi.ReplaceAllString(s.buf.String(), "")
}

// TestRealClaudeCode runs the real Claude Code (interactive, haiku) in a throwaway project that Waves connected, and checks
// the whole waiting story with the installed hooks: claim, the turn ends (Stop) and the harness is shown as waiting, it is
// not marked disconnected while it waits, a reply (UserPromptSubmit) ends the wait, the result is submitted, and /exit
// (SessionEnd) leaves no open wait. It costs a few cents, so it only runs when asked: WAVES_REAL_CLAUDE=1.
func TestRealClaudeCode(t *testing.T) {
	if os.Getenv("WAVES_REAL_CLAUDE") != "1" {
		t.Skip("set WAVES_REAL_CLAUDE=1 to run the real Claude Code (a few cents of model use)")
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not on PATH")
	}
	home, _ := os.UserHomeDir()
	linkBefore, _ := os.Readlink(filepath.Join(home, ".local", "bin", "claude"))
	t.Cleanup(func() { // an XDG override once moved this link into a test folder: put it back if it moved again
		if now, _ := os.Readlink(filepath.Join(home, ".local", "bin", "claude")); now != linkBefore && linkBefore != "" {
			os.Remove(filepath.Join(home, ".local", "bin", "claude"))
			os.Symlink(linkBefore, filepath.Join(home, ".local", "bin", "claude"))
			t.Errorf("the claude launcher link moved from %s to %s; it was put back", linkBefore, now)
		}
	})

	i := newInst(t)
	i.setHeartbeat(15)
	p, a := i.planned("real")
	a.kill()
	// Claude Code looks for its own files under XDG_DATA_HOME; the instance has its own, so point it at the real ones.
	os.Symlink(filepath.Join(home, ".local", "share", "claude"), filepath.Join(i.dir, "data", "claude"))
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "XDG_") {
			env = append(env, e)
		}
	}
	for _, e := range i.env {
		if strings.HasPrefix(e, "XDG_") {
			env = append(env, e)
		}
	}
	env = append(env, "DISABLE_AUTOUPDATER=1", "TERM=xterm-256color")

	m, s, err := openPTY()
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ws := struct{ Row, Col, X, Y uint16 }{40, 120, 0, 0}
	syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
	cmd := exec.Command(claude, "--setting-sources", "project,local", "--model", "haiku", "--allowedTools", "mcp__waves")
	cmd.Dir, cmd.Env = p.dir, env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = s, s, s
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	scr := &screen{}
	go io.Copy(scr, m)
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() { cmd.Process.Kill() })
	tail := func() string {
		x := scr.text()
		if len(x) > 2500 {
			x = x[len(x)-2500:]
		}
		select {
		case err := <-exited:
			exited <- err
			x += fmt.Sprintf("\n[claude exited: %v]", err)
		default:
		}
		return x
	}
	until := func(d time.Duration, what string, cond func() bool) {
		t.Helper()
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			if cond() {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Fatalf("timed out after %s waiting for %s\n--- screen ---\n%s", d, what, tail())
	}
	send := func(text string) { m.Write([]byte(text)); time.Sleep(600 * time.Millisecond); m.Write([]byte("\r")) }
	waitFor(t, 30*time.Second, "the trust question", func() bool { return strings.Contains(strings.ToLower(scr.text()), "trust") })
	time.Sleep(4 * time.Second) // let the dialog start reading keys
	m.Write([]byte("\x1b[B"))   // the second choice is "Yes, I trust this folder"
	time.Sleep(500 * time.Millisecond)
	m.Write([]byte("\r"))
	time.Sleep(8 * time.Second)

	state := func() string {
		task := i.str(`SELECT task_id FROM executions ORDER BY rowid DESC LIMIT 1`)
		if task == "" {
			return "none"
		}
		return i.waitingSince(task)
	}
	send("Use the Waves tools: call waves_list_available_tasks, then waves_claim_task for the first task. Do nothing else, write no file, and then stop and wait for my next message.")
	until(150*time.Second, "an execution that waits for the human", func() bool { return strings.HasPrefix(state(), "running|20") })
	t.Logf("claimed and waiting: %s", state())
	// Two silence limits at a 15 s heartbeat: still running because it waits, not because it is silent.
	time.Sleep(40 * time.Second)
	if got := state(); !strings.HasPrefix(got, "running|20") {
		t.Fatalf("after 40 s of waiting: %q\n%s", got, tail())
	}
	t.Logf("still waiting after 40 s of silence: %s", state())
	waitingAudits := i.count(`SELECT count(*) FROM audit WHERE event = 'execution.waiting'`)

	send("Now write the file hello.txt in the project containing the word hello, then call waves_submit_result with the summary 'wrote hello.txt' and the status 'completed'. Then stop.")
	until(150*time.Second, "the finished task", func() bool {
		return i.count(`SELECT count(*) FROM tasks WHERE state = 'Done'`) >= 1
	})
	if n := i.count(`SELECT count(*) FROM approvals WHERE category = 'result'`); n != 0 {
		t.Fatalf("%d result approvals were requested: a finished task is Done without one", n)
	}
	if n := i.count(`SELECT count(*) FROM audit WHERE event = 'execution.resumed'`); n < 1 {
		t.Fatalf("the reply did not end the wait (resumed records: %d)", n)
	}
	t.Logf("result submitted after the reply; wait records before it: %d, resumed: %d", waitingAudits, i.count(`SELECT count(*) FROM audit WHERE event = 'execution.resumed'`))

	send("/exit")
	select {
	case <-exited:
	case <-time.After(30 * time.Second):
		t.Fatalf("claude did not exit\n%s", tail())
	}
	time.Sleep(2 * time.Second)
	if n := i.count(`SELECT count(*) FROM execution_waits WHERE ended_at IS NULL`); n != 0 {
		t.Fatalf("%d waits still open after the session ended", n)
	}
	// What Claude Code did in the project: nothing but the result, no file written by the task.
	if out, _ := exec.Command("git", "-C", p.dir, "status", "--porcelain").Output(); strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(string(out), ".claude/", ""), ".mcp.json", "")) != "" {
		t.Logf("project status after the run: %s", out)
	}
	t.Logf("%s", fmt.Sprintf("events: waiting %d, resumed %d", i.count(`SELECT count(*) FROM audit WHERE event = 'execution.waiting'`), i.count(`SELECT count(*) FROM audit WHERE event = 'execution.resumed'`)))
}

// realClaude runs the real Claude Code headless (haiku) in a project that Waves connected, with throwaway XDG folders, and
// returns what it printed when it ended. Costs a few cents: only with WAVES_REAL_CLAUDE=1.
func realClaude(t *testing.T, i *inst, dir, prompt string, timeout time.Duration, during func()) string {
	t.Helper()
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not on PATH")
	}
	home, _ := os.UserHomeDir()
	linkBefore, _ := os.Readlink(filepath.Join(home, ".local", "bin", "claude"))
	t.Cleanup(func() { // an XDG override once moved this link into a test folder: put it back if it moved again
		if now, _ := os.Readlink(filepath.Join(home, ".local", "bin", "claude")); now != linkBefore && linkBefore != "" {
			os.Remove(filepath.Join(home, ".local", "bin", "claude"))
			os.Symlink(linkBefore, filepath.Join(home, ".local", "bin", "claude"))
			t.Errorf("the claude launcher link moved from %s to %s; it was put back", linkBefore, now)
		}
	})
	os.Symlink(filepath.Join(home, ".local", "share", "claude"), filepath.Join(i.dir, "data", "claude"))
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "XDG_") {
			env = append(env, e)
		}
	}
	for _, e := range i.env {
		if strings.HasPrefix(e, "XDG_") {
			env = append(env, e)
		}
	}
	env = append(env, "DISABLE_AUTOUPDATER=1")
	cmd := exec.Command(claude, "-p", prompt, "--setting-sources", "project,local", "--model", "haiku", "--allowedTools", "mcp__waves,Bash,Write,Edit", "--output-format", "text")
	cmd.Dir, cmd.Env = dir, env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if during != nil {
		go during()
	}
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("claude did not finish in %s\n%s", timeout, out.String())
	}
	return out.String()
}

// The whole story of 1.2 with the real Claude Code: three tasks of one phase one after another with no approval per task, one of
// them with a long foreground command (longer than the old 180 s limit), and one approval at the end of the phase.
func TestRealClaudeCodeSequenceWithALongTask(t *testing.T) {
	if os.Getenv("WAVES_REAL_CLAUDE") != "1" {
		t.Skip("set WAVES_REAL_CLAUDE=1 to run the real Claude Code (a few cents of model use)")
	}
	t.Parallel()
	i := newInst(t) // the default 30 s heartbeat: the old rules end a silent lease after 180 s
	p, a := i.plannedWith("seq", threeTaskProposal)
	a.kill()
	out := realClaude(t, i, p.dir, "Work through the Waves tasks of this project one after another. List the available tasks with the Waves tools, claim the first one, write the file its acceptance criteria name (a.txt, b.txt, c.txt) containing the word x, submit the result with status completed, then list the tasks again and repeat until none is available. Before you write the file of the SECOND task, run this in the foreground with the Bash tool (use timeout 300000, never run_in_background): python3 -c 'import time; time.sleep(200)'. Be brief.", 12*time.Minute, nil)
	t.Logf("claude said:\n%s", out)
	if n := i.count(`SELECT count(*) FROM tasks WHERE state = 'Done'`); n != 3 {
		t.Fatalf("%d of 3 tasks are Done", n)
	}
	if n := i.count(`SELECT count(*) FROM executions`); n != 3 {
		t.Fatalf("%d executions for 3 tasks", n)
	}
	if n := i.count(`SELECT count(*) FROM state_transitions WHERE entity_type = 'execution' AND to_state IN ('Disconnected','Interrupted')`); n != 0 {
		t.Fatalf("%d executions lost their lease during the run", n)
	}
	if n := i.count(`SELECT count(*) FROM approvals WHERE category = 'result'`); n != 0 {
		t.Fatalf("%d result approvals", n)
	}
	waitFor(t, 20*time.Second, "phase review", func() bool { _, st := i.phase(p.id, "p1"); return st == "Awaiting Phase Review" })
	p.dash.mustDecide(p.pending("phase_completion"), "approve", nil)
	if _, st := i.phase(p.id, "p1"); st != "Completed" {
		t.Fatalf("phase 1 is %s", st)
	}
}

// A new workflow version is approved while the real Claude Code works: its execution is cancelled, nothing is rerun, and its
// next call is refused.
func TestRealClaudeCodeIsStoppedByAReplacedWorkflow(t *testing.T) {
	if os.Getenv("WAVES_REAL_CLAUDE") != "1" {
		t.Skip("set WAVES_REAL_CLAUDE=1 to run the real Claude Code (a few cents of model use)")
	}
	t.Parallel()
	i := newInst(t)
	p, a := i.planned("replace")
	during := func() {
		deadline := time.Now().Add(120 * time.Second)
		for time.Now().Before(deadline) && i.count(`SELECT count(*) FROM executions WHERE state = 'Running'`) == 0 {
			time.Sleep(time.Second)
		}
		time.Sleep(15 * time.Second) // the long command is under way
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
	out := realClaude(t, i, p.dir, "Claim the first available Waves task. Then run this in the foreground with the Bash tool (timeout 300000, never run_in_background): python3 -c 'import time; time.sleep(90)'. Then write hello.txt containing hello and submit the result with status completed. If any Waves call says your lease is not valid, stop at once and say so. Be brief.", 8*time.Minute, during)
	t.Logf("claude said:\n%s", out)
	task := i.str(`SELECT task_id FROM executions ORDER BY rowid LIMIT 1`)
	if _, st := i.execOf(task); st != "Cancelled" {
		t.Fatalf("execution is %s, want Cancelled", st)
	}
	if s := i.taskState(task); s != "Cancelled" {
		t.Fatalf("task is %s, want Cancelled", s)
	}
	if n := i.count(`SELECT count(*) FROM executions WHERE task_id = ?`, task); n != 1 {
		t.Fatalf("%d executions: the task was rerun", n)
	}
}
