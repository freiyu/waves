//go:build e2e

// Package e2e drives the real waves binary as a separate process: setup over
// a pseudo-terminal, the HTTP API, the CLI, the MCP bridge and the guard hook,
// each instance in its own temporary XDG directories.
package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"

	"waves/internal/api/contract"
)

// testPass is a fake passphrase for tests only.
const testPass = "e2e horse battery staple" // guard:allow fake test fixture

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "waves-e2e-bin-")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "waves")
	// The same build as the release: static, trimmed paths, dashboard embedded.
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", bin, "../../cmd/waves")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build waves: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// inst is one Waves installation with its own directories and port.
type inst struct {
	t     *testing.T
	dir   string
	env   []string
	port  int
	srv   *exec.Cmd
	exit  chan error
	op    string
	guard string
	logf  *os.File
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// newInst runs `waves setup` on a pseudo-terminal and starts the service.
func newInst(t *testing.T) *inst {
	t.Helper()
	dir := t.TempDir()
	i := &inst{t: t, dir: dir, port: freePort(t)}
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o700)
	i.env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + home, "LANG=C.UTF-8",
		"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"), "XDG_DATA_HOME=" + filepath.Join(dir, "data"), "XDG_STATE_HOME=" + filepath.Join(dir, "state"),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=e2e", "GIT_AUTHOR_EMAIL=e2e@example.invalid", "GIT_COMMITTER_NAME=e2e", "GIT_COMMITTER_EMAIL=e2e@example.invalid",
	}
	cfg := filepath.Join(dir, "config", "waves")
	os.MkdirAll(cfg, 0o700)
	os.WriteFile(filepath.Join(cfg, "config.json"), []byte(fmt.Sprintf(`{"port":%d}`, i.port)), 0o600)
	out, err := i.tty([]string{"setup"}, testPass+"\n"+testPass+"\n")
	if err != nil || !strings.Contains(out, "set       approval passphrase") {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	i.op = i.secret("operator.token")
	i.guard = i.secret("guard.token")
	i.start()
	t.Cleanup(i.stop)
	return i
}

func (i *inst) cfgDir() string  { return filepath.Join(i.dir, "config", "waves") }
func (i *inst) dataDir() string { return filepath.Join(i.dir, "data", "waves") }
func (i *inst) dbPath() string  { return filepath.Join(i.dataDir(), "waves.db") }

func (i *inst) secret(name string) string {
	b, err := os.ReadFile(filepath.Join(i.cfgDir(), name))
	if err != nil {
		i.t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// start runs `waves serve` and waits until it answers.
func (i *inst) start() {
	i.t.Helper()
	var err error
	i.logf, err = os.OpenFile(filepath.Join(i.dir, "serve.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		i.t.Fatal(err)
	}
	i.srv = exec.Command(bin, "serve")
	i.srv.Env = i.env
	i.srv.Stdout, i.srv.Stderr = i.logf, i.logf
	if err := i.srv.Start(); err != nil {
		i.t.Fatal(err)
	}
	i.exit = make(chan error, 1)
	go func(c *exec.Cmd, ch chan error) { ch <- c.Wait() }(i.srv, i.exit)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if res, err := http.Get(i.url("/api/v1/health")); err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				return
			}
		}
		select {
		case err := <-i.exit:
			i.t.Fatalf("waves serve exited: %v\n%s", err, i.log())
		case <-time.After(20 * time.Millisecond):
		}
	}
	i.t.Fatalf("waves serve did not answer in 10 s\n%s", i.log())
}

// kill9 ends the service the hard way, as a crash would.
func (i *inst) kill9() {
	i.t.Helper()
	if i.srv == nil {
		return
	}
	i.srv.Process.Signal(syscall.SIGKILL)
	<-i.exit
	i.srv = nil
	i.logf.Close()
}

