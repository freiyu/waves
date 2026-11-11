package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"waves/internal/api/contract"
	"waves/internal/config"
	"waves/internal/core"
	"waves/internal/store"
)

// commands are the subcommands besides help, version, serve and setup.
func (e *Env) commands() map[string]func(context.Context, []string) error {
	return map[string]func(context.Context, []string) error{
		"hook":         e.hook,
		"mcp":          e.mcpBridge,
		"start":        e.start,
		"stop":         e.stop,
		"restart":      func(ctx context.Context, a []string) error { e.stop(ctx, nil); return e.start(ctx, a) },
		"status":       e.status,
		"open":         e.open,
		"doctor":       e.doctor,
		"project":      e.project,
		"requirements": e.requirements,
		"plan":         e.plan,
		"workflow":     e.workflow,
		"task":         e.task,
		"execution":    e.execution,
		"approval":     e.approval,
		"agent":        e.agent,
		"guard":        e.guard,
		"auth":         e.auth,
		"backup":       e.backup,
		"restore":      e.restore,
		"gc":           e.gc,
	}
}

func (e *Env) table(rows [][]string) {
	w := tabwriter.NewWriter(e.Out, 0, 2, 2, ' ', 0)
	for _, r := range rows {
		fmt.Fprintln(w, strings.Join(r, "\t"))
	}
	w.Flush()
}

// ---- service lifecycle ----

func (e *Env) pid() int {
	b, err := os.ReadFile(e.Dirs.PIDFile())
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	// Only a process that is still a waves serve counts; pids get reused.
	cmd, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", n))
	if err != nil || !strings.Contains(string(cmd), "serve") {
		return 0
	}
	return n
}

