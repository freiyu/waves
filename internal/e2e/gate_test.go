//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"waves/internal/api/contract"
)

// attempt is one try to get past a phase gate.
type attempt struct {
	name  string
	route string // api, cli, mcp, scheduler, database
	try   func() (refused bool, detail string)
}

// expectedEvent is the audit event an attempt must leave, where one kind is
// required: a lease, guard or MCP credential is flagged as a bypass attempt
// (AH-1, AH-4); a missing or misused step-up as stepup.refused.
var expectedEvent = map[string]string{}

func init() {
	for _, p := range [][2]string{
		{"operator token, no elevation", "stepup.refused"},
		{"dashboard session, no elevation", "stepup.refused"},
		{"lease token", "auth.bypass_attempt"},
		{"guard token", "auth.bypass_attempt"},
		{"MCP project token", "auth.bypass_attempt"},
		{"lease token with a real elevation", "auth.bypass_attempt"},
		{"forged elevation", "stepup.refused"},
		{"window elevation on a gate", "stepup.refused"},
		{"expired single-use elevation", "stepup.refused"},
		{"revoked elevation", "stepup.refused"},
		{"elevation of another session", "stepup.refused"},
		{"CLI elevation used by the dashboard", "stepup.refused"},
		{"elevation for another approval", "stepup.refused"},
		{"step-up with the lease token", "auth.bypass_attempt"},
		{"step-up with the MCP token", "auth.bypass_attempt"},
		{"wrong passphrase", "stepup.failed"},
		{"replayed login link", "login_link.replayed"},
		{"MCP over HTTP without the bridge: approval tool", "auth.bypass_attempt"},
		{"MCP over HTTP with the lease token", "auth.bypass_attempt"},
	} {
		expectedEvent[p[0]] = p[1]
	}
}

// localRefusals names why some attempts cannot leave an audit record: the
// refusal happens before anything reaches the service. Such an attempt must
// also leave no trace of progress (no step-up, no decision).
var localRefusals = map[string]string{
	"CLI review with the passphrase on a pipe":          cliLocal,
	"CLI review with the passphrase in the environment": cliLocal,
	"CLI review with the passphrase as an argument":     cliLocal,
	"MCP tool list has no approval tool":                "refused inside the stdio bridge, which offers only the service's tools; the direct HTTP call is audited (next row)",
	"database: forged approved approval row":            dbLocal,
	"database: forged gate transition":                  dbLocal,
	"database: direct state update":                     dbLocal,
}

const (
	cliLocal = "refused inside the CLI process before any request. The TTY check is a speed bump (AH-3): over the API an operator token with the right passphrase can step up without a terminal, and that is recorded with tty=false. The barrier is the passphrase (AH-2)"
	dbLocal  = "refused by a database trigger; an aborted statement cannot write; direct edits are the not-defended boundary, chain edits are caught (last row)"
)

// gateTarget is a project whose workflow is approved and whose first phase
// waits at its start gate.
func (i *inst) gateTarget(name string) (*project, string, string) {
	i.t.Helper()
	p := i.newProject(name)
	p.dash.must("POST", "/projects/"+p.id+"/requirements", map[string]string{"kind": "text", "text": "Add hello.txt"}, nil)
	p.dash.must("POST", "/projects/"+p.id+"/planning-requests", nil, nil)
	a := p.agent(i)
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
	phase, _ := i.phase(p.id, "p1")
	return p, phase, p.pending("phase_start")
}

// refusalEvents are the audit events a refused bypass attempt may leave.
var refusalEvents = []string{"auth.bypass_attempt", "auth.wrong_scope", "stepup.failed", "stepup.refused", "approval.refused", "login_link.replayed",
	"login_link.expired", "audit.chain_broken", "guard.denied", "lease.refused", "claim.refused"}

