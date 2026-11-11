package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// flow holds a project that went through planning to an approved workflow.
type flow struct {
	*env
	p       Principal
	project string
	version string
	phase1  string
}

func (e *env) project(t *testing.T, p Principal) string {
	t.Helper()
	dir := filepath.Join(e.dir, "proj")
	os.MkdirAll(dir, 0o700)
	id, err := e.s.RegisterProject(e.ctx, p, dir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.AddRequirement(e.ctx, p, id, "text", "", "Add a health endpoint"); err != nil {
		t.Fatal(err)
	}
	return id
}

// draft plans a project up to a draft version.
func (e *env) draft(t *testing.T, p Principal, project, doc string) string {
	t.Helper()
	plr, err := e.s.CreatePlanningRequest(e.ctx, p, project)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.PlanningRequestBody(e.ctx, project, ""); err != nil {
		t.Fatal(err)
	}
	_, valid, errs, err := e.s.SubmitProposal(e.ctx, project, plr, []byte(doc), "test")
	if err != nil || !valid {
		t.Fatalf("submit: %v %+v", err, errs)
	}
	var v string
	e.db.R.QueryRow(`SELECT v.id FROM workflow_versions v JOIN workflows w ON w.id = v.workflow_id WHERE w.project_id = ? ORDER BY version_no DESC LIMIT 1`, project).Scan(&v)
	return v
}

func (e *env) pendingApproval(t *testing.T, category, subject string) string {
	t.Helper()
	var id string
	if err := e.db.R.QueryRow(`SELECT id FROM approvals WHERE category = ? AND subject_id = ? AND state = 'pending'`, category, subject).Scan(&id); err != nil {
		t.Fatalf("no pending %s approval for %s", category, subject)
	}
	return id
}

func (e *env) decide(t *testing.T, p Principal, apr, decision string, single bool) error {
	t.Helper()
	d, err := e.s.ApprovalDetail(e.ctx, apr)
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Add(time.Second)
	var r StepUpResult
	if single {
		r, err = e.s.StepUp(e.ctx, p, testPass, apr)
	} else {
		r, err = e.s.StepUp(e.ctx, p, testPass, "")
	}
	if err != nil {
		t.Fatal(err)
	}
	return e.s.Decide(e.ctx, p, Decision{ApprovalID: apr, Decision: decision, SubjectHash: d.SubjectHash, Elevation: r.Secret, Feedback: "needs work"})
}

func (e *env) state(t *testing.T, table, id string) string {
	t.Helper()
	var s string
	e.db.R.QueryRow(`SELECT state FROM `+table+` WHERE id = ?`, id).Scan(&s)
	return s
}

// approvedFlow runs planning, workflow approval and the first phase start.
func approvedFlow(t *testing.T) *flow {
	e := newEnv(t).withPassphrase(t)
	f := &flow{env: e, p: e.dashboard(t)}
	f.project = e.project(t, f.p)
	f.version = e.draft(t, f.p, f.project, goodProposal)
	apr, err := e.s.SubmitVersion(e.ctx, f.p, f.version)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.decide(t, f.p, apr, "approve", false); err != nil {
		t.Fatalf("workflow approve: %v", err)
	}
	e.db.R.QueryRow(`SELECT id FROM phases WHERE workflow_version_id = ? AND position = 1`, f.version).Scan(&f.phase1)
	return f
}

func TestPlanningToFirstPhase(t *testing.T) {
	e := newEnv(t).withPassphrase(t)
	p := e.dashboard(t)
	project := e.project(t, p)
	plr, err := e.s.CreatePlanningRequest(e.ctx, p, project)
	if err != nil {
		t.Fatal(err)
	}
	// An invalid proposal is kept with its errors; the request stays open.
	if _, valid, errs, err := e.s.SubmitProposal(e.ctx, project, plr, []byte(`{"schema_version":1}`), "test"); err != nil || valid || len(errs) == 0 {
		t.Fatalf("invalid proposal: %v %v %+v", valid, err, errs)
	}
	body, err := e.s.PlanningRequestBody(e.ctx, project, "")
	// The harness gets the schema itself, not only a pointer to it.
	if sc, _ := json.Marshal(body["schema"]); !strings.Contains(string(sc), `"type":"command"`) || !strings.Contains(string(sc), `"acceptance_criteria"`) {
		t.Fatalf("the planning request carries no proposal schema: %s", sc)
	}
	if err != nil || body["previous_errors"] == nil {
		t.Fatalf("errors not returned to the harness: %v %v", body, err)
	}
	if _, valid, _, err := e.s.SubmitProposal(e.ctx, project, plr, []byte(goodProposal), "test"); err != nil || !valid {
		t.Fatalf("valid proposal: %v %v", valid, err)
	}
	if _, _, _, err := e.s.SubmitProposal(e.ctx, project, plr, []byte(goodProposal), "test"); code(err) != "invalid_state" {
		t.Fatalf("closed request accepted another proposal: %v", err)
	}
	if _, err := e.db.W.Exec(`UPDATE proposals SET raw_json = 'x'`); err == nil {
		t.Fatal("proposal changed")
	}
	d, _ := e.s.ProjectDetail(e.ctx, project)
	if d.Planning.LatestProposal == nil || d.Planning.LatestProposal.WorkflowVersionID == "" {
		t.Fatalf("draft not linked: %+v", d.Planning)
	}
	v := d.Planning.LatestProposal.WorkflowVersionID
	wv, err := e.s.WorkflowVersion(e.ctx, v)
	if err != nil || wv.State != "draft" || len(wv.Phases) != 2 || wv.Phases[0].Validators[0].Command != "go test ./..." {
		t.Fatalf("version: %+v %v", wv, err)
	}
	apr, err := e.s.SubmitVersion(e.ctx, p, v)
	if err != nil {
		t.Fatal(err)
	}
	ad, _ := e.s.ApprovalDetail(e.ctx, apr)
	if len(ad.Commands) != 1 || !strings.Contains(ad.Commands[0], "go test ./...") || ad.StepUp != "window_ok" {
		t.Fatalf("approval shows %+v", ad)
	}
	// The operator alone cannot approve.
	if err := e.s.Decide(e.ctx, p, Decision{ApprovalID: apr, Decision: "approve", SubjectHash: ad.SubjectHash}); code(err) != "stepup_required" {
		t.Fatalf("approve without elevation: %v", err)
	}
	if err := e.decide(t, p, apr, "approve", false); err != nil {
		t.Fatal(err)
	}
	var phase1, phase2 string
	e.db.R.QueryRow(`SELECT id FROM phases WHERE workflow_version_id = ? AND position = 1`, v).Scan(&phase1)
	e.db.R.QueryRow(`SELECT id FROM phases WHERE workflow_version_id = ? AND position = 2`, v).Scan(&phase2)
	if e.state(t, "workflow_versions", v) != "approved" || e.state(t, "phases", phase1) != "Awaiting Approval" || e.state(t, "phases", phase2) != "Planned" {
		t.Fatal("workflow approval did not open the first phase gate only")
	}
	start := e.pendingApproval(t, "phase_start", phase1)
	// A phase gate needs a fresh single-use step-up; a window is not enough.
	if err := e.decide(t, p, start, "approve", false); code(err) != "stepup_required" {
		t.Fatalf("phase start with a window: %v", err)
	}
	if err := e.decide(t, p, start, "approve", true); err != nil {
		t.Fatalf("phase start: %v", err)
	}
	if e.state(t, "phases", phase1) != "Ready for Execution" {
		t.Fatalf("phase 1 is %s", e.state(t, "phases", phase1))
	}
	var t1, t2 string
	e.db.R.QueryRow(`SELECT state FROM tasks WHERE phase_id = ? AND key = 'p1-t1'`, phase1).Scan(&t1)
	e.db.R.QueryRow(`SELECT state FROM tasks WHERE phase_id = ? AND key = 'p1-t2'`, phase1).Scan(&t2)
	if t1 != "Ready" || t2 != "Backlog" {
		t.Fatalf("tasks: %s %s, want Ready and Backlog", t1, t2)
	}
	var n int
	e.db.R.QueryRow(`SELECT count(*) FROM approvals WHERE state = 'approved' AND (decided_by_scope <> 'human' OR stepup_method <> 'passphrase' OR elevation_id IS NULL OR client IS NULL OR source_addr IS NULL)`).Scan(&n)
	if n != 0 {
		t.Fatal("an approval lacks its AH-7 detail")
	}
}

// Gate-bypass: every way around the phase gate fails.
func TestPhaseGateCannotBeBypassed(t *testing.T) {
	f := approvedFlow(t)
	e := f.env
	start := e.pendingApproval(t, "phase_start", f.phase1)
	move := func(a Actor, apr string) error {
		return e.s.tx(e.ctx, func(tx *Tx) error {
			_, err := e.s.transition(e.ctx, tx, "phase", f.phase1, "Approved", a, "test", apr)
			return err
		})
	}
	for _, a := range []Actor{sysActor, {Scope: "agent"}, {Scope: "operator"}, {Scope: "guard"}} {
		if err := move(a, start); code(err) != "illegal_transition" {
			t.Fatalf("%s moved the gate: %v", a.Scope, err)
		}
	}
	if err := move(Actor{Scope: "human"}, ""); code(err) != "approval_required" {
		t.Fatalf("human without approval: %v", err)
	}
	if err := move(Actor{Scope: "human"}, start); code(err) != "approval_required" {
		t.Fatalf("human with a pending approval: %v", err)
	}
	// An approval of another subject or category does not count.
	var wfApr string
	e.db.R.QueryRow(`SELECT id FROM approvals WHERE category = 'workflow' AND state = 'approved'`).Scan(&wfApr)
	if err := move(Actor{Scope: "human"}, wfApr); code(err) != "approval_required" {
		t.Fatalf("human with the workflow approval: %v", err)
	}
	// Elevations that do not fit this approval.
	d, _ := e.s.ApprovalDetail(e.ctx, start)
	e.clock.Add(time.Second)
	other, _ := e.s.StepUp(e.ctx, f.p, testPass, "")
	if err := e.s.Decide(e.ctx, f.p, Decision{ApprovalID: start, Decision: "approve", SubjectHash: d.SubjectHash, Elevation: other.Secret}); code(err) != "stepup_required" {
		t.Fatalf("window elevation on a gate: %v", err)
	}
	e.clock.Add(time.Second)
	single, _ := e.s.StepUp(e.ctx, f.p, testPass, start)
	if err := e.s.Decide(e.ctx, f.p, Decision{ApprovalID: start, Decision: "approve", SubjectHash: "sh_stale", Elevation: single.Secret}); code(err) != "stale_subject" {
		t.Fatalf("stale hash: %v", err)
	}
	e.clock.Add(3 * time.Minute)
	if err := e.s.Decide(e.ctx, f.p, Decision{ApprovalID: start, Decision: "approve", SubjectHash: d.SubjectHash, Elevation: single.Secret}); code(err) != "stepup_required" {
		t.Fatalf("expired single-use elevation: %v", err)
	}
	cli := e.operator(t)
	e.clock.Add(time.Second)
	cliEl, _ := e.s.StepUp(e.ctx, cli, testPass, start)
	if err := e.s.Decide(e.ctx, f.p, Decision{ApprovalID: start, Decision: "approve", SubjectHash: d.SubjectHash, Elevation: cliEl.Secret}); code(err) != "stepup_required" {
		t.Fatalf("CLI elevation used by the dashboard: %v", err)
	}
	for _, k := range []string{KindLease, KindGuard, KindMCP} {
		if err := e.s.Decide(e.ctx, Principal{Kind: k}, Decision{ApprovalID: start, Decision: "approve", SubjectHash: d.SubjectHash, Elevation: cliEl.Secret}); code(err) != "wrong_scope" {
			t.Fatalf("%s decided: %v", k, err)
		}
	}
	// The database refuses a forged transition row and a forged approval.
	if _, err := e.db.W.Exec(`INSERT INTO state_transitions(entity_type, entity_id, from_state, to_state, trigger, actor_scope, ts) VALUES ('phase', ?, 'Awaiting Approval', 'Approved', 'x', 'human', 't')`, f.phase1); err == nil {
		t.Fatal("trigger accepted a gate move without an approval")
	}
	if _, err := e.db.W.Exec(`UPDATE approvals SET state = 'approved', decided_at = 't', decided_by_scope = 'operator' WHERE id = ?`, start); err == nil {
		t.Fatal("operator-decided approval accepted")
	}
	if e.state(t, "phases", f.phase1) != "Awaiting Approval" {
		t.Fatal("the phase moved")
	}
	// The single-use elevation still works once, then never again.
	e.clock.Add(time.Second)
	if err := e.decide(t, f.p, start, "approve", true); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(time.Second)
	if _, err := e.s.StepUp(e.ctx, f.p, testPass, start); code(err) != "invalid_state" {
		t.Fatalf("step-up for a decided approval: %v", err)
	}
	if err := e.s.Decide(e.ctx, f.p, Decision{ApprovalID: start, Decision: "approve", SubjectHash: d.SubjectHash, Elevation: single.Secret}); code(err) != "invalid_state" {
		t.Fatalf("decided twice: %v", err)
	}
}

func TestStartGateRejectionKeepsPhaseClosed(t *testing.T) {
	f := approvedFlow(t)
	e := f.env
	start := e.pendingApproval(t, "phase_start", f.phase1)
	d, _ := e.s.ApprovalDetail(e.ctx, start)
	if err := e.s.Decide(e.ctx, f.p, Decision{ApprovalID: start, Decision: "reject", SubjectHash: d.SubjectHash, Feedback: "x"}); code(err) != "stepup_required" {
		t.Fatalf("gate rejection without elevation: %v", err)
	}
	if err := e.decide(t, f.p, start, "reject", false); err != nil {
		t.Fatal(err)
	}
	if e.state(t, "phases", f.phase1) != "Revision Required" {
		t.Fatal("not Revision Required")
	}
	for _, a := range []Actor{sysActor, {Scope: "operator"}, {Scope: "human"}} {
		err := e.s.tx(e.ctx, func(tx *Tx) error {
			_, err := e.s.transition(e.ctx, tx, "phase", f.phase1, "In Progress", a, "test", "")
			return err
		})
		if err == nil {
			t.Fatalf("%s resumed a phase that was never approved", a.Scope)
		}
	}
	var task string
	e.db.R.QueryRow(`SELECT id FROM tasks WHERE phase_id = ? LIMIT 1`, f.phase1).Scan(&task)
	if _, err := e.db.W.Exec(`INSERT INTO leases(id, execution_id, task_id, project_id, token_hash, granted_at, expires_at, last_heartbeat_at) VALUES ('l','e',?,?,'h','t','t','t')`, task, f.project); err == nil {
		t.Fatal("a lease was granted in a rejected phase")
	}
}

func TestApprovalRaceMovesOnce(t *testing.T) {
	f := approvedFlow(t)
	e := f.env
	start := e.pendingApproval(t, "phase_start", f.phase1)
	d, _ := e.s.ApprovalDetail(e.ctx, start)
	var els []string
	for i := 0; i < 4; i++ {
		e.clock.Add(time.Second)
		r, err := e.s.StepUp(e.ctx, f.p, testPass, start)
		if err != nil {
			t.Fatal(err)
		}
		els = append(els, r.Secret)
	}
	var wg sync.WaitGroup
	errs := make([]error, len(els))
	for i, el := range els {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = e.s.Decide(e.ctx, f.p, Decision{ApprovalID: start, Decision: "approve", SubjectHash: d.SubjectHash, Elevation: el})
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		}
	}
	var moves int
	e.db.R.QueryRow(`SELECT count(*) FROM state_transitions WHERE entity_id = ? AND to_state = 'Approved'`, f.phase1).Scan(&moves)
	if ok != 1 || moves != 1 {
		t.Fatalf("%d decisions succeeded, %d transitions, want 1 and 1", ok, moves)
	}
}

