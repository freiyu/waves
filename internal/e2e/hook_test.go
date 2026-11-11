//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// installedHook returns the PreToolUse command `waves agent connect` wrote
// into the project's .claude/settings.local.json.
func (p *project) installedHook(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(p.dir, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Hooks struct {
			PreToolUse []struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	json.Unmarshal(b, &s)
	for _, e := range s.Hooks.PreToolUse {
		for _, h := range e.Hooks {
			if strings.Contains(h.Command, " hook --policy ") {
				return h.Command
			}
		}
	}
	t.Fatalf("no Waves hook in %s", b)
	return ""
}

// runHook runs the installed hook command the way Claude Code does: through
// a shell, with the event on stdin. It returns the exit code and the time.
func (i *inst) runHook(command string, ev map[string]any) (int, time.Duration) {
	b, _ := json.Marshal(ev)
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Env = i.env
	cmd.Stdin = strings.NewReader(string(b))
	start := time.Now()
	err := cmd.Run()
	d := time.Since(start)
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), d
	}
	if err != nil {
		i.t.Fatal(err)
	}
	return 0, d
}

func writeEvent(session, cwd, target, toolUse string) map[string]any {
	return map[string]any{"session_id": session, "cwd": cwd, "hook_event_name": "PreToolUse", "tool_name": "Write", "tool_use_id": toolUse,
		"tool_input": map[string]any{"file_path": target, "content": "x"}}
}

// setPolicy changes the project's guard policy (a human action) and returns
// the reinstalled hook command.
func (p *project) setPolicy(t *testing.T, policy string, unmanaged ...string) string {
	t.Helper()
	body := map[string]string{"guard_policy": policy}
	if len(unmanaged) > 0 {
		body["unmanaged_sessions"] = unmanaged[0]
	}
	p.dash.must("PATCH", "/projects/"+p.id+"/settings", body, nil, elev(p.dash.stepUp("")))
	cmd := p.installedHook(t)
	if !strings.Contains(cmd, "--policy "+policy) {
		t.Fatalf("hook not rewritten for %s: %s", policy, cmd)
	}
	return cmd
}

func TestHookPolicies(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	p, a := i.planned("hook")
	target := filepath.Join(p.dir, "hello.txt")
	session := "sess-" + p.id

	// audit (the default) with unmanaged sessions allowed (the default): a
	// session Waves does not know writes, and the call is recorded.
	audit := p.installedHook(t)
	if code, _ := i.runHook(audit, writeEvent("sess-manual", p.dir, target, "toolu_a0")); code != 0 {
		t.Fatalf("audit blocked a write: exit %d", code)
	}
	if n := i.count(`SELECT count(*) FROM guard_decisions WHERE session_id = 'sess-manual' AND reason = 'unmanaged' AND decision = 'allow'`); n != 1 {
		t.Fatalf("unmanaged write recorded %d times", n)
	}
	// Unmanaged sessions denied, still audit: allowed, but the would-be denial is recorded.
	audit = p.setPolicy(t, "audit", "deny")
	if code, _ := i.runHook(audit, writeEvent(session, p.dir, target, "toolu_a1")); code != 0 {
		t.Fatalf("audit blocked a write: exit %d", code)
	}
	if n := i.count(`SELECT count(*) FROM guard_decisions WHERE session_id = ? AND would_deny = 1 AND decision = 'allow'`, session); n != 1 {
		t.Fatalf("audit recorded %d would-be denials", n)
	}

	enforce := p.setPolicy(t, "enforce")
	// No lease: denied, exit 2 (the only code that blocks in Claude Code).
	if code, _ := i.runHook(enforce, writeEvent(session, p.dir, target, "toolu_e1")); code != 2 {
		t.Fatalf("enforce without a lease: exit %d", code)
	}
	// Claim through MCP; the hook event of the claim call binds this session to the lease.
	a.claim()
	claimUse := fmt.Sprintf("toolu_%s_%d", p.id, a.tool)
	if code, _ := i.runHook(enforce, map[string]any{"session_id": session, "cwd": p.dir, "hook_event_name": "PreToolUse",
		"tool_name": "mcp__waves__waves_claim_task", "tool_use_id": claimUse, "tool_input": map[string]any{}}); code != 0 {
		t.Fatalf("the Waves tool itself was blocked: exit %d", code)
	}
	if code, _ := i.runHook(enforce, writeEvent(session, p.dir, target, "toolu_e2")); code != 0 {
		t.Fatalf("enforce with a bound lease: exit %d", code)
	}
	// Another session, a protected file, and a path outside the workspace stay denied.
	if code, _ := i.runHook(enforce, writeEvent("sess-other", p.dir, target, "toolu_e3")); code != 2 {
		t.Fatalf("another session wrote under the lease: exit %d", code)
	}
	if code, _ := i.runHook(enforce, writeEvent(session, p.dir, filepath.Join(p.dir, ".claude", "settings.local.json"), "toolu_e4")); code != 2 {
		t.Fatalf("the hook's own settings were writable: exit %d", code)
	}
	if code, _ := i.runHook(enforce, writeEvent(session, p.dir, filepath.Join(p.dir, ".mcp.json"), "toolu_e5")); code != 2 {
		t.Fatalf(".mcp.json was writable: exit %d", code)
	}
	if code, _ := i.runHook(enforce, writeEvent(session, p.dir, "/tmp/outside-"+p.id, "toolu_e7")); code != 2 {
		t.Fatalf("a write outside the workspace was allowed: exit %d", code)
	}
	// The lease ends: the same session is denied again.
	a.must("waves_submit_result", map[string]any{"summary": "done", "status": "completed"}, nil)
	if code, _ := i.runHook(enforce, writeEvent(session, p.dir, target, "toolu_e6")); code != 2 {
		t.Fatalf("write after the lease ended: exit %d", code)
	}

	off := p.setPolicy(t, "off")
	if code, _ := i.runHook(off, writeEvent("sess-none", p.dir, target, "toolu_o1")); code != 0 {
		t.Fatalf("off blocked: exit %d", code)
	}
}

