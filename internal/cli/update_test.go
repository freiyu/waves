package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"waves/internal/config"
)

// A stand-in release: a shell script that answers `version`, published with its SHA256SUMS on a local TLS server.
type fakeRelease struct {
	srv   *httptest.Server
	files map[string][]byte // path under /latest/download or /download/v<version>
}

func script(version string) []byte { return []byte("#!/bin/sh\necho \"waves " + version + "\"\n") }

func sumsFor(name string, body []byte) []byte {
	h := sha256.Sum256(body)
	return []byte(hex.EncodeToString(h[:]) + "  " + name + "\n")
}

func newRelease(t *testing.T, version string) *fakeRelease {
	t.Helper()
	name := "waves-linux-" + runtime.GOARCH
	r := &fakeRelease{files: map[string][]byte{}}
	body := script(version)
	r.files["/latest/download/"+name] = body
	r.files["/latest/download/SHA256SUMS"] = sumsFor(name, body)
	r.files["/download/v"+version+"/"+name] = body
	r.files["/download/v"+version+"/SHA256SUMS"] = sumsFor(name, body)
	r.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		b, ok := r.files[q.URL.Path]
		if !ok {
			http.NotFound(w, q)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(r.srv.Close)
	oldBase, oldClient := updateBase, updateClient
	updateBase = r.srv.URL
	updateClient = func() *http.Client { return r.srv.Client() }
	t.Cleanup(func() { updateBase, updateClient = oldBase, oldClient })
	return r
}

// updateEnv is an Env with a stand-in installed binary and no service listening (the port is one nothing answers on).
func updateEnv(t *testing.T, current string) (*Env, string, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	d := config.Dirs{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data"), State: filepath.Join(root, "state")}
	for _, p := range []string{d.Config, d.Data, d.State} {
		os.MkdirAll(p, 0o700)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	os.WriteFile(filepath.Join(d.Config, "config.json"), []byte(fmt.Sprintf(`{"port":%d}`, port)), 0o600)
	exe := filepath.Join(root, "bin", "waves")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	os.WriteFile(exe, script(current), 0o755)
	oldSelf, oldVer := selfPath, Version
	selfPath = func() (string, error) { return exe, nil }
	Version = current
	t.Cleanup(func() { selfPath, Version = oldSelf, oldVer })
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	out := &bytes.Buffer{}
	return &Env{Dirs: d, Out: out, Err: out, In: f}, exe, out
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func leftovers(t *testing.T, dir string) []string {
	t.Helper()
	es, _ := os.ReadDir(dir)
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

func TestUpdateInstallsAndKeepsThePreviousBinary(t *testing.T) {
	newRelease(t, "1.0.1")
	e, exe, out := updateEnv(t, "1.0.0")
	if err := e.update(context.Background(), nil); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := read(t, exe); got != string(script("1.0.1")) {
		t.Fatalf("binary not replaced: %q", got)
	}
	if got := read(t, exe+".previous"); got != string(script("1.0.0")) {
		t.Fatalf("previous binary not kept: %q", got)
	}
	if st, _ := os.Stat(exe); st.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", st.Mode())
	}
	if names := leftovers(t, filepath.Dir(exe)); len(names) != 2 {
		t.Fatalf("temporary files left behind: %v", names)
	}
	if !strings.Contains(out.String(), "1.0.0 -> 1.0.1") {
		t.Fatalf("output: %s", out)
	}
}

func TestUpdateRefusesAChecksumMismatchAndChangesNothing(t *testing.T) {
	r := newRelease(t, "1.0.1")
	name := "waves-linux-" + runtime.GOARCH
	r.files["/latest/download/"+name] = script("6.6.6") // not the file the sums describe
	e, exe, out := updateEnv(t, "1.0.0")
	err := e.update(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want a checksum mismatch, got %v\n%s", err, out)
	}
	if read(t, exe) != string(script("1.0.0")) {
		t.Fatal("the installed binary changed")
	}
	if names := leftovers(t, filepath.Dir(exe)); len(names) != 1 {
		t.Fatalf("temporary files left behind: %v", names)
	}
}

func TestUpdateRefusesWhenSumsDoNotNameTheFile(t *testing.T) {
	r := newRelease(t, "1.0.1")
	r.files["/latest/download/SHA256SUMS"] = sumsFor("waves-linux-other", script("1.0.1"))
	e, exe, _ := updateEnv(t, "1.0.0")
	if err := e.update(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "no entry") {
		t.Fatalf("got %v", err)
	}
	if read(t, exe) != string(script("1.0.0")) {
		t.Fatal("the installed binary changed")
	}
}

func TestUpdateRefusesAFileThatDoesNotRun(t *testing.T) {
	r := newRelease(t, "1.0.1")
	name := "waves-linux-" + runtime.GOARCH
	junk := []byte("not a program")
	r.files["/latest/download/"+name] = junk
	r.files["/latest/download/SHA256SUMS"] = sumsFor(name, junk)
	e, exe, _ := updateEnv(t, "1.0.0")
	if err := e.update(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "does not run") {
		t.Fatalf("got %v", err)
	}
	if read(t, exe) != string(script("1.0.0")) {
		t.Fatal("the installed binary changed")
	}
}

func TestUpdateWithNothingNewerChangesNothing(t *testing.T) {
	newRelease(t, "1.0.1")
	e, exe, out := updateEnv(t, "1.0.1")
	if err := e.update(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already 1.0.1") || read(t, exe) != string(script("1.0.1")) {
		t.Fatalf("output %q", out)
	}
	if _, err := os.Stat(exe + ".previous"); err == nil {
		t.Fatal("a previous binary was made for nothing")
	}
}

// A binary newer than the release found (a build from source) is left alone; --force installs the release anyway.
func TestUpdateKeepsABinaryNewerThanTheRelease(t *testing.T) {
	newRelease(t, "1.0.1")
	e, exe, out := updateEnv(t, "1.0.2")
	if err := e.update(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "newer than the release found") || read(t, exe) != string(script("1.0.2")) {
		t.Fatalf("output %q", out)
	}
	if err := e.update(context.Background(), []string{"--force"}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if read(t, exe) != string(script("1.0.1")) {
		t.Fatal("--force did not install the release")
	}
}

func TestUpdateVersionMustMatchWhatTheFileReports(t *testing.T) {
	r := newRelease(t, "1.0.1")
	name := "waves-linux-" + runtime.GOARCH
	// The release called 1.0.2 holds a binary that says it is 1.0.1.
	r.files["/download/v1.0.2/"+name] = script("1.0.1")
	r.files["/download/v1.0.2/SHA256SUMS"] = sumsFor(name, script("1.0.1"))
	e, exe, _ := updateEnv(t, "1.0.0")
	if err := e.update(context.Background(), []string{"--version", "1.0.2"}); err == nil || !strings.Contains(err.Error(), "reports 1.0.1") {
		t.Fatalf("got %v", err)
	}
	if read(t, exe) != string(script("1.0.0")) {
		t.Fatal("the installed binary changed")
	}
}

// An older release can refuse a database a newer one migrated: it is installed only when the human says so with --force.
func TestUpdateToAnOlderReleaseNeedsForce(t *testing.T) {
	newRelease(t, "0.9.0")
	e, exe, out := updateEnv(t, "1.0.0")
	err := e.update(context.Background(), []string{"--version", "v0.9.0"})
	if err == nil || !strings.Contains(err.Error(), "older than the installed") {
		t.Fatalf("got %v", err)
	}
	if read(t, exe) != string(script("1.0.0")) {
		t.Fatal("the installed binary changed")
	}
	if err := e.update(context.Background(), []string{"--version", "v0.9.0", "--force"}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if read(t, exe) != string(script("0.9.0")) {
		t.Fatal("older release not installed")
	}
}

func TestUpdateRefusesAnAddressThatIsNotHTTPS(t *testing.T) {
	oldBase := updateBase
	updateBase = "http://127.0.0.1:1/releases"
	t.Cleanup(func() { updateBase = oldBase })
	e, exe, _ := updateEnv(t, "1.0.0")
	if err := e.update(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "not https") {
		t.Fatalf("got %v", err)
	}
	if read(t, exe) != string(script("1.0.0")) {
		t.Fatal("the installed binary changed")
	}
}

func TestUpdateRefusesARedirectToPlainHTTP(t *testing.T) {
	var hits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { hits.Add(1); w.Write([]byte("x")) }))
	t.Cleanup(plain.Close)
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { http.Redirect(w, q, plain.URL+"/x", http.StatusFound) }))
	t.Cleanup(tls.Close)
	oldBase, oldClient := updateBase, updateClient
	updateBase = tls.URL
	def := oldClient()
	updateClient = func() *http.Client { c := tls.Client(); c.CheckRedirect = def.CheckRedirect; return c }
	t.Cleanup(func() { updateBase, updateClient = oldBase, oldClient })
	e, exe, _ := updateEnv(t, "1.0.0")
	if err := e.update(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "refusing a redirect") {
		t.Fatalf("got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("the plain http server was asked %d time(s)", hits.Load())
	}
	if read(t, exe) != string(script("1.0.0")) {
		t.Fatal("the installed binary changed")
	}
}

func TestUpdateFromAFolderChecksTheSumsToo(t *testing.T) {
	folder := t.TempDir()
	name := "waves-linux-" + runtime.GOARCH
	os.WriteFile(filepath.Join(folder, name), script("1.0.1"), 0o644)
	os.WriteFile(filepath.Join(folder, "SHA256SUMS"), sumsFor(name, script("9.9.9")), 0o644) // sums of another file
	e, exe, _ := updateEnv(t, "1.0.0")
	if err := e.update(context.Background(), []string{"--from", folder}); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("got %v", err)
	}
	if read(t, exe) != string(script("1.0.0")) {
		t.Fatal("the installed binary changed")
	}
	os.WriteFile(filepath.Join(folder, "SHA256SUMS"), sumsFor(name, script("1.0.1")), 0o644)
	if err := e.update(context.Background(), []string{"--from", folder}); err != nil {
		t.Fatal(err)
	}
	if read(t, exe) != string(script("1.0.1")) {
		t.Fatal("not installed from the folder")
	}
}

func TestUpdateRejectsOddArguments(t *testing.T) {
	r := newRelease(t, "1.0.1")
	var asked atomic.Int32
	inner := r.srv.Config.Handler
	r.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { asked.Add(1); inner.ServeHTTP(w, q) })
	e, exe, _ := updateEnv(t, "1.0.0")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"extra"}, "usage"},
		{[]string{"--nope"}, "usage"},
		{[]string{"--version", "1.0 ; rm -rf /"}, "release number"},
		{[]string{"--version", "../x"}, "release number"},
		{[]string{"--version", ""}, "need a value"},
		{[]string{"--from", ""}, "need a value"},
	} {
		err := e.update(context.Background(), c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%v: got %v, want %q", c.args, err, c.want)
		}
	}
	if asked.Load() != 0 {
		t.Fatalf("the release server was asked %d time(s) for arguments that should have been refused first", asked.Load())
	}
	if read(t, exe) != string(script("1.0.0")) {
		t.Fatal("the installed binary changed")
	}
}