// The gate-bypass suite: every route, every wrong credential. Each attempt
// must fail, and must leave an audit record, and the phase must not move.
func TestGateBypass(t *testing.T) {
	i := newInst(t)
	p, phase, apr := i.gateTarget("gate")
	// A second project gives a real lease token and a guard-visible session.
	work, wa := i.planned("work")
	_, lease := wa.claim()
	mcpTok := i.secret("mcp/" + p.id + ".token")
	dash := p.dash
	var d contract.ApprovalDetail
	dash.must("GET", "/approvals/"+apr, nil, &d)
	approve := map[string]string{"decision": "approve", "subject_hash": d.SubjectHash}
	decide := func(c *client, el string) (bool, string) {
		h := map[string]string{}
		if el != "" {
			h = elev(el)
		}
		st, code := c.call("POST", "/approvals/"+apr+"/decision", approve, nil, h)
		return st >= 400, fmt.Sprintf("%d %s", st, code)
	}
	db := i.db()

	attempts := []attempt{
		{"operator token, no elevation", "api", func() (bool, string) { return decide(i.operator(), "") }},
		{"dashboard session, no elevation", "api", func() (bool, string) { return decide(dash, "") }},
		{"lease token", "api", func() (bool, string) { return decide(i.as(lease), "") }},
		{"guard token", "api", func() (bool, string) { return decide(i.as(i.guard), "") }},
		{"MCP project token", "api", func() (bool, string) { return decide(i.as(mcpTok), "") }},
		{"lease token with a real elevation", "api", func() (bool, string) { return decide(i.as(lease), dash.stepUp(apr)) }},
		{"forged elevation", "api", func() (bool, string) { return decide(dash, "wel_forgedforgedforgedforgedforged") }},
		{"window elevation on a gate", "api", func() (bool, string) { return decide(dash, dash.stepUp("")) }},
		{"expired single-use elevation", "api", func() (bool, string) {
			el := dash.stepUp(apr)
			if _, err := db.Exec(`UPDATE elevations SET expires_at = '2000-01-01T00:00:00Z' WHERE rowid = (SELECT max(rowid) FROM elevations)`); err != nil {
				return false, "could not age the elevation: " + err.Error()
			}
			return decide(dash, el)
		}},
		{"revoked elevation", "api", func() (bool, string) {
			el := dash.stepUp(apr)
			dash.must("POST", "/auth/stepup/revoke", nil, nil, elev(el))
			return decide(dash, el)
		}},
		{"elevation of another session", "api", func() (bool, string) {
			other := i.dashboard()
			return decide(dash, other.stepUp(apr))
		}},
		{"CLI elevation used by the dashboard", "api", func() (bool, string) {
			op := i.operator()
			op.tty = true
			return decide(dash, op.stepUp(apr))
		}},
		{"elevation for another approval", "api", func() (bool, string) {
			var r struct {
				ApprovalID string `json:"approval_id"`
			}
			wa.must("waves_request_approval", map[string]any{"action": "rm -rf build", "reason": "clean", "impact": "deletes build output"}, &r)
			return decide(dash, work.dash.stepUp(r.ApprovalID))
		}},
		{"step-up with the lease token", "api", func() (bool, string) {
			st, code := i.as(lease).call("POST", "/auth/stepup", map[string]string{"passphrase": testPass, "approval_id": apr}, nil, nil)
			return st >= 400, fmt.Sprintf("%d %s", st, code)
		}},
		{"step-up with the MCP token", "api", func() (bool, string) {
			st, code := i.as(mcpTok).call("POST", "/auth/stepup", map[string]string{"passphrase": testPass, "approval_id": apr}, nil, nil)
			return st >= 400, fmt.Sprintf("%d %s", st, code)
		}},
		{"wrong passphrase", "api", func() (bool, string) {
			st, code := dash.call("POST", "/auth/stepup", map[string]string{"passphrase": "not the passphrase at all", "approval_id": apr}, nil, nil)
			return st >= 400, fmt.Sprintf("%d %s", st, code)
		}},
		{"replayed login link", "api", func() (bool, string) {
			var l struct {
				URL string `json:"url"`
			}
			i.operator().must("POST", "/auth/login-links", nil, &l)
			code := l.URL[strings.Index(l.URL, "/signin/")+8:]
			i.raw("POST", "/api/v1/auth/consume", map[string]string{"code": code}, nil)
			res, _ := i.raw("POST", "/api/v1/auth/consume", map[string]string{"code": code}, nil)
			return res.StatusCode >= 400, fmt.Sprint(res.StatusCode)
		}},
		{"login link from a session (not the operator token)", "api", func() (bool, string) {
			st, code := dash.call("POST", "/auth/login-links", nil, nil, nil)
			return st >= 400, fmt.Sprintf("%d %s", st, code)
		}},
		{"CLI review with the passphrase on a pipe", "cli", func() (bool, string) {
			out, code := i.run("approve\np1\n"+testPass+"\n", "approval", "review", apr)
			return code != 0, fmt.Sprintf("exit %d: %s", code, firstLine(out))
		}},
		{"CLI review with the passphrase in the environment", "cli", func() (bool, string) {
			saved := i.env
			i.env = append(slices.Clone(saved), "WAVES_PASSPHRASE="+testPass, "WAVES_APPROVAL_PASSPHRASE="+testPass)
			defer func() { i.env = saved }()
			// On a real terminal the CLI ignores the variables and still asks; an empty answer ends it.
			out, err := i.tty([]string{"approval", "review", apr}, "approve\np1\n\n")
			return err != nil && strings.Contains(out, "Approval passphrase:"), fmt.Sprintf("%v: still prompted=%v", err, strings.Contains(out, "Approval passphrase:"))
		}},
		{"CLI review with the passphrase as an argument", "cli", func() (bool, string) {
			out, err := i.tty([]string{"approval", "review", apr, testPass}, "")
			return err != nil && strings.Contains(out, "Usage: waves"), fmt.Sprintf("%v: %s", err, firstLine(out))
		}},
		{"MCP tool list has no approval tool", "mcp", func() (bool, string) {
			names := []string{}
			for tool, err := range wa.cs.Tools(t.Context(), nil) {
				if err != nil {
					return false, err.Error()
				}
				names = append(names, tool.Name)
			}
			for _, n := range names {
				if strings.Contains(n, "approve") || strings.Contains(n, "decide") || strings.Contains(n, "decision") {
					return false, "tool " + n
				}
			}
			text, isErr := wa.call("waves_approve", map[string]any{"approval_id": apr})
			return isErr, firstLine(text)
		}},
		{"MCP over HTTP without the bridge: approval tool", "mcp", func() (bool, string) {
			c := sdk.NewClient(&sdk.Implementation{Name: "direct"}, nil)
			cs, err := c.Connect(t.Context(), &sdk.StreamableClientTransport{Endpoint: i.url("/mcp"), HTTPClient: &http.Client{Transport: bearerRT(mcpTok)}}, nil)
			if err != nil {
				return false, err.Error()
			}
			defer cs.Close()
			_, err = cs.CallTool(t.Context(), &sdk.CallToolParams{Name: "waves_decide_approval", Arguments: map[string]any{"approval_id": apr, "decision": "approve"}})
			return err != nil, errText(err)
		}},
		{"MCP over HTTP with the lease token", "mcp", func() (bool, string) {
			c := sdk.NewClient(&sdk.Implementation{Name: "direct"}, nil)
			_, err := c.Connect(t.Context(), &sdk.StreamableClientTransport{Endpoint: i.url("/mcp"), HTTPClient: &http.Client{Transport: bearerRT(lease)}}, nil)
			return err != nil, errText(err)
		}},
		{"MCP claim of a task in a phase waiting at its gate", "scheduler", func() (bool, string) {
			task := i.str(`SELECT id FROM tasks WHERE phase_id = ? LIMIT 1`, phase)
			ga := p.agent(i)
			text, isErr := ga.call("waves_claim_task", map[string]any{"task_id": task})
			return isErr, firstLine(text)
		}},
		{"database: forged approved approval row", "database", func() (bool, string) {
			_, err := db.Exec(`UPDATE approvals SET state = 'approved', decided_at = '2026-01-01T00:00:00Z', decided_by_scope = 'human', stepup_method = 'passphrase' WHERE id = ?`, apr)
			return err != nil && strings.Contains(err.Error(), "state <> 'approved'"), errText(err)
		}},
		{"database: forged gate transition", "database", func() (bool, string) {
			_, err := db.Exec(`INSERT INTO state_transitions(entity_type, entity_id, from_state, to_state, trigger, actor_scope, ts) VALUES ('phase', ?, 'Awaiting Approval', 'Approved', 'x', 'human', 't')`, phase)
			return err != nil && strings.Contains(err.Error(), "needs a decided human approval"), errText(err)
		}},
		{"database: direct state update", "database", func() (bool, string) {
			_, err := db.Exec(`UPDATE phases SET state = 'Ready for Execution' WHERE id = ?`, phase)
			return err != nil && strings.Contains(err.Error(), "needs a transition record"), errText(err)
		}},
	}

	type row struct {
		name, route, detail, local string
		refused, audited           bool
	}
	var rows []row
	for _, a := range attempts {
		before := i.count(`SELECT COALESCE(max(seq), 0) FROM audit`)
		decided := i.count(`SELECT count(*) FROM approvals WHERE state <> 'pending'`)
		refused, detail := a.try()
		var evs []string
		rs, err := db.Query(`SELECT event FROM audit WHERE seq > ?`, before)
		if err != nil {
			t.Fatal(err)
		}
		for rs.Next() {
			var e string
			if err := rs.Scan(&e); err != nil {
				t.Fatal(err)
			}
			evs = append(evs, e)
		}
		rs.Close()
		audited := false
		for _, e := range evs {
			if want := expectedEvent[a.name]; want != "" {
				audited = audited || e == want
			} else {
				audited = audited || slices.Contains(refusalEvents, e)
			}
		}
		if !audited {
			detail += fmt.Sprintf(" (audit events: %v, want %q)", evs, expectedEvent[a.name])
		}
		if localRefusals[a.name] != "" {
			// Nothing may have reached the service: no step-up, no decision.
			for _, e := range evs {
				if strings.HasPrefix(e, "stepup.") || strings.HasPrefix(e, "approval.") {
					refused = false
					detail += " (but the service recorded " + e + ")"
				}
			}
			if i.count(`SELECT count(*) FROM approvals WHERE state <> 'pending'`) != decided {
				refused = false
			}
		}
		rows = append(rows, row{a.name, a.route, detail, localRefusals[a.name], refused, audited})
	}
	// The broken-chain case runs last: it blocks every approval afterwards.
	if _, err := db.Exec(`DROP TRIGGER audit_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE audit SET detail_json = '{"forged":1}' WHERE seq = 5`); err != nil {
		t.Fatal(err)
	}
	var el contract.StepUpResponse
	dash.call("POST", "/auth/stepup", map[string]string{"passphrase": testPass, "approval_id": apr}, &el, nil)
	_, detail := decide(dash, el.ElevationSecret)
	code := detail
	var s contract.StatusResponse
	dash.must("GET", "/status", nil, &s)
	rows = append(rows, row{"database: audit chain rewritten", "database", detail, "", strings.Contains(code, "audit_chain_broken") && s.Audit.State == "broken", i.count(`SELECT count(*) FROM audit WHERE event = 'stepup.refused' AND detail_json LIKE '%audit_chain_broken%'`) > 0})

	failed := 0
	for _, r := range rows {
		mark, aud := "PASS", fmt.Sprint(r.audited)
		if r.local != "" {
			aud = "n/a"
		}
		if !r.refused || (!r.audited && r.local == "") {
			mark = "FAIL"
			failed++
		}
		t.Logf("%-4s %-9s %-52s refused=%-5v audited=%-5s %s", mark, r.route, r.name, r.refused, aud, r.detail)
		if r.local != "" {
			t.Logf("     %-9s %-52s audit n/a: %s", "", "", r.local)
		}
	}
	if got := i.str(`SELECT state FROM phases WHERE id = ?`, phase); got != "Awaiting Approval" {
		t.Errorf("the phase moved to %q", got)
	}
	if n := i.count(`SELECT count(*) FROM approvals WHERE state = 'approved' AND (decided_by_scope <> 'human' OR elevation_id IS NULL)`); n != 0 {
		t.Errorf("%d approvals were decided without a human elevation", n)
	}
	t.Logf("gate-bypass: %d attempts, %d failed", len(rows), failed)
	if failed > 0 {
		t.Errorf("%d gate-bypass attempts were not both refused and audited", failed)
	}
}

type bearerRT string

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 90 {
		s = s[:90]
	}
	return s
}

func errText(err error) string {
	if err == nil {
		return "accepted"
	}
	return firstLine(err.Error())
}
