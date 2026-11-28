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

// A user of 1.0.1 runs `waves update` and gets 1.2.0: the real 1.0.1 binary (built from its tag) replaces itself from a release
// folder with the checksum file, the service restarts on the same database without a migration, a result that waited for approval
// in 1.0.1 is still decided by a human with the passphrase, and the project's installed hook still runs.
func TestUpdateFrom101To120OnTheRealBinary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	i := newInst(t)
	i.stop() // this test runs the background service, as a user would
	root := t.TempDir()
	old := filepath.Join(root, "bin", "waves")
	os.MkdirAll(filepath.Dir(old), 0o755)
	built := buildReleaseAs(t, "v1.01-stage-7-approved", "1.0.1")
	b, _ := os.ReadFile(built)
	if err := os.WriteFile(old, b, 0o755); err != nil {
		t.Fatal(err)
	}
	oldBytes := b
	current := bin
	bin = old
	defer func() { bin = current }()
	rel := filepath.Join(root, "release")
	os.MkdirAll(rel, 0o755)
	name := "waves-linux-" + runtime.GOARCH
	buildAs(t, "1.2.0", filepath.Join(rel, name))
	nb, _ := os.ReadFile(filepath.Join(rel, name))
	h := sha256.Sum256(nb)
	os.WriteFile(filepath.Join(rel, "SHA256SUMS"), []byte(hex.EncodeToString(h[:])+"  "+name+"\n"), 0o644)

	if out, code := i.runAs(old, "start"); code != 0 {
		t.Fatalf("start: %s", out)
	}
	t.Cleanup(func() { i.runAs(old, "stop") })
	if v := i.serverVersion(); v != "1.0.1" {
		t.Fatalf("service version %q", v)
	}
	// What 1.0.1 leaves behind: a task whose result waits for the human's approval.
	p, a := i.planned("legacy")
	task, _ := a.claim()
	os.WriteFile(filepath.Join(p.dir, "hello.txt"), []byte("hello\n"), 0o600)
	a.must("waves_submit_result", map[string]any{"summary": "wrote hello.txt", "status": "completed"}, nil)
	if s := i.taskState(task); s != "In Review" {
		t.Fatalf("1.0.1 leaves the submitted result %s", s)
	}
	a.kill()
	schema := i.count(`SELECT max(version) FROM schema_migrations`)

	out, code := i.runAs(old, "update", "--from", rel)
	if code != 0 || !strings.Contains(out, "1.0.1 -> 1.2.0") || !strings.Contains(out, "service was restarted") {
		t.Fatalf("update (exit %d): %s", code, out)
	}
	deadline := time.Now().Add(10 * time.Second)
	for i.serverVersion() != "1.2.0" && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if v := i.serverVersion(); v != "1.2.0" {
		t.Fatalf("service version after the update: %q", v)
	}
	if b, _ := os.ReadFile(old + ".previous"); string(b) != string(oldBytes) {
		t.Fatal("the 1.0.1 binary was not kept as waves.previous")
	}
	if out, _ := i.runAs(old, "version"); strings.TrimSpace(out) != "waves 1.2.0" {
		t.Fatalf("version after the update: %q", out)
	}
	noLeftovers(t, old)
	if v := i.count(`SELECT max(version) FROM schema_migrations`); v != schema {
		t.Fatalf("schema version %d after the update, was %d: 1.2.0 has no migration", v, schema)
	}
	if m, _ := filepath.Glob(i.dbPath() + ".pre-v*.bak"); len(m) != 0 {
		t.Fatalf("a migration backup exists although nothing was migrated: %v", m)
	}
	// The result that waited in 1.0.1 is decided as before: not without the passphrase, then by a human with it.
	apr := p.pending("result")
	if st, _ := i.operator().call("POST", "/approvals/"+apr+"/decision", map[string]string{"decision": "approve"}, nil, nil); st < 400 {
		t.Fatalf("the operator token decided the legacy result without a passphrase: %d", st)
	}
	i.dashboard().mustDecide(apr, "approve", nil)
	if s := i.taskState(task); s != "Done" {
		t.Fatalf("after the human approved it the task is %s", s)
	}
	// The hook that 1.0.1 installed in the project still runs under 1.2.0.
	if code, _ := i.runHook(p.installedHook(t), writeEvent("sess-old", p.dir, filepath.Join(p.dir, "x.txt"), "toolu_up")); code != 0 {
		t.Fatalf("the 1.0.1 guard hook under 1.2.0: exit %d", code)
	}
}