// Service down and service hanging: enforce fails closed (exit 2), audit
// stays out of the way. A hanging service is cut off by the 3 s deadline,
// well inside Claude Code's 5 s hook timeout.
func TestHookFailsClosed(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	p := i.newProject("closed")
	audit := p.installedHook(t)
	enforce := p.setPolicy(t, "enforce", "deny")
	target := filepath.Join(p.dir, "x.txt")
	ev := writeEvent("sess", p.dir, target, "toolu_1")
	i.stop()
	if code, _ := i.runHook(enforce, ev); code != 2 {
		t.Fatalf("enforce, service down: exit %d", code)
	}
	if code, _ := i.runHook(audit, ev); code != 0 {
		t.Fatalf("audit, service down: exit %d", code)
	}
	// A listener that accepts and never answers.
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", i.port))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	code, d := i.runHook(enforce, ev)
	if code != 2 || d > 4*time.Second {
		t.Fatalf("enforce, service hangs: exit %d after %s", code, d)
	}
	t.Logf("enforce, hanging service: exit 2 after %s", d.Round(time.Millisecond))
	if code, d := i.runHook(audit, ev); code != 0 || d > 4*time.Second {
		t.Fatalf("audit, service hangs: exit %d after %s", code, d)
	}
	// Garbage on stdin and a missing guard token also fail closed.
	cmd := exec.Command("/bin/sh", "-c", enforce)
	cmd.Env, cmd.Stdin = i.env, strings.NewReader("not json")
	if err := cmd.Run(); cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 2 {
		t.Fatalf("enforce with garbage on stdin: %v, want exit 2", err)
	}
	os.Rename(filepath.Join(i.cfgDir(), "guard.token"), filepath.Join(i.cfgDir(), "guard.token.gone"))
	if code, _ := i.runHook(enforce, ev); code != 2 {
		t.Fatalf("enforce without a guard token: exit %d", code)
	}
	// The installed hook names the binary by absolute path; a missing binary also blocks.
	if !strings.Contains(enforce, bin) {
		t.Fatalf("hook does not name the binary: %s", enforce)
	}
	broken := strings.Replace(enforce, bin, filepath.Join(i.dir, "no-such-waves"), 1)
	if code, _ := i.runHook(broken, ev); code != 2 {
		t.Fatalf("enforce with a missing binary: exit %d", code)
	}
}
