package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
	dirs  config.Dirs
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
	f := &fixture{core: c, db: db, token: mt, dirs: dirs}
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
	for name, tok := range map[string]string{"none": "", "operator": op, "guard": guard, "forged": "wmp_forged"} { // guard:allow fake test fixture
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

// The stream the bridge keeps open is how Waves knows a session is connected: it counts while the stream lives and stops
// counting when the client goes away, and only the project's own credential can start it.
func TestOpenStreamMeansConnected(t *testing.T) {
	f := setup(t)
	var pid string
	f.db.R.QueryRow(`SELECT id FROM projects`).Scan(&pid)
	cs, err := f.client(t, f.token)
	if err != nil {
		t.Fatal(err)
	}
	until := func(want bool, what string) {
		t.Helper()
		for i := 0; i < 60; i++ {
			if f.core.SessionConnected(pid, cs.ID()) == want {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%s: connected is not %v", what, want)
	}
	until(true, "after the client connected")
	cs.Close()
	until(false, "after the client closed")

	// A closed or invented session id with the right credential is refused by the SDK and must not count as a connection.
	for _, sid := range []string{cs.ID(), "invented-session-id"} {
		req, _ := http.NewRequest("GET", f.srv.URL+"/mcp", nil)
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Mcp-Session-Id", sid)
		req.Header.Set("Authorization", "Bearer "+f.token)
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		if res, err := http.DefaultClient.Do(req.WithContext(ctx)); err == nil {
			if res.StatusCode != http.StatusNotFound {
				t.Fatalf("a stream for session %q answered %d, want 404", sid, res.StatusCode)
			}
			res.Body.Close()
		}
		cancel()
		if f.core.SessionConnected(pid, sid) {
			t.Fatalf("a refused stream for %q counts as connected", sid)
		}
	}

	// The same session id with another credential, or none, starts no stream.
	cs2, err := f.client(t, f.token)
	if err != nil {
		t.Fatal(err)
	}
	defer cs2.Close()
	for _, auth := range []string{"", "Bearer wrong-token"} {
		req, _ := http.NewRequest("GET", f.srv.URL+"/mcp", nil)
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Mcp-Session-Id", cs2.ID())
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		res, err := http.DefaultClient.Do(req.WithContext(ctx))
		if err == nil {
			if res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("a stream with %q answered %d", auth, res.StatusCode)
			}
			res.Body.Close()
		}
		cancel()
	}
}

// holdFirstWrite sends the first bytes of a response to the client and then keeps the handler from finishing until release
// is closed: the moment in which a client already has the session id and the server is still busy with that request.
type holdFirstWrite struct {
	http.ResponseWriter
	once    sync.Once
	release chan struct{}
}

func (h *holdFirstWrite) Write(b []byte) (int, error) {
	n, err := h.ResponseWriter.Write(b)
	h.once.Do(func() { h.Flush(); <-h.release })
	return n, err
}

func (h *holdFirstWrite) Flush() { _ = http.NewResponseController(h.ResponseWriter).Flush() }

// The bridge opens its stream as soon as it has the session id. That can be before the server finished the initialize
// request; the stream must count all the same, and only for the credential that opened the session.
func TestStreamOpenedBeforeInitializeReturnedCounts(t *testing.T) {
	f := setup(t)
	var pid string
	f.db.R.QueryRow(`SELECT id FROM projects`).Scan(&pid)
	otherPID, otherToken := f.secondProject(t)
	inner := Handler(f.core)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Header.Get("Mcp-Session-Id") == "" {
			w = &holdFirstWrite{ResponseWriter: w, release: release}
		}
		inner.ServeHTTP(w, r)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(func() { close(release); cancel(); srv.CloseClientConnections(); srv.Close() })
	do := func(ctx context.Context, method, token, sid, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, method, srv.URL+"/mcp", strings.NewReader(body))
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		if sid != "" {
			req.Header.Set("Mcp-Session-Id", sid)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		return res
	}
	init := do(ctx, "POST", f.token, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	sid := init.Header.Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatalf("no session id: %d", init.StatusCode)
	}

	// Another project's credential cannot take the session in this moment.
	octx, ocancel := context.WithCancel(ctx)
	other := do(octx, "GET", otherToken, sid, "")
	if f.core.SessionConnected(pid, sid) || f.core.SessionConnected(otherPID, sid) {
		t.Fatal("a stream of another project's credential counts")
	}
	ocancel()
	other.Body.Close()

	// The SDK allows one stream per session: wait until the refused one is gone.
	var own *http.Response
	for i := 0; i < 40; i++ {
		if own = do(ctx, "GET", f.token, sid, ""); own.StatusCode == http.StatusOK {
			break
		}
		own.Body.Close()
		time.Sleep(50 * time.Millisecond)
	}
	if own.StatusCode != http.StatusOK {
		t.Fatalf("own stream answered %d", own.StatusCode)
	}
	if !f.core.SessionConnected(pid, sid) {
		t.Fatal("a stream opened while initialize was still being answered does not count")
	}
}

// secondProject registers and connects another project and returns its id and MCP credential.
func (f *fixture) secondProject(t *testing.T) (string, string) {
	t.Helper()
	ctx := context.Background()
	tok, _ := f.core.CreateCredential(ctx, "operator")
	op, _ := f.core.Bearer(ctx, tok, "t")
	root := filepath.Join(t.TempDir(), "other")
	os.MkdirAll(root, 0o700)
	pid, err := f.core.RegisterProject(ctx, op, root, "other")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.core.ConnectPlan(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	el, err := f.core.StepUp(ctx, op, testPass, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.core.Connect(ctx, op, el.Secret, pid, plan.PlanHash); err != nil {
		t.Fatal(err)
	}
	mt, err := config.ReadSecret(filepath.Join(f.dirs.Config, "mcp", pid+".token"))
	if err != nil {
		t.Fatal(err)
	}
	return pid, mt
}

// raw sends one request to /mcp, naming a session, with the given project credential.
func (f *fixture) raw(t *testing.T, method, token, sid, body string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, method, f.srv.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Mcp-Session-Id", sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	res.Body.Close()
	return res.StatusCode
}

// A session answers only to the credential that opened it. Another project that learned the id can neither send to it, take
// its stream, nor close it, and the attempt is recorded.
func TestAnotherCredentialCannotTouchASession(t *testing.T) {
	f := setup(t)
	var pid string
	f.db.R.QueryRow(`SELECT id FROM projects`).Scan(&pid)
	_, otherToken := f.secondProject(t)
	cs, err := f.client(t, f.token)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	for _, m := range []struct{ method, body string }{
		{"POST", `{"jsonrpc":"2.0","id":9,"method":"tools/list"}`},
		{"GET", ""},
		{"DELETE", ""},
	} {
		if st := f.raw(t, m.method, otherToken, cs.ID(), m.body); st != http.StatusNotFound {
			t.Fatalf("%s with another project's credential answered %d, want 404", m.method, st)
		}
	}
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("the owner lost its session: %v", err)
	}
	if !f.core.SessionConnected(pid, cs.ID()) {
		t.Fatal("the owner is no longer connected")
	}
	var n int
	f.db.R.QueryRow(`SELECT count(*) FROM audit WHERE event = 'auth.bypass_attempt' AND subject = 'mcp session of another credential'`).Scan(&n)
	if n != 3 {
		t.Fatalf("%d refusals recorded, want 3", n)
	}
}

// Many new sessions must not make Waves forget who holds a live one: after the limit only closed sessions are dropped.
func TestManySessionsDoNotForgetLiveBindings(t *testing.T) {
	old := sessionLimit
	sessionLimit = 4
	t.Cleanup(func() { sessionLimit = old })
	f := setup(t)
	_, otherToken := f.secondProject(t)
	cs, err := f.client(t, f.token)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	for i := 0; i < 20; i++ {
		other, err := f.client(t, otherToken)
		if err != nil {
			t.Fatal(err)
		}
		other.Close()
	}
	if st := f.raw(t, "DELETE", otherToken, cs.ID(), ""); st != http.StatusNotFound {
		t.Fatalf("after many sessions another credential's DELETE answered %d, want 404", st)
	}
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("the owner lost its session: %v", err)
	}
}

// Closed sessions are forgotten once the limit is reached, so the bindings do not grow with every session ever opened.
func TestClosedSessionsAreDroppedAtTheLimit(t *testing.T) {
	old := sessionLimit
	sessionLimit = 4
	t.Cleanup(func() { sessionLimit = old })
	s := &server{srv: sdk.NewServer(&sdk.Implementation{Name: "t"}, nil), sessions: map[string]string{}}
	for i := 0; i < 50; i++ {
		s.bind(fmt.Sprintf("closed-%d", i), "cred")
	}
	if n := len(s.sessions); n > 5 {
		t.Fatalf("%d bindings kept for sessions that do not exist", n)
	}
}