func (e *Env) start(ctx context.Context, _ []string) error {
	if e.healthy(ctx) {
		fmt.Fprintln(e.Out, "Waves is already running.")
		return nil
	}
	if _, err := os.Stat(e.Dirs.OperatorToken()); err != nil {
		return errors.New("Waves is not set up yet; run: waves setup")
	}
	if err := os.MkdirAll(e.Dirs.State, 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(e.Dirs.State, "service.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "serve")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start the service: %w", err)
	}
	cmd.Process.Release()
	for i := 0; i < 50; i++ {
		if e.healthy(ctx) {
			cfg, _ := config.Load(e.Dirs)
			fmt.Fprintf(e.Out, "Waves is running at http://%s. Sign in with: waves open\n", cfg.Addr())
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("the service did not come up; see %s", filepath.Join(e.Dirs.State, "service.log"))
}

func (e *Env) stop(ctx context.Context, _ []string) error {
	pid := e.pid()
	if pid == 0 {
		fmt.Fprintln(e.Out, "Waves is not running.")
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop pid %d: %w", pid, err)
	}
	for i := 0; i < 100; i++ {
		if syscall.Kill(pid, 0) != nil {
			fmt.Fprintln(e.Out, "Waves stopped.")
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("pid %d did not stop within 10 s", pid)
}

func (e *Env) status(ctx context.Context, args []string) error {
	_, asJSON := wantJSON(args)
	c, err := e.client()
	if err != nil {
		return err
	}
	var s contract.StatusResponse
	if err := c.get(ctx, "/status", &s); err != nil {
		return err
	}
	if asJSON {
		return e.printJSON(s)
	}
	fmt.Fprintf(e.Out, "Waves %s at %s. Audit chain %s (%d records).\n", s.ServerVersion, c.base, s.Audit.State, s.Audit.Records)
	fmt.Fprintf(e.Out, "Executions: %d running, %d queued, %d waiting approval, %d failed, %d disconnected or interrupted.\n", s.Counts.Running, s.Counts.Queued, s.Counts.WaitingApproval, s.Counts.Failed, s.Counts.Disconnected)
	if len(s.NeedsAttention) == 0 {
		fmt.Fprintln(e.Out, "Nothing needs you.")
		return nil
	}
	rows := [][]string{{"NEEDS YOU", "SUBJECT", "PROJECT", "REF"}}
	for _, a := range s.NeedsAttention {
		rows = append(rows, []string{a.Label, a.Subject, a.ProjectName, a.RefID})
	}
	e.table(rows)
	if s.NextStep != nil {
		fmt.Fprintln(e.Out, "Next: "+s.NextStep.Text)
	}
	return nil
}

func (e *Env) open(ctx context.Context, _ []string) error {
	c, err := e.client()
	if err != nil {
		return err
	}
	var r struct {
		URL string `json:"url"`
	}
	if err := c.post(ctx, "/auth/login-links", nil, &r); err != nil {
		return err
	}
	fmt.Fprintln(e.Out, "Sign-in link (works once, valid 60 s):")
	fmt.Fprintln(e.Out, r.URL)
	for _, opener := range []string{"wslview", "xdg-open"} {
		if p, err := exec.LookPath(opener); err == nil {
			cmd := exec.Command(p, r.URL)
			if cmd.Start() == nil {
				cmd.Process.Release()
				break
			}
		}
	}
	return nil
}

func (e *Env) doctor(ctx context.Context, _ []string) error {
	ok := true
	check := func(name string, good bool, detail string) {
		mark := "ok  "
		if !good {
			mark, ok = "FAIL", false
		}
		fmt.Fprintf(e.Out, "%s  %-22s %s\n", mark, name, detail)
	}
	for _, d := range []string{e.Dirs.Config, e.Dirs.Data, e.Dirs.State} {
		st, err := os.Stat(d)
		check("dir "+filepath.Base(filepath.Dir(d))+"/"+filepath.Base(d), err == nil && st.Mode().Perm() == 0o700, d)
	}
	for _, f := range []string{e.Dirs.OperatorToken(), e.Dirs.GuardToken()} {
		_, err := config.ReadSecret(f)
		check(filepath.Base(f), err == nil, f)
	}
	for _, bin := range []string{"git", "claude"} {
		p, err := exec.LookPath(bin)
		check(bin, err == nil, p)
	}
	if !e.healthy(ctx) {
		check("service", false, "not running (waves start)")
	} else {
		c, err := e.client()
		if err == nil {
			var s contract.StatusResponse
			err = c.get(ctx, "/status", &s)
			check("service", err == nil, c.base+" version "+s.ServerVersion)
			check("audit chain", s.Audit.State == "verified", fmt.Sprintf("%s, %d records", s.Audit.State, s.Audit.Records))
			var h contract.HarnessList
			if c.get(ctx, "/harnesses", &h) == nil && len(h.Items) > 0 {
				check("claude code", h.Items[0].Installed, h.Items[0].Version)
			}
			// A recorded connection whose hook is gone or changed on disk means the guard is not active.
			var pl contract.ProjectList
			if c.get(ctx, "/projects", &pl) == nil {
				for _, pr := range pl.Items {
					var g contract.GuardInfo
					if c.get(ctx, "/projects/"+pr.ID+"/guard", &g) != nil {
						continue
					}
					if g.Drift != "" {
						check("hook "+pr.Name, false, g.Drift+". Fix the file, then: waves project switch "+pr.Name+" and waves guard "+g.Policy)
					} else if g.Installed {
						check("hook "+pr.Name, true, "guard "+g.Policy)
					}
				}
			}
		}
	}
	if !ok {
		return exitCode(1)
	}
	return nil
}

// ---- projects and planning ----

func (e *Env) activeProject(ctx context.Context, c *client) (contract.Project, error) {
	var l contract.ProjectList
	if err := c.get(ctx, "/projects", &l); err != nil {
		return contract.Project{}, err
	}
	for _, p := range l.Items {
		if p.Active {
			return p, nil
		}
	}
	return contract.Project{}, errors.New("no active project; add one with: waves project add <path>")
}

func (e *Env) projectByName(ctx context.Context, c *client, name string) (contract.Project, error) {
	var l contract.ProjectList
	if err := c.get(ctx, "/projects", &l); err != nil {
		return contract.Project{}, err
	}
	for _, p := range l.Items {
		if p.Name == name || p.ID == name {
			return p, nil
		}
	}
	return contract.Project{}, fmt.Errorf("no project named %q; see: waves project list", name)
}

func (e *Env) project(ctx context.Context, args []string) error {
	args, asJSON := wantJSON(args)
	c, err := e.client()
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "list" {
		var l contract.ProjectList
		if err := c.get(ctx, "/projects", &l); err != nil {
			return err
		}
		if asJSON {
			return e.printJSON(l)
		}
		rows := [][]string{{"", "NAME", "PHASE", "APPROVALS", "PATH"}}
		for _, p := range l.Items {
			act, phase := "", "no workflow yet"
			if p.Active {
				act = "*"
			}
			if p.ActivePhase != nil {
				phase = p.ActivePhase.Code + " " + p.ActivePhase.Title + " (" + p.ActivePhase.State + ")"
			} else if p.ProgressPct == 100 {
				phase = "all phases completed"
			}
			rows = append(rows, []string{act, p.Name, phase, strconv.Itoa(p.PendingApprovals), p.Path})
		}
		e.table(rows)
		return nil
	}
	if len(args) < 2 {
		return errUsage
	}
	switch args[0] {
	case "add":
		path, err := filepath.Abs(args[1])
		if err != nil {
			return err
		}
		var p contract.Project
		if err := c.post(ctx, "/projects", map[string]string{"path": path}, &p); err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "Registered %s (%s). Files are never changed by registering.\n", p.Name, p.ID)
	case "switch":
		p, err := e.projectByName(ctx, c, args[1])
		if err != nil {
			return err
		}
		if err := c.post(ctx, "/projects/"+p.ID+"/activate", nil, nil); err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "Active project: %s.\n", p.Name)
	case "remove":
		p, err := e.projectByName(ctx, c, args[1])
		if err != nil {
			return err
		}
		if err := c.do(ctx, "DELETE", "/projects/"+p.ID, nil, nil, nil); err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "Removed %s from Waves. Its files were not touched.\n", p.Name)
	default:
		return errUsage
	}
	return nil
}

func (e *Env) requirements(ctx context.Context, args []string) error {
	if len(args) < 2 || args[0] != "add" {
		return errUsage
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	p, err := e.activeProject(ctx, c)
	if err != nil {
		return err
	}
	body := map[string]string{"kind": "file", "path": args[1]}
	if args[1] == "--text" {
		if len(args) < 3 {
			return errors.New(`usage: waves requirements add --text "<text>"`)
		}
		body = map[string]string{"kind": "text", "text": strings.Join(args[2:], " ")}
	} else if abs, err := filepath.Abs(args[1]); err == nil {
		body["path"] = abs
	}
	var r contract.Requirement
	if err := c.post(ctx, "/projects/"+p.ID+"/requirements", body, &r); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "Added requirement %s to %s.\n", r.Source, p.Name)
	return nil
}

func (e *Env) plan(ctx context.Context, args []string) error {
	if len(args) != 1 || args[0] != "request" {
		return errUsage
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	p, err := e.activeProject(ctx, c)
	if err != nil {
		return err
	}
	var r contract.PlanningRequest
	if err := c.post(ctx, "/projects/"+p.ID+"/planning-requests", nil, &r); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "Planning request %s is open for %s.\n", r.ID, p.Name)
	fmt.Fprintln(e.Out, "Start Claude Code in the project and ask: Fetch the Waves planning request and submit a workflow proposal.")
	return nil
}

func (e *Env) workflow(ctx context.Context, args []string) error {
	args, asJSON := wantJSON(args)
	if len(args) != 1 || args[0] != "show" {
		return errUsage
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	p, err := e.activeProject(ctx, c)
	if err != nil {
		return err
	}
	var wi contract.WorkflowInfo
	if err := c.get(ctx, "/projects/"+p.ID+"/workflow", &wi); err != nil {
		return err
	}
	if wi.CurrentID == "" {
		fmt.Fprintln(e.Out, "No workflow yet. Create a planning request: waves plan request")
		return nil
	}
	var v contract.WorkflowVersion
	if err := c.get(ctx, "/workflow-versions/"+wi.CurrentID, &v); err != nil {
		return err
	}
	if asJSON {
		return e.printJSON(v)
	}
	fmt.Fprintf(e.Out, "%s workflow v%d (%s), content hash %s\n", v.ProjectName, v.Number, v.State, v.ContentHash)
	for _, ph := range v.Phases {
		fmt.Fprintf(e.Out, "\n%s %s\n  %s\n", ph.Code, ph.Title, ph.Objective)
		for _, t := range ph.Tasks {
			fmt.Fprintf(e.Out, "  task %-10s %s (%s)\n", t.Code, t.Title, t.Mode)
		}
		for _, val := range ph.Validators {
			if val.Kind == "command" {
				fmt.Fprintf(e.Out, "  check %s: %s (timeout %d s)\n", val.Name, val.Command, val.TimeoutS)
			} else {
				fmt.Fprintf(e.Out, "  checklist %s: %s\n", val.Name, strings.Join(val.Items, "; "))
			}
		}
	}
	return nil
}

func (e *Env) task(ctx context.Context, args []string) error {
	args, asJSON := wantJSON(args)
	if len(args) == 0 {
		return errUsage
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		p, err := e.activeProject(ctx, c)
		if err != nil {
			return err
		}
		var d contract.ProjectDetail
		if err := c.get(ctx, "/projects/"+p.ID, &d); err != nil {
			return err
		}
		if asJSON {
			return e.printJSON(d.Tasks)
		}
		rows := [][]string{{"TASK", "STATE", "MODE", "TITLE", "ID"}}
		for _, t := range d.Tasks {
			st := t.State
			if t.StateDetail != "" {
				st += " (" + t.StateDetail + ")"
			}
			rows = append(rows, []string{t.Code, st, t.Mode, t.Title, t.ID})
		}
		e.table(rows)
	case "run":
		return c.post(ctx, "/tasks/"+url.PathEscape(arg(args, 1))+"/run", nil, nil)
	case "reopen":
		// After a rejected phase review: send a finished task back to work (passphrase).
		if len(args) < 3 {
			return errUsage
		}
		var d struct {
			PhaseID string `json:"phase_id"`
		}
		if err := c.get(ctx, "/tasks/"+url.PathEscape(args[1]), &d); err != nil {
			return err
		}
		hdr, err := e.elevate(ctx, c)
		if err != nil {
			return err
		}
		body := map[string]any{"task_ids": []string{args[1]}, "reason": strings.Join(args[2:], " ")}
		if err := c.do(ctx, "POST", "/phases/"+url.PathEscape(d.PhaseID)+"/reopen", body, nil, hdr); err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "Task %s is Ready again; the agent sees the review feedback when it claims it.\n", args[1])
	case "cancel", "retry":
		if len(args) < 2 {
			return errUsage
		}
		body := map[string]string{"reason": strings.Join(args[2:], " ")}
		if err := c.post(ctx, "/tasks/"+url.PathEscape(args[1])+"/"+args[0], body, nil); err != nil {
			return err
		}
		fmt.Fprintln(e.Out, map[string]string{"cancel": "Task " + args[1] + " cancelled.", "retry": "Task " + args[1] + " is Ready again."}[args[0]])
	default:
		return errUsage
	}
	return nil
}

// taskStateOf returns a task's state for messages; "unknown" if it cannot be read.
func taskStateOf(ctx context.Context, c *client, id string) string {
	var t struct {
		Task contract.TaskSummary `json:"task"`
	}
	if c.get(ctx, "/tasks/"+url.PathEscape(id), &t) != nil || t.Task.State == "" {
		return "unknown"
	}
	return strings.ReplaceAll(t.Task.State, "_", " ")
}

func arg(a []string, i int) string {
	if i < len(a) {
		return a[i]
	}
	return ""
}

// ---- executions ----

func (e *Env) execution(ctx context.Context, args []string) error {
	args, asJSON := wantJSON(args)
	if len(args) == 0 {
		return errUsage
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		var l contract.ExecutionList
		if err := c.get(ctx, "/executions", &l); err != nil {
			return err
		}
		if asJSON {
			return e.printJSON(l)
		}
		rows := [][]string{{"EXECUTION", "STATE", "TASK", "PROJECT"}}
		for _, x := range l.Items {
			rows = append(rows, []string{x.ID, x.State, x.TaskCode + " " + x.TaskTitle, x.ProjectName})
		}
		e.table(rows)
	case "cancel":
		if len(args) != 2 {
			return errUsage
		}
		if err := c.post(ctx, "/executions/"+url.PathEscape(args[1])+"/cancel", nil, nil); err != nil {
			return err
		}
		var d contract.ExecutionDetail
		if err := c.get(ctx, "/executions/"+url.PathEscape(args[1]), &d); err != nil {
			return err
		}
		ts := taskStateOf(ctx, c, d.TaskID)
		fmt.Fprintf(e.Out, "Execution %s is %s. Its task %s is %s.", d.ID, strings.ReplaceAll(d.State, "_", " "), d.TaskCode, ts)
		switch {
		case strings.HasPrefix(ts, "blocked"):
			fmt.Fprintf(e.Out, " Retry it with: waves task retry %s \"<reason>\"", d.TaskID)
		case ts == "waiting approval":
			fmt.Fprint(e.Out, " Decide it in the approval inbox: waves approval list")
		}
		fmt.Fprintln(e.Out)
	case "logs":
		if len(args) < 2 {
			return errUsage
		}
		follow := len(args) > 2 && args[2] == "-f"
		return e.logs(ctx, c, args[1], follow)
	default:
		return errUsage
	}
	return nil
}

// logs prints the log; with follow it waits for execution.log events on the
// event stream (no polling) until the execution ends.
func (e *Env) logs(ctx context.Context, c *client, id string, follow bool) error {
	var offset int
	fetch := func() error {
		var s string
		if err := c.get(ctx, fmt.Sprintf("/executions/%s/log?offset=%d", url.PathEscape(id), offset), &s); err != nil {
			return err
		}
		offset += len(s)
		fmt.Fprint(e.Out, s)
		return nil
	}
	if err := fetch(); err != nil || !follow {
		return err
	}
	req, _ := newStreamRequest(ctx, c, "/events")
	res, err := (&httpClientNoTimeout).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	event := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = line[7:]
		case strings.HasPrefix(line, "data: ") && strings.Contains(line, `"`+id+`"`):
			if event == "execution.log" {
				if err := fetch(); err != nil {
					return err
				}
			}
			if event == "execution.changed" && (strings.Contains(line, `"to":"completed"`) || strings.Contains(line, `"to":"failed"`) || strings.Contains(line, `"to":"cancelled"`) || strings.Contains(line, `"to":"interrupted"`)) {
				fetch()
				return nil
			}
		}
	}
	return sc.Err()
}