func (i *inst) stop() {
	if i.srv == nil {
		return
	}
	i.srv.Process.Signal(syscall.SIGTERM)
	select {
	case <-i.exit:
	case <-time.After(10 * time.Second):
		i.srv.Process.Kill()
		<-i.exit
	}
	i.srv = nil
	i.logf.Close()
}

func (i *inst) log() string {
	b, _ := os.ReadFile(filepath.Join(i.dir, "serve.log"))
	return string(b)
}

func (i *inst) url(p string) string { return fmt.Sprintf("http://127.0.0.1:%d%s", i.port, p) }

// run executes a waves command with stdin from a pipe.
func (i *inst) run(stdin string, args ...string) (string, int) {
	cmd := exec.Command(bin, args...)
	cmd.Env = i.env
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		i.t.Fatal(err)
	}
	return out.String(), code
}

// tty executes a waves command with a pseudo-terminal as stdin. The input is
// written line by line as the prompts appear.
func (i *inst) tty(args []string, input string) (string, error) {
	m, s, err := openPTY()
	if err != nil {
		return "", err
	}
	defer m.Close()
	cmd := exec.Command(bin, args...)
	cmd.Env = i.env
	var out bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = s, &out, &out
	if err := cmd.Start(); err != nil {
		s.Close()
		return "", err
	}
	s.Close()
	// Drain the echo so the terminal never blocks.
	go io.Copy(io.Discard, m)
	m.Write([]byte(input))
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-time.After(20 * time.Second):
		cmd.Process.Kill()
		err = fmt.Errorf("timed out")
	}
	return out.String(), err
}

// openPTY opens a pseudo-terminal pair with the Linux ioctls.
func openPTY() (*os.File, *os.File, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		m.Close()
		return nil, nil, e
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		m.Close()
		return nil, nil, e
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	return m, s, nil
}

// db opens the database directly, as an attacker with file access could.
func (i *inst) db() *sql.DB {
	d, err := sql.Open("sqlite", "file:"+i.dbPath()+"?_pragma=busy_timeout(5000)")
	if err != nil {
		i.t.Fatal(err)
	}
	i.t.Cleanup(func() { d.Close() })
	return d
}

func (i *inst) count(q string, args ...any) int {
	i.t.Helper()
	d := i.db()
	defer d.Close()
	var n int
	if err := d.QueryRow(q, args...).Scan(&n); err != nil {
		i.t.Fatalf("%s: %v", q, err)
	}
	return n
}

func (i *inst) str(q string, args ...any) string {
	i.t.Helper()
	d := i.db()
	defer d.Close()
	var s sql.NullString
	if err := d.QueryRow(q, args...).Scan(&s); err != nil && err != sql.ErrNoRows {
		i.t.Fatalf("%s: %v", q, err)
	}
	return s.String
}

// ---- HTTP clients ----

// client is one caller of the API: a bearer token or a dashboard session.
type client struct {
	i      *inst
	bearer string
	cookie string
	csrf   string
	tty    bool
}

func (i *inst) operator() *client { return &client{i: i, bearer: i.op} }
func (i *inst) as(token string) *client {
	return &client{i: i, bearer: token}
}

// dashboard signs in through the official path: a login link from the
// operator token, consumed once.
func (i *inst) dashboard() *client {
	i.t.Helper()
	var l struct {
		URL string `json:"url"`
	}
	i.operator().must("POST", "/auth/login-links", nil, &l)
	u, _ := url.Parse(l.URL)
	code := strings.TrimPrefix(u.Fragment, "/signin/")
	res, body := i.raw("POST", "/api/v1/auth/consume", map[string]string{"code": code}, nil)
	if res.StatusCode != 200 {
		i.t.Fatalf("consume: %d %s", res.StatusCode, body)
	}
	var s contract.Session
	json.Unmarshal(body, &s)
	c := &client{i: i, csrf: s.CSRFToken}
	for _, ck := range res.Cookies() {
		if ck.Name == "waves_session" {
			c.cookie = ck.Value
		}
	}
	if c.cookie == "" {
		i.t.Fatal("no session cookie")
	}
	return c
}