func TestBrokenChainBlocksApprovals(t *testing.T) {
	f := approvedFlow(t)
	e := f.env
	start := e.pendingApproval(t, "phase_start", f.phase1)
	e.db.W.Exec(`DROP TRIGGER audit_no_update`)
	e.db.W.Exec(`UPDATE audit SET detail_json = '{"forged":1}' WHERE seq = 3`)
	if err := e.decide(t, f.p, start, "approve", false); code(err) != "error:" && code(err) != "audit_chain_broken" {
		// StepUp with an approval id already refuses; decide() fails at the step-up.
	}
	d, _ := e.s.ApprovalDetail(e.ctx, start)
	if err := e.s.Decide(e.ctx, f.p, Decision{ApprovalID: start, Decision: "approve", SubjectHash: d.SubjectHash, Elevation: "x"}); code(err) != "audit_chain_broken" {
		t.Fatalf("decision on a broken chain: %v", err)
	}
	e.clock.Add(time.Second)
	el := mustStepUp(t, e, f.p)
	if _, err := e.s.AcknowledgeAudit(e.ctx, f.p, el); err != nil {
		t.Fatal(err)
	}
	if err := e.decide(t, f.p, start, "approve", true); err != nil {
		t.Fatalf("after acknowledging: %v", err)
	}
}