// ---- approvals ----

func (e *Env) approval(ctx context.Context, args []string) error {
	args, asJSON := wantJSON(args)
	if len(args) == 0 {
		return errUsage
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		var l contract.ApprovalList
		if err := c.get(ctx, "/approvals?state=pending", &l); err != nil {
			return err
		}
		if asJSON {
			return e.printJSON(l)
		}
		if len(l.Items) == 0 {
			fmt.Fprintln(e.Out, "No approval is waiting.")
			return nil
		}
		rows := [][]string{{"APPROVAL", "KIND", "SUBJECT", "PROJECT", "REQUESTED BY"}}
		for _, a := range l.Items {
			rows = append(rows, []string{a.ID, a.CategoryLabel, a.Subject, a.ProjectName, a.RequestedBy})
		}
		e.table(rows)
		return nil
	case "review":
		if len(args) != 2 {
			return errUsage
		}
		return e.review(ctx, c, args[1])
	}
	return errUsage
}

func (e *Env) readLine(prompt string) (string, error) {
	fmt.Fprint(e.Out, prompt)
	r := bufio.NewReader(e.In)
	s, err := r.ReadString('\n')
	return strings.TrimSpace(s), err
}

// review shows one approval and decides it. It needs a terminal, the typed
// phase key for phase gates, and the passphrase for a fresh step-up.
func (e *Env) review(ctx context.Context, c *client, id string) error {
	if !c.tty {
		return errors.New("approval review needs an interactive terminal")
	}
	var d contract.ApprovalDetail
	if err := c.get(ctx, "/approvals/"+url.PathEscape(id), &d); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "%s: %s\nProject: %s · requested by %s\n\n", d.CategoryLabel, d.Action, d.ProjectName, d.RequestedBy)
	for _, f := range d.Facts {
		fmt.Fprintf(e.Out, "  %s: %s\n", f.Label, f.Value)
	}
	if len(d.Commands) > 0 {
		fmt.Fprintln(e.Out, "\nCommands Waves will run:")
		for _, cmd := range d.Commands {
			fmt.Fprintln(e.Out, "  "+cmd)
		}
	}
	fmt.Fprintf(e.Out, "\n%s\n\n", d.Impact)
	if d.State != "pending" {
		fmt.Fprintf(e.Out, "This approval is already %s.\n", d.State)
		return nil
	}
	choice, err := e.readLine("Decide: approve, reject, or skip? ")
	if err != nil {
		return err
	}
	body := map[string]string{"subject_hash": d.SubjectHash}
	switch choice {
	case "approve":
		body["decision"] = "approve"
		if d.PhaseID != "" {
			key := strings.Fields(d.Subject)
			want := ""
			if len(key) > 0 {
				want = key[0]
			}
			typed, err := e.readLine(fmt.Sprintf("Type the phase key (%s) to confirm: ", want))
			if err != nil {
				return err
			}
			if !strings.EqualFold(typed, want) {
				return errors.New("the phase key did not match; nothing was approved")
			}
		}
		if d.Category == "phase_completion" {
			var rv contract.PhaseReview
			if c.get(ctx, "/phases/"+d.PhaseID+"/review", &rv) == nil && rv.RequiresOverride {
				reason, err := e.readLine("A validator failed or is unconfirmed. Override reason: ")
				if err != nil {
					return err
				}
				body["override_reason"] = reason
			}
		}
	case "reject":
		body["decision"] = "reject"
		fb, err := e.readLine("What must change? ")
		if err != nil {
			return err
		}
		body["feedback"] = fb
	default:
		fmt.Fprintln(e.Out, "Nothing was decided.")
		return nil
	}
	hdr := map[string]string{}
	if body["decision"] == "approve" || d.RejectNeedsStepUp {
		pass, err := e.readSecretTTY("Approval passphrase: ")
		if err != nil {
			return err
		}
		if pass == "" {
			return errors.New("no passphrase entered; nothing was decided")
		}
		var el contract.StepUpResponse
		if err := c.post(ctx, "/auth/stepup", map[string]string{"passphrase": pass, "approval_id": id}, &el); err != nil {
			return err
		}
		hdr["X-Waves-Elevation"] = el.ElevationSecret
	}
	if err := c.do(ctx, "POST", "/approvals/"+url.PathEscape(id)+"/decision", body, nil, hdr); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "Recorded: %s.\n", body["decision"])
	return nil
}

