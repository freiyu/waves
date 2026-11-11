package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The guard is only as good as the hook file. Anything that removes, disables
// or edits it must show as drift, and the guard must stop reporting "installed".
func TestHookDriftShowsEveryWayToSwitchTheGuardOff(t *testing.T) {
	a := startedFlow(t)
	settings := filepath.Join(a.root, ".claude", "settings.local.json")
	good, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	info := func() (bool, string) {
		g, err := a.s.GuardInfo(a.ctx, a.project)
		if err != nil {
			t.Fatal(err)
		}
		return g.Installed, g.Drift
	}
	if inst, drift := info(); !inst || drift != "" {
		t.Fatalf("fresh connection: installed=%v drift=%q", inst, drift)
	}
	cases := []struct {
		name  string
		write func() error
		want  string
	}{
		{"file removed", func() error { return os.Remove(settings) }, "is missing"},
		{"hook entry removed", func() error { return os.WriteFile(settings, []byte(`{"hooks":{"PreToolUse":[]}}`), 0o600) }, "entry is missing"},
		{"hooks disabled", func() error {
			return os.WriteFile(settings, []byte(strings.Replace(string(good), "{", `{"disableAllHooks": true,`, 1)), 0o600)
		}, "disableAllHooks"},
		{"command swapped", func() error {
			return os.WriteFile(settings, []byte(strings.Replace(string(good), " hook --policy audit", " hook --policy off", 1)), 0o600)
		}, "was changed"},
		{"timeout changed", func() error {
			return os.WriteFile(settings, []byte(strings.Replace(string(good), `"timeout": 5`, `"timeout": 500`, 1)), 0o600)
		}, "was changed"},
		{"not json", func() error { return os.WriteFile(settings, []byte("{"), 0o600) }, "cannot be read"},
	}
	for _, c := range cases {
		if err := os.WriteFile(settings, good, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := c.write(); err != nil {
			t.Fatal(err)
		}
		inst, drift := info()
		if inst || !strings.Contains(drift, c.want) {
			t.Errorf("%s: installed=%v drift=%q, want drift containing %q", c.name, inst, drift, c.want)
		}
	}

	if err := os.WriteFile(settings, good, 0o600); err != nil {
		t.Fatal(err)
	}
	// Under enforce the wrapper that turns a failure into a deny is part of the hook.
	a.clock.Add(time.Second)
	if err := a.s.SetProjectGuard(a.ctx, a.p, mustStepUp(t, a.env, a.p), a.project, "enforce", ""); err != nil {
		t.Fatal(err)
	}
	if inst, drift := info(); !inst || drift != "" {
		t.Fatalf("enforce: installed=%v drift=%q", inst, drift)
	}
	b, _ := os.ReadFile(settings)
	if err := os.WriteFile(settings, []byte(strings.Replace(string(b), " || exit 2", "", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if inst, drift := info(); inst || !strings.Contains(drift, "was changed") {
		t.Errorf("enforce wrapper stripped: installed=%v drift=%q", inst, drift)
	}

	// Setting the policy again, with the passphrase, writes the hook back.
	a.clock.Add(time.Second)
	if err := a.s.SetProjectGuard(a.ctx, a.p, mustStepUp(t, a.env, a.p), a.project, "enforce", ""); err != nil {
		t.Fatal(err)
	}
	if inst, drift := info(); !inst || drift != "" {
		t.Fatalf("after repair: installed=%v drift=%q", inst, drift)
	}
	// A project that was never connected reports no drift.
	if drift := hookDrift(t.TempDir(), "audit"); !strings.Contains(drift, "is missing") {
		t.Errorf("empty folder: %q", drift)
	}
}

// A change to the hook during a task must reach the diff and the fingerprint
// even when an ignore rule hides the file.
func TestUntrackedFilesIncludeIgnoredConnectionFiles(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	for f, body := range map[string]string{".gitignore": ".claude/\n.mcp.json\n*.log\n", ".claude/settings.local.json": "{}", ".mcp.json": "{}", "debug.log": "x", "new.txt": "y"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, f)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := untrackedFiles(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"new.txt", ".claude/settings.local.json", ".mcp.json"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s missing from %v", want, got)
		}
	}
	if slices.Contains(got, "debug.log") {
		t.Errorf("an ordinary ignored file must stay out: %v", got)
	}
	fp1 := workspaceFingerprint(t.Context(), root)
	if fp1 == "" {
		t.Fatal("no fingerprint")
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "settings.local.json"), []byte(`{"disableAllHooks":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if fp2 := workspaceFingerprint(t.Context(), root); fp2 == fp1 {
		t.Error("editing an ignored connection file did not change the workspace fingerprint")
	}
	if out, err := exec.Command("git", "-C", root, "status", "--porcelain").Output(); err != nil || strings.Contains(string(out), "settings.local.json") {
		t.Fatalf("the test setup must really hide the file from git: %q %v", out, err)
	}
}

// The agent controls the settings file. A FIFO or a huge file there must neither hang nor fill memory:
// the check reports it as drift at once.
func TestHookDriftDoesNotBlockOnHostileSettingsFiles(t *testing.T) {
	a := startedFlow(t)
	settings := filepath.Join(a.root, ".claude", "settings.local.json")
	for name, setup := range map[string]func() error{
		"fifo": func() error { return syscall.Mkfifo(settings, 0o600) },
		"too large": func() error {
			return os.WriteFile(settings, []byte(`{"x":"`+strings.Repeat("a", maxConnectFile)+`"}`), 0o600)
		},
	} {
		if err := os.Remove(settings); err != nil {
			t.Fatal(err)
		}
		if err := setup(); err != nil {
			t.Fatal(err)
		}
		done := inBackground(func() string { return hookDrift(a.root, "audit") })
		select {
		case drift := <-done:
			if !strings.Contains(drift, "cannot be read") {
				t.Errorf("%s: drift %q", name, drift)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: hookDrift blocked", name)
		}
	}
}

func inBackground(f func() string) <-chan string {
	c := make(chan string, 1)
	go func() { c <- f() }()
	return c
}