func (i *inst) raw(method, path string, body any, hdr map[string]string) (*http.Response, []byte) {
	i.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, i.url(path), rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		i.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

// call sends one API request and returns the status and the error code.
func (c *client) call(method, path string, body any, out any, hdr map[string]string) (int, string) {
	c.i.t.Helper()
	h := map[string]string{}
	if c.bearer != "" {
		h["Authorization"] = "Bearer " + c.bearer
	}
	if c.cookie != "" {
		h["Cookie"] = "waves_session=" + c.cookie
		h["X-Waves-CSRF"] = c.csrf
	}
	if c.tty {
		h["X-Waves-TTY"] = "1"
	}
	for k, v := range hdr {
		h[k] = v
	}
	res, b := c.i.raw(method, "/api/v1"+path, body, h)
	if res.StatusCode >= 300 {
		var e contract.ErrorBody
		json.Unmarshal(b, &e)
		return res.StatusCode, e.Error.Code
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			c.i.t.Fatalf("%s %s: decode: %v\n%s", method, path, err, b)
		}
	}
	return res.StatusCode, ""
}

func (c *client) must(method, path string, body any, out any, hdr ...map[string]string) {
	c.i.t.Helper()
	var h map[string]string
	if len(hdr) > 0 {
		h = hdr[0]
	}
	if st, code := c.call(method, path, body, out, h); st >= 300 {
		c.i.t.Fatalf("%s %s: %d %s", method, path, st, code)
	}
}

// stepUp returns an elevation secret; approvalID "" asks for a window.
func (c *client) stepUp(approvalID string) string {
	c.i.t.Helper()
	var r contract.StepUpResponse
	c.must("POST", "/auth/stepup", map[string]string{"passphrase": testPass, "approval_id": approvalID}, &r)
	return r.ElevationSecret
}

func elev(secret string) map[string]string { return map[string]string{"X-Waves-Elevation": secret} }

// decide approves or rejects one approval with a fresh single-use step-up.
func (c *client) decide(apr, decision string, extra map[string]string) (int, string) {
	c.i.t.Helper()
	var d contract.ApprovalDetail
	c.must("GET", "/approvals/"+apr, nil, &d)
	body := map[string]string{"decision": decision, "subject_hash": d.SubjectHash}
	if decision == "reject" {
		body["feedback"] = "needs another pass"
	}
	for k, v := range extra {
		body[k] = v
	}
	return c.call("POST", "/approvals/"+apr+"/decision", body, nil, elev(c.stepUp(apr)))
}

func (c *client) mustDecide(apr, decision string, extra map[string]string) {
	c.i.t.Helper()
	if st, code := c.decide(apr, decision, extra); st >= 300 {
		c.i.t.Fatalf("decide %s %s: %d %s", apr, decision, st, code)
	}
}

// pending returns the id of the pending approval of a category in this project.
func (p *project) pending(category string) string {
	c := p.dash
	c.i.t.Helper()
	var l contract.ApprovalList
	c.must("GET", "/approvals?state=pending", nil, &l)
	for _, a := range l.Items {
		if a.Category == category && a.ProjectID == p.id {
			return a.ID
		}
	}
	c.i.t.Fatalf("no pending %s approval in %+v", category, l.Items)
	return ""
}

// ---- projects and the fake agent ----

// project is a registered, connected git project.
type project struct {
	id, dir string
	dash    *client
}