// ---- agent and guard ----

func (e *Env) elevate(ctx context.Context, c *client) (map[string]string, error) {
	pass, err := e.readSecretTTY("Approval passphrase: ")
	if err != nil {
		return nil, err
	}
	if pass == "" {
		return nil, errors.New("no passphrase entered; nothing was changed")
	}
	var el contract.StepUpResponse
	if err := c.post(ctx, "/auth/stepup", map[string]string{"passphrase": pass}, &el); err != nil {
		return nil, err
	}
	return map[string]string{"X-Waves-Elevation": el.ElevationSecret}, nil
}

func (e *Env) agent(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		var h contract.HarnessList
		if err := c.get(ctx, "/harnesses", &h); err != nil {
			return err
		}
		for _, x := range h.Items {
			fmt.Fprintf(e.Out, "%s %s · installed %s · connected: %s\n", x.Name, x.Version, yesNo(x.Installed), strings.Join(x.ConnectedProjects, ", "))
			for _, cp := range x.Capabilities {
				fmt.Fprintf(e.Out, "  %-18s %-11s %s\n", cp.Name, cp.Value, cp.Note)
			}
		}
		return nil
	case "status":
		return e.guard(ctx, []string{"status"})
	case "connect", "disconnect":
		if args[0] == "connect" && (len(args) < 2 || args[1] != "claude-code") {
			return errors.New("usage: waves agent connect claude-code")
		}
		p, err := e.activeProject(ctx, c)
		if err != nil {
			return err
		}
		var plan contract.ConnectPlan
		if args[0] == "connect" {
			if err := c.get(ctx, "/projects/"+p.ID+"/agent/connect-plan", &plan); err != nil {
				return err
			}
			fmt.Fprintln(e.Out, "Connecting writes exactly this (project files only, never your user settings):")
			for _, l := range plan.Lines {
				fmt.Fprintln(e.Out, "  "+l)
			}
		} else {
			fmt.Fprintln(e.Out, "Disconnecting removes the Waves hook and MCP entry from "+p.Name+" and revokes its credential.")
		}
		if ok, err := e.readLine("Continue? Type yes: "); err != nil || ok != "yes" {
			fmt.Fprintln(e.Out, "Nothing was changed.")
			return nil
		}
		hdr, err := e.elevate(ctx, c)
		if err != nil {
			return err
		}
		path, body := "/projects/"+p.ID+"/agent/disconnect", map[string]string{}
		if args[0] == "connect" {
			path, body = "/projects/"+p.ID+"/agent/connect", map[string]string{"plan_hash": plan.PlanHash}
		}
		if err := c.do(ctx, "POST", path, body, nil, hdr); err != nil {
			return err
		}
		fmt.Fprintln(e.Out, map[string]string{"connect": "Claude Code connected to " + p.Name + ".", "disconnect": "Claude Code disconnected from " + p.Name + "."}[args[0]])
		return nil
	}
	return errUsage
}

