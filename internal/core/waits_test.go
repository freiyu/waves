package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"waves/internal/api/contract"
)

// leasedSession claims p1-t1 and binds the claim to the Claude Code session "cc" through the hook.
func leasedSession(t *testing.T) (*agentFlow, ClaimResult, string) {
	t.Helper()
	a := startedFlow(t)
	task := a.task(t, "p1-t1")
	r, err := a.s.ClaimTask(a.ctx, a.agent, "s1", task, "tu_w")
	if err != nil {
		t.Fatal(err)
	}
	a.hook(t, "cc", "mcp__waves__waves_claim_task", "tu_w", nil)
	return a, r, task
}

func (a *agentFlow) open(t *testing.T, exe string) int {
	return a.count(t, `SELECT count(*) FROM execution_waits WHERE execution_id = ? AND ended_at IS NULL`, exe)
}

func (a *agentFlow) signal(t *testing.T, session, event string) error {
	return a.s.SignalGuard(a.ctx, a.guard, HookSignal{SessionID: session, Event: event})
}

// A harness whose turn ended is not silent: it waits. The execution stays Running past the silence limits and the
// lease ends only after the longest wait, as before: Interrupted, the task Blocked with recovery_required, no rerun.
func TestWaitingForTheHumanKeepsTheLeaseUntilTheLongestWait(t *testing.T) {
	a, r, task := leasedSession(t)
	if err := a.signal(t, "cc", "stop"); err != nil {
		t.Fatal(err)
	}
	if a.open(t, r.ExecutionID) != 1 {
		t.Fatal("no wait recorded")
	}
	x, err := a.s.ExecutionDetail(a.ctx, r.ExecutionID)
	if err != nil || x.WaitingSince == nil || x.State != "running" {
		t.Fatalf("waiting view: %+v %v", x.ExecutionSummary, err)
	}
	// A second Stop does not open a second wait.
	a.signal(t, "cc", "stop")
	if a.open(t, r.ExecutionID) != 1 {
		t.Fatal("two open waits")
	}
	// Far past the silence limits (60 s and 180 s): still running, lease alive.
	a.clock.Add(7 * time.Hour)
	a.s.expireLeases(a.ctx)
	if a.state(t, "executions", r.ExecutionID) != "Running" {
		t.Fatalf("a waiting execution was marked %s", a.state(t, "executions", r.ExecutionID))
	}
	// The longest wait (8 h) ends the lease the usual way.
	a.clock.Add(2 * time.Hour)
	a.s.expireLeases(a.ctx)
	if a.state(t, "executions", r.ExecutionID) != "Interrupted" || a.state(t, "tasks", task) != "Blocked" {
		t.Fatalf("after the longest wait: %s %s", a.state(t, "executions", r.ExecutionID), a.state(t, "tasks", task))
	}
	if a.open(t, r.ExecutionID) != 0 {
		t.Fatal("the wait outlived the lease")
	}
	if n := a.count(t, `SELECT count(*) FROM executions WHERE task_id = ?`, task); n != 1 {
		t.Fatalf("%d executions: the task was rerun", n)
	}
}

func TestPromptHeartbeatAndToolCallsEndTheWait(t *testing.T) {
	a, r, _ := leasedSession(t)
	inside := map[string]any{"file_path": filepath.Join(a.root, "x.go")}
	for name, resume := range map[string]func(){
		"prompt":    func() { a.signal(t, "cc", "prompt") },
		"heartbeat": func() { a.clock.Add(20 * time.Second); a.s.Heartbeat(a.ctx, a.agent, "s1", r.LeaseToken) },
		"tool call": func() { a.clock.Add(20 * time.Second); a.hook(t, "cc", "Write", "tu_x", inside) },
	} {
		a.signal(t, "cc", "stop")
		if a.open(t, r.ExecutionID) != 1 {
			t.Fatalf("%s: no wait before", name)
		}
		resume()
		if a.open(t, r.ExecutionID) != 0 {
			t.Fatalf("%s did not end the wait", name)
		}
	}
	if a.auditCount(t, "execution.waiting") != 3 || a.auditCount(t, "execution.resumed") != 3 {
		t.Fatalf("audit: %d waiting, %d resumed", a.auditCount(t, "execution.waiting"), a.auditCount(t, "execution.resumed"))
	}
}

