//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// buildAs builds the real binary (static, dashboard embedded) that reports the given version.
func buildAs(t *testing.T, version, out string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -X main.version="+version, "-o", out, "../../cmd/waves")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", version, err, b)
	}
}

func (i *inst) serverVersion() string {
	req, _ := http.NewRequest("GET", i.url("/api/v1/status"), nil)
	req.Header.Set("Authorization", "Bearer "+i.op)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "down: " + err.Error()
	}
	defer res.Body.Close()
	var s struct {
		V string `json:"server_version"`
	}
	json.NewDecoder(res.Body).Decode(&s)
	return s.V
}

// noLeftovers fails when a half-finished update left its temporary folder next to the binary.
func noLeftovers(t *testing.T, binary string) {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(filepath.Dir(binary), ".waves-update-*"))
	if len(m) != 0 {
		t.Fatalf("temporary update folders left behind: %v", m)
	}
}

func (i *inst) runAs(binary string, args ...string) (string, int) {
	cmd := exec.Command(binary, args...)
	cmd.Env = i.env
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		i.t.Fatal(err)
	}
	return string(out), code
}

// waves update on the real binary: a checksum that does not match changes nothing, running executions stop it, and with --force the
// service is stopped, the file swapped and the new version started, the old file kept as waves.previous.
func TestUpdateOnTheRealBinary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	i := newInst(t)
	i.stop() // the harness runs its own foreground service; this test runs the background one, as a user would
	root := t.TempDir()
	old := filepath.Join(root, "bin", "waves")
	os.MkdirAll(filepath.Dir(old), 0o755)
	buildAs(t, "1.0.0", old)
	rel := filepath.Join(root, "release")
	os.MkdirAll(rel, 0o755)
	name := "waves-linux-" + runtime.GOARCH
	buildAs(t, "1.0.1", filepath.Join(rel, name))
	nb, _ := os.ReadFile(filepath.Join(rel, name))
	h := sha256.Sum256(nb)
	good := hex.EncodeToString(h[:]) + "  " + name + "\n"
	os.WriteFile(filepath.Join(rel, "SHA256SUMS"), []byte(strings.Repeat("0", 64)+"  "+name+"\n"), 0o644)
	oldBytes, _ := os.ReadFile(old)

	if out, code := i.runAs(old, "start"); code != 0 {
		t.Fatalf("start: %s", out)
	}
	t.Cleanup(func() { i.runAs(old, "stop") })
	if v := i.serverVersion(); v != "1.0.0" {
		t.Fatalf("service version %q", v)
	}

	// 1. A wrong checksum: refused, the file and the service are untouched.
	out, code := i.runAs(old, "update", "--from", rel)
	if code == 0 || !strings.Contains(out, "checksum mismatch") {
		t.Fatalf("a wrong checksum was accepted (exit %d): %s", code, out)
	}
	if b, _ := os.ReadFile(old); string(b) != string(oldBytes) {
		t.Fatal("the binary changed on a checksum mismatch")
	}
	if v := i.serverVersion(); v != "1.0.0" {
		t.Fatalf("the service was disturbed: %q", v)
	}
	noLeftovers(t, old)
	os.WriteFile(filepath.Join(rel, "SHA256SUMS"), []byte(good), 0o644)

	// 2. A running execution: refused without --force, nothing changed.
	_, ra := i.planned("busy")
	ra.claim()
	out, code = i.runAs(old, "update", "--from", rel)
	if code == 0 || !strings.Contains(out, "execution is running") {
		t.Fatalf("running executions did not stop the update (exit %d): %s", code, out)
	}
	if b, _ := os.ReadFile(old); string(b) != string(oldBytes) {
		t.Fatal("the binary changed although executions were running")
	}
	if v := i.serverVersion(); v != "1.0.0" {
		t.Fatalf("the service was disturbed: %q", v)
	}

	// 3. --force: the new version answers on the same port, the old file is kept, and the interrupted task is not run again.
	out, code = i.runAs(old, "update", "--from", rel, "--force")
	if code != 0 || !strings.Contains(out, "1.0.0 -> 1.0.1") || !strings.Contains(out, "service was restarted") {
		t.Fatalf("forced update (exit %d): %s", code, out)
	}
	deadline := time.Now().Add(10 * time.Second)
	for i.serverVersion() != "1.0.1" && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if v := i.serverVersion(); v != "1.0.1" {
		t.Fatalf("service version after the update: %q", v)
	}
	if b, _ := os.ReadFile(old + ".previous"); string(b) != string(oldBytes) {
		t.Fatal("the previous binary was not kept")
	}
	if b, _ := os.ReadFile(old); string(b) != string(nb) {
		t.Fatal("the new binary is not in place")
	}
	if out, _ := i.runAs(old, "version"); strings.TrimSpace(out) != "waves 1.0.1" {
		t.Fatalf("version after the update: %q", out)
	}
	// The states are stored capitalised: a lower-case comparison would always count zero and prove nothing.
	if n := i.count(`SELECT count(*) FROM executions WHERE state IN ('Running','Starting')`); n != 0 {
		t.Fatalf("%d execution(s) still running after the restart: an interrupted task must wait for the human", n)
	}
	if st := i.str(`SELECT state FROM executions LIMIT 1`); st != "Interrupted" {
		t.Fatalf("the forced update left the execution %q: it must be Interrupted and wait for the human", st)
	}
	if n := i.count(`SELECT count(*) FROM executions`); n != 1 {
		t.Fatalf("%d executions: the restart started another one", n)
	}
}

// A new version that does not come up is not left in place: the old file is put back and the old service started again.
func TestUpdateRollsBackWhenTheNewVersionDoesNotStart(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	i := newInst(t)
	i.stop()
	root := t.TempDir()
	old := filepath.Join(root, "bin", "waves")
	os.MkdirAll(filepath.Dir(old), 0o755)
	buildAs(t, "1.0.0", old)
	oldBytes, _ := os.ReadFile(old)
	rel := filepath.Join(root, "release")
	os.MkdirAll(rel, 0o755)
	name := "waves-linux-" + runtime.GOARCH
	broken := []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo \"waves 1.0.2\"; exit 0; fi\nexit 1\n") // reports a version, never serves
	os.WriteFile(filepath.Join(rel, name), broken, 0o755)
	h := sha256.Sum256(broken)
	os.WriteFile(filepath.Join(rel, "SHA256SUMS"), []byte(hex.EncodeToString(h[:])+"  "+name+"\n"), 0o644)

	if out, code := i.runAs(old, "start"); code != 0 {
		t.Fatalf("start: %s", out)
	}
	t.Cleanup(func() { i.runAs(old, "stop") })
	out, code := i.runAs(old, "update", "--from", rel)
	if code == 0 || !strings.Contains(out, "did not start") || !strings.Contains(out, "old service is running again") {
		t.Fatalf("a version that does not start was accepted (exit %d): %s", code, out)
	}
	if b, _ := os.ReadFile(old); string(b) != string(oldBytes) {
		t.Fatal("the old binary was not put back")
	}
	deadline := time.Now().Add(10 * time.Second)
	for i.serverVersion() != "1.0.0" && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if v := i.serverVersion(); v != "1.0.0" {
		t.Fatalf("the old service is not running again: %q", v)
	}
	noLeftovers(t, old)
	if _, err := os.Stat(old + ".previous"); err == nil {
		t.Fatal("the rolled-back update left waves.previous behind")
	}
}
