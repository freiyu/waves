package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"waves/internal/config"
	"waves/internal/core"
	"waves/internal/events"
	"waves/internal/store"
)

const testPass = "correct horse battery" // guard:allow fake test fixture

type harness struct {
	t      *testing.T
	core   *core.Service
	db     *store.DB
	srv    *httptest.Server
	port   int
	opTok  string
	cookie string
	csrf   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	dirs := config.Dirs{Config: filepath.Join(dir, "c"), Data: filepath.Join(dir, "d"), State: filepath.Join(dir, "s")}
	dirs.Ensure()
	db, err := store.Open(context.Background(), dirs.DB())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	c := core.New(core.Options{DB: db, Bus: bus, Dirs: dirs, Version: "test", Argon: core.Argon{Time: 1, MemKiB: 64, Threads: 1}})
	if err := c.SetInitialPassphrase(context.Background(), testPass); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, core: c, db: db}
	h.srv = httptest.NewUnstartedServer(nil)
	h.srv.Start()
	h.port = h.srv.Listener.Addr().(*net.TCPAddr).Port
	h.srv.Config.Handler = New(c, h.port, nil, nil).Handler()
	t.Cleanup(func() { h.srv.Close(); bus.Close(); db.Close() })
	h.opTok, err = c.CreateCredential(context.Background(), "operator")
	if err != nil {
		t.Fatal(err)
	}
	return h
}

type req struct {
	method, path, body string
	hdr                map[string]string
	noCSRF             bool
	cookie             bool
	bearer             string
}

func (h *harness) do(r req) (*http.Response, map[string]any) {
	h.t.Helper()
	var body io.Reader
	if r.body != "" {
		body = strings.NewReader(r.body)
	}
	hr, _ := http.NewRequest(r.method, h.srv.URL+r.path, body)
	if r.body != "" {
		hr.Header.Set("Content-Type", "application/json")
	}
	if r.bearer != "" {
		hr.Header.Set("Authorization", "Bearer "+r.bearer)
	}
	if r.cookie && h.cookie != "" {
		hr.AddCookie(&http.Cookie{Name: cookieName, Value: h.cookie})
		if !r.noCSRF {
			hr.Header.Set(headerCSRF, h.csrf)
		}
	}
	for k, v := range r.hdr {
		if k == "Host" {
			hr.Host = v
			continue
		}
		hr.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(hr)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var m map[string]any
	json.Unmarshal(b, &m)
	return res, m
}

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

// signIn goes through the login-link flow like the browser.
func (h *harness) signIn() {
	h.t.Helper()
	res, m := h.do(req{method: "POST", path: "/api/v1/auth/login-links", bearer: h.opTok})
	if res.StatusCode != 201 {
		h.t.Fatalf("login link: %d %v", res.StatusCode, m)
	}
	u := m["url"].(string)
	code := u[strings.LastIndex(u, "/")+1:]
	if !strings.Contains(u, "#/signin/") {
		h.t.Fatalf("login code not in the fragment: %s", u)
	}
	res, m = h.do(req{method: "POST", path: "/api/v1/auth/consume", body: `{"code":"` + code + `"}`})
	if res.StatusCode != 200 {
		h.t.Fatalf("consume: %d %v", res.StatusCode, m)
	}
	for _, c := range res.Cookies() {
		if c.Name == cookieName {
			h.cookie = c.Value
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				h.t.Fatalf("cookie flags: %+v", c)
			}
		}
	}
	h.csrf = m["csrf_token"].(string)
	if h.cookie == "" || h.csrf == "" {
		h.t.Fatal("no session cookie or csrf token")
	}
}

func (h *harness) stepUp(approval string) string {
	h.t.Helper()
	body := `{"passphrase":"` + testPass + `"}`
	if approval != "" {
		body = `{"passphrase":"` + testPass + `","approval_id":"` + approval + `"}`
	}
	res, m := h.do(req{method: "POST", path: "/api/v1/auth/stepup", body: body, cookie: true})
	if res.StatusCode != 200 {
		h.t.Fatalf("step-up: %d %v", res.StatusCode, m)
	}
	return m["elevation_secret"].(string)
}

func TestLoopbackChecks(t *testing.T) {
	h := newHarness(t)
	res, m := h.do(req{method: "GET", path: "/api/v1/health", hdr: map[string]string{"Host": "evil.example:80"}})
	if res.StatusCode != 421 || errCode(m) != "wrong_host" {
		t.Fatalf("foreign host: %d %v", res.StatusCode, m)
	}
	res, _ = h.do(req{method: "GET", path: "/api/v1/health"})
	if res.StatusCode != 200 || res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("health: %d %v", res.StatusCode, res.Header)
	}
	h.signIn()
	res, m = h.do(req{method: "POST", path: "/api/v1/auth/logout", cookie: true, hdr: map[string]string{"Origin": "http://evil.example"}})
	if res.StatusCode != 403 || errCode(m) != "bad_origin" {
		t.Fatalf("cross-origin write: %d %v", res.StatusCode, m)
	}
	res, m = h.do(req{method: "POST", path: "/api/v1/backup", cookie: true, body: "x=1", hdr: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}})
	if res.StatusCode != 415 {
		t.Fatalf("form post: %d %v", res.StatusCode, m)
	}
	res, m = h.do(req{method: "PATCH", path: "/api/v1/settings", cookie: true, body: `{"log_retention_days":` + strings.Repeat("1", 2<<20) + `}`})
	if res.StatusCode != 413 {
		t.Fatalf("oversized body: %d %v", res.StatusCode, m)
	}
}