func (e *Env) guard(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errUsage
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	p, err := e.activeProject(ctx, c)
	if err != nil {
		return err
	}
	switch args[0] {
	case "status":
		var g contract.GuardInfo
		if err := c.get(ctx, "/projects/"+p.ID+"/guard", &g); err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "%s: guard %s, hook installed %s, unmanaged sessions %s, would-deny in 24 h: %d\n", p.Name, g.Policy, yesNo(g.Installed), g.UnmanagedSessions, g.WouldDeny24h)
		if g.Drift != "" {
			fmt.Fprintf(e.Out, "WARNING: %s. The guard is not active. Fix the file, then run: waves guard %s\n", g.Drift, g.Policy)
		}
		fmt.Fprintln(e.Out, "Not defended: "+g.NotDefended)
		return nil
	case "off", "audit", "enforce":
		fmt.Fprintln(e.Out, map[string]string{
			"off":     "off: the hook allows every call and records nothing.",
			"audit":   "audit: nothing is blocked; would-be denials are recorded.",
			"enforce": "enforce: file writes without a lease are denied, and any hook failure denies.",
		}[args[0]])
		fmt.Fprintln(e.Out, "This changes the guard policy of "+p.Name+". Your passphrase step-up allows this one change.")
		hdr, err := e.elevate(ctx, c)
		if err != nil {
			return err
		}
		if err := c.do(ctx, "PATCH", "/projects/"+p.ID+"/settings", map[string]string{"guard_policy": args[0]}, nil, hdr); err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "Guard policy for %s: %s.\n", p.Name, args[0])
		return nil
	}
	return errUsage
}

