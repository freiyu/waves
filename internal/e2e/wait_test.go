//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// installedSignal returns the command connect wrote for a Claude Code event (Stop, UserPromptSubmit).
func (p *project) installedSignal(t *testing.T, event string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(p.dir, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	json.Unmarshal(b, &s)
	for _, e := range s.Hooks[event] {
		for _, h := range e.Hooks {
			if strings.Contains(h.Command, " hook --signal ") {
				return h.Command
			}
		}
	}
	t.Fatalf("no Waves %s hook in %s", event, b)
	return ""
}

func (i *inst) waitingSince(task string) string {
	var l struct {
		Items []struct {
			TaskID       string `json:"task_id"`
			State        string `json:"state"`
			WaitingSince string `json:"waiting_since"`
		} `json:"items"`
	}
	i.operator().must("GET", "/executions", nil, &l)
	for _, x := range l.Items {
		if x.TaskID == task {
			return x.State + "|" + x.WaitingSince
		}
	}
	return "none"
}

// A harness whose turn ended is waiting for the human, not silent: through the real binary, the installed Stop
// hook marks the execution as waiting, silence limits do not apply, and the next prompt ends the wait.
func TestWaitingForInstruction(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	i.setHeartbeat(15)
	p, a := i.planned("wait")
	task, _ := a.claim()
	session := "sess-" + p.id
	claimUse := fmt.Sprintf("toolu_%s_%d", p.id, a.tool)
	pre := p.installedHook(t)
	if code, _ := i.runHook(pre, map[string]any{"session_id": session, "cwd": p.dir, "hook_event_name": "PreToolUse",
		"tool_name": "mcp__waves__waves_claim_task", "tool_use_id": claimUse, "tool_input": map[string]any{}}); code != 0 {
		t.Fatalf("bind the session: exit %d", code)
	}
	for _, ev := range []string{"PreToolUse", "Stop", "UserPromptSubmit", "SessionEnd"} {
		b, _ := os.ReadFile(filepath.Join(p.dir, ".claude", "settings.local.json"))
		if !strings.Contains(string(b), `"`+ev+`"`) {
			t.Fatalf("connect did not install the %s hook", ev)
		}
	}
	approved := i.count(`SELECT count(*) FROM approvals WHERE state = 'approved'`)
	stop, prompt := p.installedSignal(t, "Stop"), p.installedSignal(t, "UserPromptSubmit")
	if code, _ := i.runHook(stop, map[string]any{"session_id": session, "cwd": p.dir, "hook_event_name": "Stop"}); code != 0 {
		t.Fatalf("the signal hook must never block: exit %d", code)
	}
	if got := i.waitingSince(task); !strings.HasPrefix(got, "running|20") {
		t.Fatalf("after Stop: %q", got)
	}
	// Twice the disconnect limit (30 s at a 15 s heartbeat): still running, because it is waiting, not silent.
	time.Sleep(36 * time.Second)
	if got := i.waitingSince(task); !strings.HasPrefix(got, "running|20") {
		t.Fatalf("a waiting execution changed after the silence limit: %q", got)
	}
	if code, _ := i.runHook(prompt, map[string]any{"session_id": session, "cwd": p.dir, "hook_event_name": "UserPromptSubmit"}); code != 0 {
		t.Fatalf("prompt hook: exit %d", code)
	}
	if got := i.waitingSince(task); got != "running|" {
		t.Fatalf("after the prompt: %q", got)
	}
	// The session ends while the harness waits: the wait is closed, the execution is not touched otherwise.
	i.runHook(stop, map[string]any{"session_id": session, "cwd": p.dir, "hook_event_name": "Stop"})
	if got := i.waitingSince(task); !strings.HasPrefix(got, "running|20") {
		t.Fatalf("second wait: %q", got)
	}
	if code, _ := i.runHook(p.installedSignal(t, "SessionEnd"), map[string]any{"session_id": session, "cwd": p.dir, "hook_event_name": "SessionEnd", "reason": "prompt_input_exit"}); code != 0 {
		t.Fatalf("session end hook: exit %d", code)
	}
	if got := i.waitingSince(task); got != "running|" {
		t.Fatalf("after the session ended: %q", got)
	}
	// Gate-bypass: only the guard token reports a wait. The operator token, a missing token and the project's MCP token are refused.
	body := map[string]string{"session_id": session, "event": "stop"}
	for name, tok := range map[string]string{"operator": i.op, "mcp": i.secret("mcp/" + p.id + ".token"), "none": ""} {
		code, _ := i.as(tok).call("POST", "/guard/signal", body, nil, nil)
		if code != 403 && code != 401 {
			t.Fatalf("%s token reached /guard/signal: HTTP %d", name, code)
		}
	}
	if got := i.waitingSince(task); got != "running|" {
		t.Fatalf("a refused signal changed the execution: %q", got)
	}
	if n := i.count(`SELECT count(*) FROM approvals WHERE state = 'approved'`); n != approved {
		t.Fatalf("approvals went from %d to %d during a wait", approved, n)
	}
}

// A malformed call must not silently remove a fork label: the field is required, and an empty string is the way to clear it.
func TestForkLabelNeedsTheField(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	a, b := i.newProject("origin"), i.newProject("copy")
	op := i.operator()
	op.must("PATCH", "/projects/"+b.id, map[string]string{"fork_of": a.id}, nil)
	if code, _ := op.call("PATCH", "/projects/"+b.id, map[string]string{}, nil, nil); code != 422 {
		t.Fatalf("an empty body: HTTP %d, want 422", code)
	}
	if got := i.str(`SELECT COALESCE(fork_of,'') FROM projects WHERE id = ?`, b.id); got != a.id {
		t.Fatalf("an empty body changed the label to %q", got)
	}
	op.must("PATCH", "/projects/"+b.id, map[string]string{"fork_of": ""}, nil)
	if got := i.str(`SELECT COALESCE(fork_of,'') FROM projects WHERE id = ?`, b.id); got != "" {
		t.Fatalf("clearing left %q", got)
	}
}

// The hook p95 in the budget test does not show the price of a lease renewal: it fires at most once per 15 s, so the
// 300-call run barely meets it. Here the hook is called right after a 16 s pause (the renewing call) and again at once
// (a plain call), three rounds, with the lease at its shortest heartbeat (15 s).
func TestHookCostWithLeaseRenewal(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	i.setHeartbeat(15)
	p, a := i.planned("cost")
	a.claim()
	enforce := p.setPolicy(t, "enforce")
	session := "sess-" + p.id
	target := filepath.Join(p.dir, "hello.txt")
	i.runHook(enforce, map[string]any{"session_id": session, "cwd": p.dir, "hook_event_name": "PreToolUse", "tool_name": "mcp__waves__waves_claim_task",
		"tool_use_id": fmt.Sprintf("toolu_%s_%d", p.id, a.tool), "tool_input": map[string]any{}})
	var renewing, plain []time.Duration
	for round := 0; round < 3; round++ {
		time.Sleep(16 * time.Second)
		before := i.str(`SELECT last_heartbeat_at FROM leases WHERE revoked_at IS NULL`)
		code, d := i.runHook(enforce, writeEvent(session, p.dir, target, fmt.Sprintf("toolu_r%d", round)))
		if code != 0 {
			t.Fatalf("round %d: exit %d", round, code)
		}
		renewing = append(renewing, d)
		if after := i.str(`SELECT last_heartbeat_at FROM leases WHERE revoked_at IS NULL`); after == before {
			t.Fatalf("round %d: the call after a 16 s pause did not renew the lease", round)
		}
		code, d = i.runHook(enforce, writeEvent(session, p.dir, target, fmt.Sprintf("toolu_p%d", round)))
		if code != 0 {
			t.Fatalf("round %d plain: exit %d", round, code)
		}
		plain = append(plain, d)
	}
	t.Logf("hook latency with a lease renewal: %v; right after, without one: %v (budget 50 ms)", renewing, plain)
	for _, d := range renewing {
		if d > 50*time.Millisecond {
			t.Fatalf("a renewing hook call took %s, budget 50 ms", d)
		}
	}
}

// The guard settings and the other project settings are not applied half and half: a mixed request is refused whole.
func TestMixedProjectSettingsRequestIsRefusedWhole(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	p, _ := i.planned("mixed")
	before := i.str(`SELECT guard_policy FROM project_settings WHERE project_id = ?`, p.id)
	code, _ := p.dash.call("PATCH", "/projects/"+p.id+"/settings", map[string]any{"guard_policy": "enforce", "context_budget": 5000}, nil, elev(p.dash.stepUp("")))
	if code != 422 {
		t.Fatalf("a mixed request: HTTP %d, want 422", code)
	}
	if got := i.str(`SELECT guard_policy FROM project_settings WHERE project_id = ?`, p.id); got != before {
		t.Fatalf("a refused request changed the policy from %q to %q", before, got)
	}
	if got := i.count(`SELECT count(*) FROM project_settings WHERE project_id = ? AND context_budget = 5000`, p.id); got != 0 {
		t.Fatal("a refused request changed the budget")
	}
}

// A dashboard event stream ends when its session does: after logout it gets nothing more and is closed.
func TestEventStreamEndsWithItsSession(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	c := i.dashboard()
	req, _ := http.NewRequest("GET", i.url("/api/v1/events"), nil)
	req.Header.Set("Cookie", "waves_session="+c.cookie)
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("event stream: %v %v", err, res)
	}
	defer res.Body.Close()
	closed := make(chan struct{})
	go func() { io.Copy(io.Discard, res.Body); close(closed) }()
	time.Sleep(6 * time.Second) // the stream re-checks its session at most every 5 s while events flow
	c.must("POST", "/auth/logout", nil, nil)
	i.newProject("after-logout") // an event for the stream to try to send
	select {
	case <-closed:
	case <-time.After(30 * time.Second):
		t.Fatal("the stream of a logged-out session stayed open")
	}
}

// The longest wait has a user path and is a security setting: it takes a passphrase elevation and stays in 1 to 72 hours.
func TestLongestWaitSettingNeedsAnElevation(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	c := i.dashboard()
	if code, _ := c.call("PATCH", "/settings", map[string]int{"waiting_max_h": 72}, nil, nil); code != 403 {
		t.Fatalf("without an elevation: HTTP %d, want 403", code)
	}
	if code, _ := c.call("PATCH", "/settings", map[string]int{"waiting_max_h": 100}, nil, elev(c.stepUp(""))); code != 422 {
		t.Fatalf("out of range: HTTP %d, want 422", code)
	}
	c.must("PATCH", "/settings", map[string]int{"waiting_max_h": 12}, nil, elev(c.stepUp("")))
	var st struct {
		WaitingMaxH int `json:"waiting_max_h"`
	}
	c.must("GET", "/settings", nil, &st)
	if st.WaitingMaxH != 12 {
		t.Fatalf("waiting_max_h is %d, want 12", st.WaitingMaxH)
	}
}