// A hook call by a session that holds a lease is a sign of life: a harness that works but forgets to heartbeat is not
// cut off after three minutes. It renews at most once per 15 s and never changes the decision.
func TestToolCallsAreASignOfLife(t *testing.T) {
	a, r, task := leasedSession(t)
	inside := map[string]any{"file_path": filepath.Join(a.root, "x.go")}
	for i := 0; i < 20; i++ { // 20 minutes of work, a tool call every minute, no heartbeat at all
		a.clock.Add(time.Minute)
		if d := a.hook(t, "cc", "Write", "tu_"+string(rune('a'+i)), inside); d.Decision != "allow" {
			t.Fatalf("decision changed: %+v", d)
		}
		a.s.expireLeases(a.ctx)
	}
	if a.state(t, "executions", r.ExecutionID) != "Running" || a.state(t, "tasks", task) != "In Progress" {
		t.Fatalf("an active session was cut off: %s %s", a.state(t, "executions", r.ExecutionID), a.state(t, "tasks", task))
	}
	// A Disconnected execution that makes a tool call is Running again.
	a.clock.Add(61 * time.Second)
	a.s.expireLeases(a.ctx)
	if a.state(t, "executions", r.ExecutionID) != "Disconnected" {
		t.Fatal("not disconnected after silence")
	}
	a.hook(t, "cc", "Write", "tu_back", inside)
	if a.state(t, "executions", r.ExecutionID) != "Running" {
		t.Fatal("a tool call did not revive the execution")
	}
}

// Gate-bypass: nobody but the guard token reports a wait, and a wait grants nothing.
func TestWaitSignalCannotBeAbused(t *testing.T) {
	a, r, task := leasedSession(t)
	for name, p := range map[string]Principal{"mcp": a.agent, "operator": {Kind: KindOperator}, "session": a.p, "lease": {Kind: KindLease}} {
		if err := a.s.SignalGuard(a.ctx, p, HookSignal{SessionID: "cc", Event: "stop"}); code(err) != "wrong_scope" {
			t.Fatalf("%s reported a wait: %v", name, err)
		}
	}
	if a.open(t, r.ExecutionID) != 0 {
		t.Fatal("a refused signal left a wait")
	}
	for _, bad := range []HookSignal{{SessionID: "", Event: "stop"}, {SessionID: "cc", Event: "approve"}, {SessionID: "cc", Event: ""}} {
		if err := a.s.SignalGuard(a.ctx, a.guard, bad); code(err) != "validation_failed" {
			t.Fatalf("%+v accepted: %v", bad, err)
		}
	}
	// A session without a lease: no error, no wait, nothing to tell it apart.
	if err := a.signal(t, "nobody", "stop"); err != nil {
		t.Fatalf("unknown session: %v", err)
	}
	if a.count(t, `SELECT count(*) FROM execution_waits`) != 0 {
		t.Fatal("a wait for a session without a lease")
	}
	// Another session cannot end this session's wait.
	a.signal(t, "cc", "stop")
	a.signal(t, "other-session", "prompt")
	if a.open(t, r.ExecutionID) != 1 {
		t.Fatal("another session ended the wait")
	}
	// Waiting does not approve anything, move a gate, or start a second run.
	if a.state(t, "tasks", task) != "In Progress" || a.count(t, `SELECT count(*) FROM approvals WHERE state = 'approved' AND decided_by_scope != 'human'`) != 0 {
		t.Fatal("waiting changed a task or an approval")
	}
	// Cancelling (the human's way out) ends the wait with the execution.
	if err := a.s.CancelExecution(a.ctx, a.p, r.ExecutionID); err != nil {
		t.Fatal(err)
	}
	if a.open(t, r.ExecutionID) != 0 {
		t.Fatal("the wait outlived the execution")
	}
	// A session whose lease ended can no longer report a wait.
	a.signal(t, "cc", "stop")
	if a.count(t, `SELECT count(*) FROM execution_waits WHERE ended_at IS NULL`) != 0 {
		t.Fatal("a wait after the lease ended")
	}
}