// ---- maintenance ----

func (e *Env) auth(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "passphrase" {
		return errUsage
	}
	if len(args) == 2 && args[1] == "--reset" {
		return e.resetPassphrase(ctx)
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	cur, err := e.readSecretTTY("Current passphrase: ")
	if err != nil {
		return err
	}
	var el contract.StepUpResponse
	if err := c.post(ctx, "/auth/stepup", map[string]string{"passphrase": cur}, &el); err != nil {
		return err
	}
	next, err := e.newPassphrase()
	if err != nil {
		return err
	}
	if err := c.do(ctx, "PUT", "/auth/passphrase", map[string]string{"current": cur, "new": next}, nil, map[string]string{"X-Waves-Elevation": el.ElevationSecret}); err != nil {
		return err
	}
	fmt.Fprintln(e.Out, "Passphrase changed.")
	return nil
}

func (e *Env) newPassphrase() (string, error) {
	for {
		p1, err := e.readSecretTTY("New passphrase: ")
		if err != nil {
			return "", err
		}
		if err := core.CheckPassphraseStrength(p1); err != nil {
			fmt.Fprintln(e.Err, "  "+err.Error()+".")
			continue
		}
		p2, err := e.readSecretTTY("Again:          ")
		if err != nil {
			return "", err
		}
		if p1 == p2 {
			return p1, nil
		}
		fmt.Fprintln(e.Err, "  The two entries differ.")
	}
}

// offline refuses to run while the service is up and asks for a typed word.
func (e *Env) offline(ctx context.Context, word, what string) error {
	if e.healthy(ctx) || e.pid() != 0 {
		return errors.New("stop the service first: waves stop")
	}
	if !term.IsTerminal(int(e.In.Fd())) {
		return errors.New("this needs an interactive terminal")
	}
	typed, err := e.readLine(fmt.Sprintf("%s Type %q to continue: ", what, word))
	if err != nil {
		return err
	}
	if typed != word {
		return errors.New("confirmation did not match; nothing was changed")
	}
	return nil
}

func (e *Env) resetPassphrase(ctx context.Context) error {
	if err := e.offline(ctx, "reset", "This replaces the approval passphrase and signs out every session."); err != nil {
		return err
	}
	next, err := e.newPassphrase()
	if err != nil {
		return err
	}
	c, db, bus, err := openCore(ctx, e.Dirs, nil)
	if err != nil {
		return err
	}
	defer db.Close()
	defer bus.Close()
	if err := c.ResetPassphrase(ctx, next); err != nil {
		return err
	}
	fmt.Fprintln(e.Out, "Passphrase reset. All sessions were signed out.")
	return nil
}

func (e *Env) backup(ctx context.Context, args []string) error {
	if len(args) > 1 {
		return errUsage
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	// The service writes only into its own backup folder; a path of your choosing is a copy this command makes as you.
	var r struct {
		Bytes int    `json:"bytes"`
		Path  string `json:"path"`
	}
	if err := c.post(ctx, "/backup", map[string]string{}, &r); err != nil {
		return err
	}
	if len(args) == 0 {
		fmt.Fprintf(e.Out, "Backup written: %s (%d bytes).\n", r.Path, r.Bytes)
		return nil
	}
	dst, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	// The path comes from the service's answer and is about to be deleted: it must be a backup in our own folder.
	if filepath.Dir(r.Path) != e.Dirs.Backups() || !strings.HasPrefix(filepath.Base(r.Path), "waves-") || !strings.HasSuffix(r.Path, ".db") {
		return fmt.Errorf("the service named an unexpected backup file %q; nothing was copied or deleted", r.Path)
	}
	if err := copyPrivate(r.Path, dst); err != nil {
		return fmt.Errorf("the backup is at %s but could not be copied to %s: %w", r.Path, dst, err)
	}
	os.Remove(r.Path) // the copy you asked for replaces the one in the backup folder
	fmt.Fprintf(e.Out, "Backup written: %s (%d bytes).\n", dst, r.Bytes)
	return nil
}

// copyPrivate copies src to a new file dst (mode 600, never over an existing file or through a symlink).
func copyPrivate(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	return nil
}

func (e *Env) restore(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errUsage
	}
	if err := e.offline(ctx, "restore", "This replaces the Waves database with "+args[0]+". The current one is kept next to it."); err != nil {
		return err
	}
	// The restore writes its own audit record into the restored file before it goes into place.
	if _, err := store.Restore(ctx, args[0], e.Dirs.DB()); err != nil {
		return err
	}
	fmt.Fprintln(e.Out, "Restored. Start the service with: waves start")
	fmt.Fprintln(e.Out, "The passphrase, approvals and audit log are now those of the backup file. Acknowledge the alert on the Activity page after you check them.")
	return nil
}

func (e *Env) gc(ctx context.Context, _ []string) error {
	c, err := e.client()
	if err != nil {
		return err
	}
	var r struct {
		Removed int `json:"removed_files"`
		Bytes   int `json:"freed_bytes"`
	}
	if err := c.post(ctx, "/gc", nil, &r); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "Removed %d old log files (%d bytes).\n", r.Removed, r.Bytes)
	return nil
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