// A user of 1.2.0 runs `waves update` and gets 1.2.1, and can go back: the real 1.2.0 binary (built from its tag) replaces
// itself from a release folder, the service restarts on the same database without a migration, a phase review that waited in
// 1.2.0 is still decided only by a human with the passphrase, the installed hook still runs, and 1.2.0 opens the database again.
func TestUpdateFrom120To121AndBackOnTheRealBinary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	i := newInst(t)
	i.stop() // this test runs the background service, as a user would
	root := t.TempDir()
	installed := filepath.Join(root, "bin", "waves")
	os.MkdirAll(filepath.Dir(installed), 0o755)
	name := "waves-linux-" + runtime.GOARCH
	// A release folder as `make release` lays it out: the binary and its checksum.
	folder := func(dir, binary string) string {
		d := filepath.Join(root, dir)
		os.MkdirAll(d, 0o755)
		b, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(b)
		os.WriteFile(filepath.Join(d, name), b, 0o755)
		os.WriteFile(filepath.Join(d, "SHA256SUMS"), []byte(hex.EncodeToString(h[:])+"  "+name+"\n"), 0o644)
		return d
	}
	was := folder("was", buildReleaseAs(t, "v1.2-stage-6-approved", "1.2.0"))
	candidate := filepath.Join(t.TempDir(), "waves-next")
	buildAs(t, "1.2.1", candidate)
	next := folder("next", candidate)
	oldBytes, _ := os.ReadFile(filepath.Join(was, name))
	if err := os.WriteFile(installed, oldBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	current := bin
	bin = installed
	defer func() { bin = current }()

	runs := func(version string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for i.serverVersion() != version && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if v := i.serverVersion(); v != version {
			t.Fatalf("service version %q, want %s", v, version)
		}
		if out, _ := i.runAs(installed, "version"); strings.TrimSpace(out) != "waves "+version {
			t.Fatalf("binary version %q, want %s", out, version)
		}
	}
	if out, code := i.runAs(installed, "start"); code != 0 {
		t.Fatalf("start: %s", out)
	}
	t.Cleanup(func() { i.runAs(installed, "stop") })
	runs("1.2.0")
	// What 1.2.0 leaves behind: a finished phase whose review waits for the human.
	p, a := i.planned("carried")
	a.claim()
	os.WriteFile(filepath.Join(p.dir, "hello.txt"), []byte("hello\n"), 0o600)
	a.must("waves_submit_result", map[string]any{"summary": "wrote hello.txt", "status": "completed"}, nil)
	waiting := func() bool {
		return i.count(`SELECT count(*) FROM approvals WHERE category = 'phase_completion' AND state = 'pending'`) == 1
	}
	for deadline := time.Now().Add(15 * time.Second); !waiting() && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
	}
	apr := p.pending("phase_completion")
	a.kill()
	schema := i.count(`SELECT max(version) FROM schema_migrations`)

	out, code := i.runAs(installed, "update", "--from", next)
	if code != 0 || !strings.Contains(out, "1.2.0 -> 1.2.1") || !strings.Contains(out, "service was restarted") {
		t.Fatalf("update (exit %d): %s", code, out)
	}
	runs("1.2.1")
	if b, _ := os.ReadFile(installed + ".previous"); string(b) != string(oldBytes) {
		t.Fatal("the 1.2.0 binary was not kept as waves.previous")
	}
	noLeftovers(t, installed)
	if v := i.count(`SELECT max(version) FROM schema_migrations`); v != schema {
		t.Fatalf("schema version %d after the update, was %d: 1.2.1 has no migration", v, schema)
	}
	if m, _ := filepath.Glob(i.dbPath() + ".pre-v*.bak"); len(m) != 0 {
		t.Fatalf("a migration backup exists although nothing was migrated: %v", m)
	}
	if st, _ := i.operator().call("POST", "/approvals/"+apr+"/decision", map[string]string{"decision": "approve"}, nil, nil); st < 400 {
		t.Fatalf("the operator token decided the waiting review without a passphrase: %d", st)
	}
	hook := p.installedHook(t) // installed by 1.2.0
	if code, _ := i.runHook(hook, writeEvent("sess-carried", p.dir, filepath.Join(p.dir, "x.txt"), "toolu_up")); code != 0 {
		t.Fatalf("the 1.2.0 guard hook under 1.2.1: exit %d", code)
	}
	i.dashboard().mustDecide(apr, "approve", nil)

	// Back again: an older release needs --force, then opens the same database.
	if out, _ := i.runAs(installed, "update", "--from", was); !strings.Contains(out, "Nothing changed") {
		t.Fatalf("an older release without --force: %s", out)
	}
	runs("1.2.1")
	if out, code := i.runAs(installed, "update", "--from", was, "--force"); code != 0 {
		t.Fatalf("back to 1.2.0 (exit %d): %s", code, out)
	}
	runs("1.2.0")
	if got := i.count(`SELECT count(*) FROM approvals WHERE state = 'approved' AND category = 'phase_completion'`); got != 1 {
		t.Fatalf("1.2.0 sees %d approved reviews, want the one 1.2.1 recorded", got)
	}
	if code, _ := i.runHook(hook, writeEvent("sess-back", p.dir, filepath.Join(p.dir, "y.txt"), "toolu_back")); code != 0 {
		t.Fatalf("the guard hook after going back: exit %d", code)
	}
}
