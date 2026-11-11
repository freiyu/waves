//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakeClaude gives the commands a PATH of their own with a stand-in `claude` first, so doctor's Claude Code check does not depend on this machine.
func withFakeClaude(t *testing.T, i *inst) {
	t.Helper()
	d := filepath.Join(i.dir, "fakebin")
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "claude"), []byte("#!/bin/sh\necho '2.1.0 (Claude Code)'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for n, e := range i.env {
		if strings.HasPrefix(e, "PATH=") {
			i.env[n] = "PATH=" + d + ":/usr/bin:/bin" // git is in /usr/bin; a Claude Code on the developer's PATH must not leak in
		}
	}
}

func TestDoctor(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	withFakeClaude(t, i)

	out, code := i.run("", "doctor")
	if code != 0 || strings.Contains(out, "FAIL") {
		t.Fatalf("healthy instance: exit %d\n%s", code, out)
	}
	for _, want := range []string{"operator.token", "guard.token", "git", "claude", "service", "audit chain"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output lacks %q\n%s", want, out)
		}
	}

	// A config directory other than 0700 is a failure and names the directory.
	if err := os.Chmod(i.cfgDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	out, code = i.run("", "doctor")
	if code != 1 || !strings.Contains(out, "FAIL  dir") {
		t.Errorf("loose config dir: exit %d\n%s", code, out)
	}
	if err := os.Chmod(i.cfgDir(), 0o700); err != nil {
		t.Fatal(err)
	}

	// No Claude Code on PATH is reported, and the other checks still run.
	if err := os.Remove(filepath.Join(i.dir, "fakebin", "claude")); err != nil {
		t.Fatal(err)
	}
	out, code = i.run("", "doctor")
	if code != 1 || !strings.Contains(out, "FAIL  claude") || !strings.Contains(out, "audit chain") {
		t.Errorf("no claude: exit %d\n%s", code, out)
	}

	// A stopped service is reported with the way out.
	i.stop()
	out, code = i.run("", "doctor")
	if code != 1 || !strings.Contains(out, "FAIL  service") || !strings.Contains(out, "waves start") {
		t.Errorf("service stopped: exit %d\n%s", code, out)
	}
}