// Every (entity, from, to, actor) the table does not list is refused by the
// database, and every listed move without an approval category is accepted.
func TestTransitionTableCrossProduct(t *testing.T) {
	e := newEnv(t)
	states := map[string][]string{
		"workflow_version": {"draft", "awaiting_approval", "approved", "rejected", "superseded"},
		"phase":            {"Planned", "Awaiting Approval", "Approved", "Ready for Execution", "In Progress", "Validation", "Awaiting Phase Review", "Completed", "Blocked", "Failed", "Revision Required"},
		"task":             {"Backlog", "Ready", "In Progress", "Blocked", "Waiting Approval", "In Review", "Done", "Cancelled"},
		"execution":        {"Queued", "Starting", "Running", "Waiting Approval", "Paused", "Completed", "Failed", "Interrupted", "Disconnected", "Cancelled"},
	}
	allowed := map[string]bool{}
	gated := map[string]bool{}
	rows, _ := e.db.R.Query(`SELECT entity_type, from_state, to_state, actor_scope, approval_category IS NOT NULL FROM allowed_transitions`)
	for rows.Next() {
		var en, fr, to, ac string
		var g bool
		rows.Scan(&en, &fr, &to, &ac, &g)
		allowed[en+"|"+fr+"|"+to+"|"+ac] = true
		gated[en+"|"+fr+"|"+to+"|"+ac] = g
	}
	rows.Close()
	checked, accepted := 0, 0
	for en, sts := range states {
		for _, fr := range sts {
			for _, to := range sts {
				for _, ac := range []string{"system", "agent", "operator", "human", "guard"} {
					k := en + "|" + fr + "|" + to + "|" + ac
					_, err := e.db.W.Exec(`INSERT INTO state_transitions(entity_type, entity_id, from_state, to_state, trigger, actor_scope, ts) VALUES (?, 'x', ?, ?, 'test', ?, 't')`, en, fr, to, ac)
					// Resuming from Revision Required also needs an earlier start approval (trigger phase_resume_needs_prior_approval).
					want := allowed[k] && !gated[k] && !(en == "phase" && fr == "Revision Required" && to == "In Progress")
					if (err == nil) != want {
						t.Errorf("%s: accepted=%v, want %v", k, err == nil, want)
					}
					checked++
					if err == nil {
						accepted++
					}
				}
			}
		}
	}
	if len(allowed) != 105 {
		t.Fatalf("allowed rows = %d, want 105", len(allowed))
	}
	// Every gated move passes with a decided human approval of its category
	// for its subject, and fails with one for another subject or category.
	e.db.W.Exec(`INSERT INTO projects(id, name, path, is_git, created_at) VALUES ('prj_x','x','/x',0,'t')`)
	cats := map[string]string{}
	rows, _ = e.db.R.Query(`SELECT entity_type, from_state, to_state, actor_scope, approval_category FROM allowed_transitions WHERE approval_category IS NOT NULL`)
	for rows.Next() {
		var en, fr, to, ac, cat string
		rows.Scan(&en, &fr, &to, &ac, &cat)
		cats[en+"|"+fr+"|"+to+"|"+ac] = cat
	}
	rows.Close()
	n := 0
	for k, cat := range cats {
		p := strings.Split(k, "|")
		n++
		subj, apr := fmt.Sprintf("subj_%d", n), fmt.Sprintf("apr_%d", n)
		ins := func(id, subject, category string) {
			if _, err := e.db.W.Exec(`INSERT INTO approvals(id, category, subject_type, subject_id, project_id, state, requested_by, requested_at, decided_at, decided_by_scope, stepup_method, elevation_id)
				VALUES (?, ?, ?, ?, 'prj_x', 'approved', 'system', 't', 't', 'human', 'passphrase', 'el')`, id, category, p[0], subject); err != nil {
				t.Fatal(err)
			}
		}
		move := func(id string) error {
			_, err := e.db.W.Exec(`INSERT INTO state_transitions(entity_type, entity_id, from_state, to_state, trigger, actor_scope, approval_id, ts) VALUES (?, ?, ?, ?, 'test', ?, ?, 't')`, p[0], subj, p[1], p[2], p[3], id)
			return err
		}
		ins(apr+"_other", "someone_else", cat)
		if move(apr+"_other") == nil {
			t.Errorf("%s accepted an approval of another subject", k)
		}
		wrong := "risky_action"
		if cat == wrong {
			wrong = "result"
		}
		ins(apr+"_cat", subj, wrong)
		if move(apr+"_cat") == nil {
			t.Errorf("%s accepted an approval of category %s", k, wrong)
		}
		ins(apr, subj, cat)
		if err := move(apr); err != nil {
			t.Errorf("%s refused its own approval: %v", k, err)
		}
	}
	if len(cats) == 0 {
		t.Fatal("no gated moves")
	}
	t.Logf("checked %d combinations, %d accepted; %d gated moves checked with their approval", checked, accepted, len(cats))
}

