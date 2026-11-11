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