// sampleProposal is a small two-phase workflow. The validator command runs
// with /bin/sh -c after the human approved it.
const sampleProposal = `{
  "schema_version": 1,
  "summary": "Add a greeting file",
  "risks": [{"description": "none", "severity": "low"}],
  "phases": [
    {"key": "p1", "title": "Build", "objective": "Write the greeting", "deliverables": ["hello.txt"],
     "validators": [{"type": "command", "name": "file exists", "command": "test -f hello.txt", "timeout_s": 30}],
     "tasks": [{"key": "p1-t1", "title": "Greeting", "acceptance_criteria": ["hello.txt says hello"]}]},
    {"key": "p2", "title": "Polish", "objective": "Tidy up", "validators": [{"type": "command", "name": "true", "command": "true", "timeout_s": 30}],
     "tasks": [{"key": "p2-t1", "title": "Tidy", "depends_on": ["p1-t1"], "acceptance_criteria": ["done"]}]}
  ]
}`

func (i *inst) git(dir string, args ...string) {
	i.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = dir, i.env
	if out, err := cmd.CombinedOutput(); err != nil {
		i.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newProject registers a git project and connects Claude Code to it.
func (i *inst) newProject(name string) *project {
	i.t.Helper()
	dir := filepath.Join(i.dir, "work", name)
	os.MkdirAll(dir, 0o700)
	i.git(dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# "+name+"\n"), 0o600)
	i.git(dir, "add", ".")
	i.git(dir, "commit", "-q", "-m", "init")
	p := &project{dir: dir, dash: i.dashboard()}
	var pr contract.Project
	p.dash.must("POST", "/projects", map[string]string{"path": dir, "name": name}, &pr)
	p.id = pr.ID
	var plan contract.ConnectPlan
	p.dash.must("GET", "/projects/"+p.id+"/agent/connect-plan", nil, &plan)
	p.dash.must("POST", "/projects/"+p.id+"/agent/connect", map[string]string{"plan_hash": plan.PlanHash}, nil, elev(p.dash.stepUp("")))
	return p
}

// agent is the scriptable fake harness: an MCP client on `waves mcp`, the
// bridge Claude Code starts from .mcp.json.
type agent struct {
	t    *testing.T
	cs   *sdk.ClientSession
	cmd  *exec.Cmd
	p    *project
	i    *inst
	tool int
}

func (p *project) agent(i *inst) *agent {
	i.t.Helper()
	cmd := exec.Command(bin, "mcp", "--project", p.id)
	cmd.Env, cmd.Dir = i.env, p.dir
	c := sdk.NewClient(&sdk.Implementation{Name: "fake-agent", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cs, err := c.Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		i.t.Fatalf("connect agent: %v", err)
	}
	a := &agent{t: i.t, cs: cs, cmd: cmd, p: p, i: i}
	i.t.Cleanup(func() { cs.Close() })
	return a
}

// call runs one tool and returns its text and whether it was an error. Every
// call carries a tool_use_id like Claude Code's.
func (a *agent) call(name string, args any) (string, bool) {
	a.t.Helper()
	a.tool++
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p := &sdk.CallToolParams{Name: name, Arguments: args}
	p.SetMeta(map[string]any{"claudecode/toolUseId": fmt.Sprintf("toolu_%s_%d", a.p.id, a.tool)})
	r, err := a.cs.CallTool(ctx, p)
	if err != nil {
		return err.Error(), true
	}
	var sb strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), r.IsError
}

func (a *agent) must(name string, args any, out any) {
	a.t.Helper()
	text, isErr := a.call(name, args)
	if isErr {
		a.t.Fatalf("%s: %s", name, text)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(text), out); err != nil {
			a.t.Fatalf("%s: decode %v: %s", name, err, text)
		}
	}
}

// kill ends the bridge process like a crashed harness.
func (a *agent) kill() { a.cmd.Process.Signal(syscall.SIGKILL) }

// claim takes the first available task and returns its id and lease token.
func (a *agent) claim() (task, lease string) {
	a.t.Helper()
	var l struct {
		Tasks []struct {
			ID string `json:"task_id"`
		} `json:"tasks"`
	}
	a.must("waves_list_available_tasks", map[string]any{}, &l)
	if len(l.Tasks) == 0 {
		a.t.Fatal("no task available")
	}
	var c struct {
		LeaseToken string `json:"lease_token"`
	}
	a.must("waves_claim_task", map[string]any{"task_id": l.Tasks[0].ID}, &c)
	return l.Tasks[0].ID, c.LeaseToken
}

// planned runs planning with the fake agent and approves the workflow and
// the first phase start, through the dashboard.
func (i *inst) planned(name string) (*project, *agent) {
	i.t.Helper()
	return i.plannedWith(name, sampleProposal)
}

func (i *inst) plannedWith(name, proposal string) (*project, *agent) {
	i.t.Helper()
	p := i.newProject(name)
	p.dash.must("POST", "/projects/"+p.id+"/requirements", map[string]string{"kind": "text", "text": "Add hello.txt that says hello"}, nil)
	p.dash.must("POST", "/projects/"+p.id+"/planning-requests", nil, nil)
	a := p.agent(i)
	var req struct {
		ID string `json:"planning_request_id"`
	}
	a.must("waves_get_planning_request", map[string]any{}, &req)
	var prop map[string]any
	json.Unmarshal([]byte(proposal), &prop)
	a.must("waves_submit_workflow_proposal", map[string]any{"planning_request_id": req.ID, "proposal": prop}, nil)
	var d contract.ProjectDetail
	p.dash.must("GET", "/projects/"+p.id, nil, &d)
	if d.Planning.LatestProposal == nil || d.Planning.LatestProposal.WorkflowVersionID == "" {
		i.t.Fatalf("no draft: %+v", d.Planning)
	}
	var sub struct {
		ApprovalID string `json:"approval_id"`
	}
	p.dash.must("POST", "/workflow-versions/"+d.Planning.LatestProposal.WorkflowVersionID+"/submit", nil, &sub)
	p.dash.mustDecide(p.pending("workflow"), "approve", nil)
	p.dash.mustDecide(p.pending("phase_start"), "approve", nil)
	return p, a
}

// phase returns the id and stored state of a phase by key (p1, p2).
func (i *inst) phase(project, key string) (string, string) {
	d := i.db()
	defer d.Close()
	var id, st string
	d.QueryRow(`SELECT p.id, p.state FROM phases p JOIN workflow_versions v ON v.id = p.workflow_version_id JOIN workflows w ON w.id = v.workflow_id
		WHERE w.project_id = ? AND p.key = ? AND v.state = 'approved'`, project, key).Scan(&id, &st)
	return id, st
}

func (i *inst) taskState(id string) string { return i.str(`SELECT state FROM tasks WHERE id = ?`, id) }
func (i *inst) execOf(task string) (string, string) {
	d := i.db()
	defer d.Close()
	var id, st string
	d.QueryRow(`SELECT id, state FROM executions WHERE task_id = ? ORDER BY rowid DESC LIMIT 1`, task).Scan(&id, &st)
	return id, st
}

// waitFor retries cond until it holds or the timeout passes.
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", d, what)
}

// hook runs `waves hook` with a Claude Code PreToolUse event.
func (i *inst) hook(policy, cwd, tool, target, toolUseID string) (string, int) {
	ev, _ := json.Marshal(map[string]any{"session_id": "sess-e2e", "cwd": cwd, "hook_event_name": "PreToolUse", "tool_name": tool, "tool_use_id": toolUseID,
		"tool_input": map[string]any{"file_path": target, "content": "x"}})
	return i.run(string(ev), "hook", "--policy", policy)
}

// setLimit raises max_concurrent (the claim limit) through the settings API.
func (i *inst) setLimit(n int) {
	i.t.Helper()
	c := i.dashboard()
	c.must("PATCH", "/settings", map[string]int{"max_concurrent": n}, nil, elev(c.stepUp("")))
}

// setHeartbeat sets the heartbeat interval (seconds, at least 15).
func (i *inst) setHeartbeat(s int) {
	i.t.Helper()
	i.dashboard().must("PATCH", "/settings", map[string]int{"heartbeat_interval_s": s}, nil)
}