func TestSessionAndCSRF(t *testing.T) {
	h := newHarness(t)
	if res, m := h.do(req{method: "GET", path: "/api/v1/status"}); res.StatusCode != 401 || errCode(m) != "unauthenticated" {
		t.Fatalf("no credential: %d %v", res.StatusCode, m)
	}
	h.signIn()
	if res, m := h.do(req{method: "GET", path: "/api/v1/status", cookie: true}); res.StatusCode != 200 {
		t.Fatalf("status: %d %v", res.StatusCode, m)
	}
	if res, m := h.do(req{method: "PATCH", path: "/api/v1/settings", cookie: true, noCSRF: true, body: `{"log_retention_days":30}`}); res.StatusCode != 403 || errCode(m) != "csrf_failed" {
		t.Fatalf("write without CSRF: %d %v", res.StatusCode, m)
	}
	if res, m := h.do(req{method: "PATCH", path: "/api/v1/settings", cookie: true, body: `{"log_retention_days":30}`}); res.StatusCode != 200 || m["log_retention_days"].(float64) != 30 {
		t.Fatalf("write with CSRF: %d %v", res.StatusCode, m)
	}
	if res, m := h.do(req{method: "PATCH", path: "/api/v1/settings", cookie: true, body: `{"listen":"0.0.0.0:80"}`}); res.StatusCode != 422 {
		t.Fatalf("unknown setting: %d %v", res.StatusCode, m)
	}
	// A session cannot mint login links: only the operator token can.
	if res, m := h.do(req{method: "POST", path: "/api/v1/auth/login-links", cookie: true}); res.StatusCode != 403 {
		t.Fatalf("session made a login link: %d %v", res.StatusCode, m)
	}
	h.do(req{method: "POST", path: "/api/v1/auth/logout", cookie: true})
	if res, _ := h.do(req{method: "GET", path: "/api/v1/status", cookie: true}); res.StatusCode != 401 {
		t.Fatalf("after logout: %d", res.StatusCode)
	}
}

// Gate-bypass: guard and MCP credentials never act as the operator or the human.
func TestNonOperatorCredentialsAreRefused(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	guard, _ := h.core.CreateCredential(ctx, "guard")
	for _, path := range []string{"/api/v1/status", "/api/v1/settings"} {
		if res, m := h.do(req{method: "GET", path: path, bearer: guard}); res.StatusCode != 403 || errCode(m) != "wrong_scope" {
			t.Fatalf("guard on %s: %d %v", path, res.StatusCode, m)
		}
	}
	for _, r := range []req{
		{method: "POST", path: "/api/v1/auth/stepup", body: `{"passphrase":"` + testPass + `"}`, bearer: guard},
		{method: "POST", path: "/api/v1/audit/acknowledge", bearer: guard},
		{method: "PUT", path: "/api/v1/auth/passphrase", body: `{"current":"a","new":"b"}`, bearer: guard},
	} {
		if res, m := h.do(r); res.StatusCode != 403 {
			t.Fatalf("guard on %s: %d %v", r.path, res.StatusCode, m)
		}
	}
	var n int
	h.db.R.QueryRow(`SELECT count(*) FROM audit WHERE event = 'auth.bypass_attempt'`).Scan(&n)
	if n != 5 {
		t.Fatalf("bypass attempts audited = %d, want 5", n)
	}
	if res, _ := h.do(req{method: "GET", path: "/api/v1/status", bearer: "wop_forged"}); res.StatusCode != 401 {
		t.Fatalf("forged token: %d", res.StatusCode)
	}
}

func TestStepUpOverHTTP(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	res, m := h.do(req{method: "POST", path: "/api/v1/auth/stepup", cookie: true, body: `{"passphrase":"nope nope nope"}`})
	if res.StatusCode != 403 || errCode(m) != "stepup_failed" || m["error"].(map[string]any)["attempts_left"].(float64) != 4 {
		t.Fatalf("wrong passphrase: %d %v", res.StatusCode, m)
	}
	if res, m := h.do(req{method: "PATCH", path: "/api/v1/settings", cookie: true, body: `{"max_concurrent":3}`}); res.StatusCode != 403 || errCode(m) != "stepup_required" {
		t.Fatalf("security setting without elevation: %d %v", res.StatusCode, m)
	}
	time.Sleep(1100 * time.Millisecond) // the 1 s delay after a failure
	el := h.stepUp("")
	if res, m := h.do(req{method: "PATCH", path: "/api/v1/settings", cookie: true, body: `{"max_concurrent":3}`, hdr: map[string]string{headerElev: el}}); res.StatusCode != 200 {
		t.Fatalf("with elevation: %d %v", res.StatusCode, m)
	}
	_, m = h.do(req{method: "GET", path: "/api/v1/auth/session", cookie: true})
	if m["elevation"] == nil {
		t.Fatalf("session does not show the window: %v", m)
	}
	h.do(req{method: "POST", path: "/api/v1/auth/stepup/revoke", cookie: true, hdr: map[string]string{headerElev: el}})
	if res, _ := h.do(req{method: "PATCH", path: "/api/v1/settings", cookie: true, body: `{"max_concurrent":2}`, hdr: map[string]string{headerElev: el}}); res.StatusCode != 403 {
		t.Fatalf("revoked elevation still works: %d", res.StatusCode)
	}
}