func TestReleaseSourceIsHTTPS(t *testing.T) {
	if !strings.HasPrefix(updateBase, "https://github.com/freiyu/waves/") {
		t.Fatalf("the release source is %q", updateBase)
	}
}

func TestUpdateSumsParsing(t *testing.T) {
	name := "waves-linux-" + runtime.GOARCH
	body := script("1.0.1")
	h := sha256.Sum256(body)
	hexsum := hex.EncodeToString(h[:])
	dir := t.TempDir()
	file := filepath.Join(dir, name)
	os.WriteFile(file, body, 0o644)
	for _, c := range []struct {
		label, sums, want string
	}{
		{"star prefix and upper case are accepted", strings.ToUpper(hexsum) + "  *" + name + "\n", ""},
		{"other files in the list are ignored", strings.Repeat("a", 64) + "  other\n" + hexsum + "  " + name + "\n", ""},
		{"the same line twice is fine", hexsum + "  " + name + "\n" + hexsum + "  " + name + "\n", ""},
		{"two different sums for one name are refused", hexsum + "  " + name + "\n" + strings.Repeat("b", 64) + "  " + name + "\n", "twice"},
		{"a path-prefixed name does not count", hexsum + "  ./" + name + "\n", "no entry"},
		{"a short sum does not count", hexsum[:40] + "  " + name + "\n", "no entry"},
	} {
		sums := filepath.Join(dir, "SHA256SUMS")
		os.WriteFile(sums, []byte(c.sums), 0o644)
		err := verifySum(sums, file, name)
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: got %v", c.label, err)
		}
	}
}