func TestForkLabel(t *testing.T) {
	a := startedFlow(t)
	root2 := filepath.Join(a.dir, "fork")
	mkGitProject(t, root2)
	id2, err := a.s.RegisterProjectFork(a.ctx, a.p, root2, "demo-fork", a.project)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := a.s.Project(a.ctx, id2); p.ForkOf != a.project {
		t.Fatalf("fork_of %q", p.ForkOf)
	}
	if err := a.s.SetProjectFork(a.ctx, a.p, a.project, a.project); code(err) != "validation_failed" {
		t.Fatalf("self fork: %v", err)
	}
	if err := a.s.SetProjectFork(a.ctx, a.p, a.project, id2); code(err) != "validation_failed" {
		t.Fatalf("a loop of forks: %v", err)
	}
	if err := a.s.SetProjectFork(a.ctx, a.p, id2, "prj_missing"); code(err) != "validation_failed" {
		t.Fatalf("unknown origin: %v", err)
	}
	for _, p := range []Principal{a.agent, a.guard, {Kind: KindLease}} {
		if err := a.s.SetProjectFork(a.ctx, p, id2, ""); code(err) != "wrong_scope" {
			t.Fatalf("scope %v changed a fork: %v", p.Kind, err)
		}
	}
	if err := a.s.SetProjectFork(a.ctx, a.p, id2, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.s.SetProjectFork(a.ctx, a.p, id2, a.project); err != nil {
		t.Fatal(err)
	}
	if err := a.s.RemoveProject(a.ctx, a.p, a.project); err != nil {
		t.Fatal(err)
	}
	if p, _ := a.s.Project(a.ctx, id2); p.ForkOf != "" {
		t.Fatalf("a fork of a removed project still points at it: %q", p.ForkOf)
	}
}

func mkGitProject(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
}

func TestGraphLayout(t *testing.T) {
	a := startedFlow(t)
	f := func(v float64) *float64 { return &v }
	good := contract.GraphLayout{
		Nodes: map[string]contract.GraphNodePos{"phs_a": {X: 10, Y: 20}},
		Lines: map[string]contract.GraphLine{"phs_a>phs_b": {From: "bottom", To: "top", Fo: f(0.3), Tt: f(1)}},
	}
	if l, _ := a.s.GraphLayout(a.ctx); len(l.Nodes) != 0 || l.Nodes == nil || l.Lines == nil {
		t.Fatalf("empty layout: %+v", l)
	}
	if err := a.s.SetGraphLayout(a.ctx, a.p, good); err != nil {
		t.Fatal(err)
	}
	if l, _ := a.s.GraphLayout(a.ctx); l.Nodes["phs_a"].X != 10 || *l.Lines["phs_a>phs_b"].Fo != 0.3 {
		t.Fatalf("round trip: %+v", l)
	}
	bad := map[string]contract.GraphLayout{
		"key with a slash":  {Nodes: map[string]contract.GraphNodePos{"../x": {}}},
		"huge coordinate":   {Nodes: map[string]contract.GraphNodePos{"phs_a": {X: 1e9}}},
		"unknown side":      {Lines: map[string]contract.GraphLine{"a>b": {From: "diagonal", To: "top"}}},
		"position above 1":  {Lines: map[string]contract.GraphLine{"a>b": {From: "top", To: "top", Fo: f(1.5)}}},
		"negative position": {Lines: map[string]contract.GraphLine{"a>b": {From: "top", To: "top", Tt: f(-0.1)}}},
		"markup in a key":   {Nodes: map[string]contract.GraphNodePos{"<script>": {}}},
	}
	for name, l := range bad {
		if err := a.s.SetGraphLayout(a.ctx, a.p, l); code(err) != "validation_failed" {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	many := contract.GraphLayout{Nodes: map[string]contract.GraphNodePos{}}
	for i := 0; i <= graphMaxNodes; i++ {
		many.Nodes["n"+string(rune('a'+i%26))+string(rune('a'+i/26%26))+string(rune('a'+i/676))] = contract.GraphNodePos{}
	}
	if err := a.s.SetGraphLayout(a.ctx, a.p, many); code(err) != "validation_failed" {
		t.Errorf("%d nodes accepted: %v", len(many.Nodes), err)
	}
	if l, _ := a.s.GraphLayout(a.ctx); l.Nodes["phs_a"].X != 10 {
		t.Fatal("a refused layout replaced the saved one")
	}
	for name, p := range map[string]Principal{"mcp": a.agent, "guard": a.guard, "lease": {Kind: KindLease}} {
		if err := a.s.SetGraphLayout(a.ctx, p, good); code(err) != "wrong_scope" {
			t.Errorf("%s saved a layout: %v", name, err)
		}
	}
}

// The signal hooks join the user's own hooks and leave them alone: connect adds Stop and UserPromptSubmit entries next
// to whatever is there, and disconnect removes only what Waves installed.
func TestSignalHooksShareTheFileWithTheUsersOwn(t *testing.T) {
	root := t.TempDir()
	settings := filepath.Join(root, ".claude", "settings.local.json")
	os.MkdirAll(filepath.Dir(settings), 0o700)
	os.WriteFile(settings, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo mine"}]}]},"model":"haiku"}`), 0o600)
	out, _, _, err := plannedEdits(root, "prj_x", "audit")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(out, &m)
	hooks := m["hooks"].(map[string]any)
	if n := len(hooks["Stop"].([]any)); n != 2 {
		t.Fatalf("Stop has %d entries, want the user's and Waves's", n)
	}
	if _, ok := hooks["UserPromptSubmit"]; !ok || m["model"] != "haiku" {
		t.Fatalf("settings: %s", out)
	}
	os.WriteFile(settings, out, 0o600)
	if err := revertSettings(settings, changeRecord{}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(settings)
	if !strings.Contains(string(b), "echo mine") || strings.Contains(string(b), "hook --signal") || strings.Contains(string(b), "UserPromptSubmit") || strings.Contains(string(b), "PreToolUse") {
		t.Fatalf("after revert: %s", b)
	}
	// Connecting twice does not stack entries.
	again, _, _, _ := plannedEdits(root, "prj_x", "audit")
	os.WriteFile(settings, again, 0o600)
	twice, _, _, _ := plannedEdits(root, "prj_x", "audit")
	json.Unmarshal(twice, &m)
	if n := len(m["hooks"].(map[string]any)["Stop"].([]any)); n != 2 {
		t.Fatalf("Stop has %d entries after connecting twice", n)
	}
}

// A wait stretches the lease; when it ends the lease gets its normal window back, so a session that dies after its
// prompt is not held for hours, and a long wait does not leave the execution looking silent the moment it ends.
func TestEndOfAWaitGivesTheLeaseItsNormalWindowBack(t *testing.T) {
	a, r, task := leasedSession(t)
	a.signal(t, "cc", "stop")
	a.clock.Add(2 * time.Hour)
	a.signal(t, "cc", "prompt")
	a.s.expireLeases(a.ctx)
	if a.state(t, "executions", r.ExecutionID) != "Running" {
		t.Fatalf("right after the prompt the execution is %s", a.state(t, "executions", r.ExecutionID))
	}
	a.clock.Add(181 * time.Second) // the session died after its prompt
	a.s.expireLeases(a.ctx)
	if a.state(t, "executions", r.ExecutionID) != "Interrupted" || a.state(t, "tasks", task) != "Blocked" {
		t.Fatalf("a dead session after a wait is still held: %s %s", a.state(t, "executions", r.ExecutionID), a.state(t, "tasks", task))
	}
}

// The audit chain is re-verified before every approval: a loop of signals must not grow it without limit.
func TestWaitSignalsCannotFloodTheAuditChain(t *testing.T) {
	a, r, _ := leasedSession(t)
	for i := 0; i < 60; i++ {
		a.signal(t, "cc", "stop")
		a.signal(t, "cc", "prompt")
	}
	n := a.count(t, `SELECT count(*) FROM audit WHERE subject = ? AND event IN ('execution.waiting','execution.resumed')`, r.ExecutionID)
	if n > maxWaitAudits {
		t.Fatalf("%d wait records in one minute, limit %d", n, maxWaitAudits)
	}
	if a.count(t, `SELECT count(*) FROM execution_waits WHERE execution_id = ?`, r.ExecutionID) != 60 {
		t.Fatal("the waits themselves must still be recorded")
	}
	if a.auditCount(t, "audit.chain_broken") != 0 {
		t.Fatal("chain broken")
	}
}

// The longest wait is a security setting: an operator token alone cannot stretch how long an idle session holds the lock.
func TestLongestWaitNeedsAHumanElevation(t *testing.T) {
	a := startedFlow(t)
	if err := a.s.PatchSettings(a.ctx, a.p, "", map[string]int{"waiting_max_h": 72}); code(err) != "stepup_required" {
		t.Fatalf("changed without elevation: %v", err)
	}
	if err := a.s.PatchSettings(a.ctx, a.p, mustStepUp(t, a.env, a.p), map[string]int{"waiting_max_h": 100}); code(err) != "validation_failed" {
		t.Fatalf("out of range accepted: %v", err)
	}
}

// The harness session ended (SessionEnd): a wait is closed and the lease goes back to its normal window, so a dead session or
// a finished `claude -p` is flagged within minutes, not after the longest wait. Without a wait nothing changes.
func TestSessionEndEndsAWaitAndShortensTheLease(t *testing.T) {
	a, r, task := leasedSession(t)
	a.signal(t, "cc", "stop")
	a.clock.Add(time.Hour)
	if err := a.signal(t, "cc", "end"); err != nil {
		t.Fatal(err)
	}
	if a.open(t, r.ExecutionID) != 0 {
		t.Fatal("the wait outlived its session")
	}
	a.s.expireLeases(a.ctx)
	if a.state(t, "executions", r.ExecutionID) != "Running" {
		t.Fatalf("right after the session ended: %s", a.state(t, "executions", r.ExecutionID))
	}
	a.clock.Add(181 * time.Second)
	a.s.expireLeases(a.ctx)
	if a.state(t, "executions", r.ExecutionID) != "Interrupted" || a.state(t, "tasks", task) != "Blocked" {
		t.Fatalf("a finished session is still held: %s %s", a.state(t, "executions", r.ExecutionID), a.state(t, "tasks", task))
	}
	if n := a.count(t, `SELECT count(*) FROM executions WHERE task_id = ?`, task); n != 1 {
		t.Fatalf("%d executions: the task was rerun", n)
	}
	// SessionEnd from another credential or for a session without a lease does nothing.
	for _, p := range []Principal{a.agent, {Kind: KindOperator}} {
		if err := a.s.SignalGuard(a.ctx, p, HookSignal{SessionID: "cc", Event: "end"}); code(err) != "wrong_scope" {
			t.Fatalf("scope %v ended a session: %v", p.Kind, err)
		}
	}
	if err := a.signal(t, "nobody", "end"); err != nil {
		t.Fatal(err)
	}
}

// A project connected by 1.0.0 has the guard hook but not the signal hooks: Waves says so and does not call it drift.
func TestSignalHooksAreReportedMissingOnAnOldConnection(t *testing.T) {
	a := startedFlow(t)
	g, _ := a.s.GuardInfo(a.ctx, a.project)
	if !g.Installed || !g.WaitingHooks || g.Drift != "" {
		t.Fatalf("after a connect: %+v", g)
	}
	settings := filepath.Join(a.root, ".claude", "settings.local.json")
	b, _ := os.ReadFile(settings)
	var m map[string]any
	json.Unmarshal(b, &m)
	hooks := m["hooks"].(map[string]any)
	for ev := range signalEvents {
		delete(hooks, ev)
	}
	old, _ := json.Marshal(m)
	os.WriteFile(settings, old, 0o600)
	g, _ = a.s.GuardInfo(a.ctx, a.project)
	if g.WaitingHooks || !g.Installed || g.Drift != "" {
		t.Fatalf("a 1.0.0 style file: %+v", g)
	}
	// Connecting again installs them and keeps the policy the project had.
	a.clock.Add(time.Second)
	if err := a.s.SetProjectGuard(a.ctx, a.p, mustStepUp(t, a.env, a.p), a.project, "enforce", ""); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(settings, old, 0o600) // the policy change rewrote the hook; take the signal hooks away again
	plan, err := a.s.ConnectPlan(a.ctx, a.project)
	if err != nil {
		t.Fatal(err)
	}
	a.clock.Add(time.Second)
	if _, err := a.s.Connect(a.ctx, a.p, mustStepUp(t, a.env, a.p), a.project, plan.PlanHash); err != nil {
		t.Fatalf("connect again: %v", err)
	}
	g, _ = a.s.GuardInfo(a.ctx, a.project)
	if !g.WaitingHooks || !g.Installed || g.Drift != "" || g.Policy != "enforce" {
		t.Fatalf("after connecting again: %+v", g)
	}
}