func TestEventStream(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hr, _ := http.NewRequestWithContext(ctx, "GET", h.srv.URL+"/api/v1/events", nil)
	hr.AddCookie(&http.Cookie{Name: cookieName, Value: h.cookie})
	res, err := http.DefaultClient.Do(hr)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	sc := bufio.NewScanner(res.Body)
	want := []string{"event: hello", "event: project.changed"}
	go func() {
		time.Sleep(100 * time.Millisecond)
		h.core.Bus().Publish("project.changed", map[string]string{"project_id": "prj_x"})
	}()
	for len(want) > 0 && sc.Scan() {
		if sc.Text() == want[0] {
			want = want[1:]
		}
	}
	if len(want) > 0 {
		t.Fatalf("stream ended before %v", want)
	}
}

// Review M3: every human route stops at the router without an elevation.
func TestHumanRoutesNeedElevation(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	srv := New(h.core, h.port, nil, nil)
	for _, rt := range srv.routes() {
		if rt.scope != "human" || rt.path == "/approvals/{id}/decision" {
			continue
		}
		path := "/api/v1" + strings.NewReplacer("{id}", "prj_x", "{vid}", "v").Replace(rt.path)
		res, m := h.do(req{method: rt.method, path: path, cookie: true, body: "{}"})
		if res.StatusCode != 403 || errCode(m) != "stepup_required" {
			t.Errorf("%s %s without elevation: %d %v", rt.method, rt.path, res.StatusCode, m)
		}
		// Gate-bypass: the refused attempt is audited (AH-1).
		var after int
		h.db.R.QueryRow(`SELECT count(*) FROM audit WHERE event = 'stepup.refused' AND subject = ?`, rt.method+" "+rt.path).Scan(&after)
		if after != 1 {
			t.Errorf("%s %s: refusal not audited", rt.method, rt.path)
		}
	}
}

// Every route of the design contract has a handler (review: endpoints match the contract).
func TestDesignRoutesAreServed(t *testing.T) {
	h := newHarness(t)
	srv := New(h.core, h.port, nil, nil)
	have := map[string]bool{}
	for _, rt := range srv.routes() {
		have[rt.method+" "+rt.path] = true
	}
	design := []string{
		"GET /health", "GET /status", "GET /doctor", "POST /auth/login-links", "GET /auth/session", "POST /auth/logout", "POST /auth/stepup",
		"PUT /auth/passphrase", "GET /audit", "POST /audit/acknowledge", "POST /backup", "POST /gc",
		"GET /projects", "POST /projects", "GET /projects/{id}", "DELETE /projects/{id}", "POST /projects/{id}/activate",
		"GET /projects/{id}/settings", "PATCH /projects/{id}/settings", "GET /settings", "PATCH /settings",
		"GET /projects/{id}/requirements", "POST /projects/{id}/requirements", "POST /projects/{id}/planning-requests",
		"GET /planning-requests/{id}", "GET /projects/{id}/proposals", "GET /proposals/{id}", "POST /proposals/{id}/create-workflow",
		"GET /projects/{id}/workflow", "GET /workflow-versions/{id}", "PATCH /workflow-versions/{id}", "POST /workflow-versions/{id}/versions",
		"POST /workflow-versions/{id}/submit", "GET /phases/{id}", "GET /phases/{id}/review", "POST /phases/{id}/validation-runs/{vid}/confirm",
		"GET /tasks", "GET /tasks/{id}", "GET /executions", "GET /executions/{id}", "GET /executions/{id}/log", "GET /executions/{id}/diff",
		"GET /executions/{id}/context", "POST /executions/{id}/cancel", "POST /executions/{id}/resolve",
		"GET /approvals", "GET /approvals/{id}", "POST /approvals/{id}/decision", "GET /harnesses",
		"POST /projects/{id}/agent/connect", "POST /projects/{id}/agent/disconnect", "GET /projects/{id}/guard", "GET /projects/{id}/guard/decisions",
		"POST /guard/decide", "GET /events",
	}
	for _, d := range design {
		if !have[d] {
			t.Errorf("design route %s has no handler", d)
		}
	}
	for _, task := range []string{"cancel", "retry", "resume", "run"} {
		if !have["POST /tasks/{id}/{action}"] {
			t.Errorf("POST /tasks/{id}/%s has no handler", task)
		}
	}
	if m := srv.MissingRoutes(); len(m) > 0 {
		t.Fatalf("contract routes without a handler: %v", m)
	}
}