func TestUpdateFromAFolderRefusesAPipe(t *testing.T) {
	folder := t.TempDir()
	name := "waves-linux-" + runtime.GOARCH
	if err := syscall.Mkfifo(filepath.Join(folder, name), 0o600); err != nil {
		t.Skip("no fifo here:", err)
	}
	os.WriteFile(filepath.Join(folder, "SHA256SUMS"), sumsFor(name, script("1.0.1")), 0o644)
	e, exe, _ := updateEnv(t, "1.0.0")
	if err := e.update(context.Background(), []string{"--from", folder}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("got %v", err)
	}
	if read(t, exe) != string(script("1.0.0")) {
		t.Fatal("the installed binary changed")
	}
}

// A service that answers on the port but that waves start did not start (no process id file) cannot be restarted from here.
func TestUpdateRefusesAServiceItDidNotStart(t *testing.T) {
	newRelease(t, "1.0.1")
	e, exe, _ := updateEnv(t, "1.0.0")
	cfg, err := config.Load(e.Dirs)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", cfg.Addr())
	if err != nil {
		t.Skip("port taken:", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { w.WriteHeader(200) })}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	err = e.update(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "was not started with waves start") {
		t.Fatalf("got %v", err)
	}
	if read(t, exe) != string(script("1.0.0")) {
		t.Fatal("the installed binary changed")
	}
	if _, serr := os.Stat(exe + ".previous"); serr == nil {
		t.Fatal("a previous binary was made for nothing")
	}
}

func TestVersionOrder(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"1.0.1", "1.0.0", true}, {"1.0.0", "1.0.1", false}, {"1.0.0", "1.0.0", false}, {"1.10.0", "1.9.9", true},
		{"1.0.1", "1.0.1-dev", true}, {"1.0.1-dev", "1.0.1", false}, {"2.0.0", "0.1.0-dev", true}, {"1.0.0", "dev", true},
	} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}
