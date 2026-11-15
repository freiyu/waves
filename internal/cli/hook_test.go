package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"waves/internal/config"
)

func hookEnv(t *testing.T, handler http.HandlerFunc) (*Env, func()) {
	t.Helper()
	dir := t.TempDir()
	d := config.Dirs{Config: filepath.Join(dir, "c"), Data: filepath.Join(dir, "d"), State: filepath.Join(dir, "s")}
	d.Ensure()
	srv := httptest.NewServer(handler)
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	config.Save(d, config.File{Port: port})
	config.WriteSecret(d.GuardToken(), []byte("wgd_test\n")) // guard:allow fake test fixture
	in := filepath.Join(dir, "event.json")
	os.WriteFile(in, []byte(`{"session_id":"s","cwd":"/tmp","tool_name":"Write","tool_use_id":"t","tool_input":{"file_path":"/tmp/x"}}`), 0o600)
	f, _ := os.Open(in)
	t.Cleanup(func() { f.Close() })
	return &Env{Dirs: d, Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, In: f}, srv.Close
}

func exit(err error) int {
	var ec exitCode
	if errors.As(err, &ec) {
		return int(ec)
	}
	if err != nil {
		return 1
	}
	return 0
}

func answer(decision string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer wgd_test" { // guard:allow fake test fixture
			w.WriteHeader(403)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"decision": decision, "reason_code": "test"})
	}
}

func TestHookExitCodes(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		h      http.HandlerFunc
		policy string
		down   bool
		want   int
	}{
		{"allow", answer("allow"), "enforce", false, 0},
		{"deny", answer("deny"), "enforce", false, 2},
		{"service down, enforce", answer("allow"), "enforce", true, 2},
		{"service down, audit", answer("allow"), "audit", true, 0},
		{"bad answer, enforce", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("garbage")) }, "enforce", false, 2},
		{"server error, enforce", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }, "enforce", false, 2},
		{"slow service, enforce", func(w http.ResponseWriter, r *http.Request) { time.Sleep(2 * time.Second) }, "enforce", false, 2},
		{"unknown decision, enforce", answer("maybe"), "enforce", false, 2},
		{"off", answer("deny"), "off", false, 0},
	}
	for _, c := range cases {
		e, stop := hookEnv(t, c.h)
		if c.down {
			stop()
		}
		start := time.Now()
		got := exit(e.hook(ctx, []string{"--policy", c.policy}))
		if got != c.want {
			t.Errorf("%s: exit %d, want %d", c.name, got, c.want)
		}
		if time.Since(start) > time.Second {
			t.Errorf("%s: took %s, the hook must give up after %s", c.name, time.Since(start), hookTimeout)
		}
		stop()
	}
	// Without a guard token the hook denies in enforce.
	e, stop := hookEnv(t, answer("allow"))
	defer stop()
	os.Remove(e.Dirs.GuardToken())
	if got := exit(e.hook(ctx, []string{"--policy", "enforce"})); got != 2 {
		t.Fatalf("missing token: exit %d", got)
	}
}

// The Stop and UserPromptSubmit hooks only report. Whatever happens they exit 0 and print nothing, so they can never
// hold up or block Claude Code.
func TestSignalHookNeverBlocksAndOnlyReports(t *testing.T) {
	var got struct {
		path, auth, body string
	}
	e, stop := hookEnv(t, func(w http.ResponseWriter, r *http.Request) {
		b := new(bytes.Buffer)
		b.ReadFrom(r.Body)
		got.path, got.auth, got.body = r.URL.Path, r.Header.Get("Authorization"), b.String()
		w.WriteHeader(204)
	})
	defer stop()
	if err := e.hook(context.Background(), []string{"--signal", "stop"}); err != nil {
		t.Fatalf("signal hook: %v", err)
	}
	if got.path != "/api/v1/guard/signal" || got.auth != "Bearer wgd_test" || got.body != `{"event":"stop","session_id":"s"}` { // guard:allow fake test fixture
		t.Fatalf("request: %+v", got)
	}
	if e.Out.(*bytes.Buffer).Len()+e.Err.(*bytes.Buffer).Len() != 0 {
		t.Fatal("the signal hook printed something")
	}
	got.path = ""
	e.In.Seek(0, 0) // the event is read once per hook run
	if err := e.hook(context.Background(), []string{"--signal", "end"}); err != nil || got.path != "/api/v1/guard/signal" {
		t.Fatalf("session end: %v %q", err, got.path)
	}
	// An event name Waves does not know sends nothing.
	got.path = ""
	if err := e.hook(context.Background(), []string{"--signal", "approve"}); err != nil || got.path != "" {
		t.Fatalf("unknown event: %v %q", err, got.path)
	}
	// A service that is down, or answers with an error, never turns into a failure.
	e2, stop2 := hookEnv(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	if err := e2.hook(context.Background(), []string{"--signal", "prompt"}); err != nil {
		t.Fatalf("service error: %v", err)
	}
	stop2()
	e3, stop3 := hookEnv(t, func(w http.ResponseWriter, r *http.Request) {})
	stop3()
	if err := e3.hook(context.Background(), []string{"--signal", "stop"}); err != nil {
		t.Fatalf("service down: %v", err)
	}
	// No guard token: nothing to send, still exit 0.
	e4, stop4 := hookEnv(t, func(w http.ResponseWriter, r *http.Request) { t.Error("sent without a token") })
	defer stop4()
	os.Remove(e4.Dirs.GuardToken())
	if err := e4.hook(context.Background(), []string{"--signal", "stop"}); err != nil {
		t.Fatalf("no token: %v", err)
	}
}
