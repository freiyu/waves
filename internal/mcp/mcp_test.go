package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"waves/internal/config"
	"waves/internal/core"
	"waves/internal/events"
	"waves/internal/store"
)

const testPass = "correct horse battery" // guard:allow fake test fixture

type fixture struct {
	core  *core.Service
	db    *store.DB
	srv   *httptest.Server
	token string
	lease string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	dirs := config.Dirs{Config: filepath.Join(dir, "c"), Data: filepath.Join(dir, "d"), State: filepath.Join(dir, "s")}
	dirs.Ensure()
	db, err := store.Open(ctx, dirs.DB())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	c := core.New(core.Options{DB: db, Bus: bus, Dirs: dirs, Version: "test", Argon: core.Argon{Time: 1, MemKiB: 64, Threads: 1}})
	c.SetInitialPassphrase(ctx, testPass)
	tok, _ := c.CreateCredential(ctx, "operator")
	op, _ := c.Bearer(ctx, tok, "t")
	root := filepath.Join(dir, "proj")
	os.MkdirAll(root, 0o700)
	pid, err := c.RegisterProject(ctx, op, root, "demo")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := c.ConnectPlan(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	el, err := c.StepUp(ctx, op, testPass, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Connect(ctx, op, el.Secret, pid, plan.PlanHash); err != nil {
		t.Fatal(err)
	}
	mt, err := config.ReadSecret(filepath.Join(dirs.Config, "mcp", pid+".token"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{core: c, db: db, token: mt}
	mux := http.NewServeMux()
	mux.Handle("/mcp", Handler(c))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(func() { f.srv.Close(); bus.Close(); db.Close() })
	return f
}

func (f *fixture) client(t *testing.T, token string) (*sdk.ClientSession, error) {
	c := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil)
	return c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: f.srv.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{token, http.DefaultTransport}}}, nil)
}

func text(r *sdk.CallToolResult) string {
	if r == nil || len(r.Content) == 0 {
		return ""
	}
	return r.Content[0].(*sdk.TextContent).Text
}

func TestToolsAndNoApprovalTool(t *testing.T) {
	f := setup(t)
	cs, err := f.client(t, f.token)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	var names []string
	for tool, err := range cs.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	want := slices.Clone(Tools)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}
	for _, n := range names {
		if strings.Contains(n, "approve") || strings.Contains(n, "reject") || strings.Contains(n, "setting") {
			t.Fatalf("tool %s must not exist", n)
		}
	}
	r, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "waves_list_available_tasks", Arguments: map[string]any{}})
	if err != nil || r.IsError || !strings.Contains(text(r), `"tasks":[]`) {
		t.Fatalf("list: %v %s", err, text(r))
	}
	r, _ = cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "waves_claim_task", Arguments: map[string]any{"task_id": "tsk_nope"}})
	if !r.IsError || !strings.Contains(text(r), "task_not_ready") {
		t.Fatalf("claim of a missing task: %s", text(r))
	}
	r, _ = cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "waves_heartbeat", Arguments: map[string]any{"lease_token": "wls_forged"}}) // guard:allow fake test fixture
	if !r.IsError || !strings.Contains(text(r), "lease_invalid") {
		t.Fatalf("forged lease: %s", text(r))
	}
}

// Gate-bypass: only the project MCP credential reaches /mcp.
func TestMCPRefusesOtherCredentials(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	op, _ := f.core.CreateCredential(ctx, "operator")
	guard, _ := f.core.CreateCredential(ctx, "guard")
	for name, tok := range map[string]string{"none": "", "operator": op, "guard": guard, "forged": "wmp_forged"} {
		req, _ := http.NewRequest("POST", f.srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Fatalf("%s credential: %d", name, res.StatusCode)
		}
	}
	var n int
	f.db.R.QueryRow(`SELECT count(*) FROM audit WHERE event = 'auth.bypass_attempt'`).Scan(&n)
	if n != 1 {
		t.Fatalf("guard token on /mcp audited %d times, want 1", n)
	}
}

func TestBridgeForwardsCalls(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ct, st := sdk.NewInMemoryTransports()
	errc := make(chan error, 1)
	go func() { errc <- Bridge(ctx, f.srv.URL+"/mcp", f.token, "test", st) }()
	c := sdk.NewClient(&sdk.Implementation{Name: "claude-code-stand-in"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := &sdk.CallToolParams{Name: "waves_get_planning_request", Arguments: map[string]any{}}
	p.SetMeta(map[string]any{"claudecode/toolUseId": "toolu_1"})
	r, err := cs.CallTool(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]string
	json.Unmarshal([]byte(text(r)), &body)
	if !r.IsError || body["error"] != "not_found" {
		t.Fatalf("no open request through the bridge: %s", text(r))
	}
	cs.Close()
	cancel()
	<-errc
}

// An internal failure tells the agent a request id; the cause goes to the log only.
func TestInternalErrorCarriesARequestID(t *testing.T) {
	r, _, _ := result(nil, errors.New("create validator: constraint failed"))
	if !r.IsError || !strings.Contains(text(r), "(request req_") || strings.Contains(text(r), "constraint") {
		t.Fatalf("internal error result: %s", text(r))
	}
}