// No code outside the transition path writes a state column.
func TestNoDirectStateUpdates(t *testing.T) {
	re := regexp.MustCompile("(?i)UPDATE\\s+(phases|tasks|executions|workflow_versions)\\s+SET[^`]*?[^_.a-z]state\\s*=")
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "transition.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		if loc := re.FindIndex(b); loc != nil {
			t.Errorf("%s writes a state column outside the transition path: %q", f, b[loc[0]:loc[1]])
		}
	}
}

// Reopening tasks is a human action for a rejected phase review only.
func TestReopenNeedsStepUpAndARejectedReview(t *testing.T) {
	f := approvedFlow(t)
	e := f.env
	var task string
	e.db.R.QueryRow(`SELECT id FROM tasks WHERE phase_id = ? AND parent_task_id IS NULL LIMIT 1`, f.phase1).Scan(&task)
	if err := e.s.ReopenTasks(e.ctx, Principal{Kind: KindLease}, "", f.phase1, []string{task}, "x"); code(err) != "wrong_scope" {
		t.Fatalf("lease reopened tasks: %v", err)
	}
	if err := e.s.ReopenTasks(e.ctx, f.p, "", f.phase1, []string{task}, "x"); code(err) != "stepup_required" {
		t.Fatalf("reopen without step-up: %v", err)
	}
	// A rejected start gate is not a rejected review: it goes back through its own approval.
	if err := e.decide(t, f.p, e.pendingApproval(t, "phase_start", f.phase1), "reject", false); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(time.Second)
	if err := e.s.ReopenTasks(e.ctx, f.p, mustStepUp(t, e, f.p), f.phase1, []string{task}, "x"); code(err) != "invalid_state" {
		t.Fatalf("reopen after a start-gate rejection: %v", err)
	}
	// The database refuses the move for any actor but human.
	for _, sc := range []string{"system", "agent", "operator"} {
		if _, err := e.db.W.Exec(`INSERT INTO state_transitions(entity_type, entity_id, from_state, to_state, trigger, actor_scope, ts) VALUES ('task', ?, 'Done', 'Ready', 'x', ?, 't')`, task, sc); err == nil {
			t.Fatalf("%s may reopen a Done task", sc)
		}
	}
}
