package cli

// `waves update` replaces this binary with a published release. The network is used only while the command runs: nothing checks
// for updates in the background. A file is installed only after its SHA256 matches the SHA256SUMS of the same release (that
// catches a damaged download, not a compromised release: both files come from the same place). The old binary is kept next to the
// new one as waves.previous. A process running as the same user can swap the verified file between the check and the rename; that is
// inside the boundary Waves documents as not defended (that user can overwrite the binary directly).

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"waves/internal/api/contract"
)

const (
	releaseRepo    = "freiyu/waves"
	maxSumsBytes   = 1 << 20
	maxBinaryBytes = 200 << 20
	startWait      = 30 * time.Second // a migration with its backup can take a while on a large database
)

// Package variables so a test can point the command at a local TLS server and a stand-in binary. No flag or environment variable
// reaches them: the release source of a real run is fixed.
var (
	updateBase   = "https://github.com/" + releaseRepo + "/releases"
	updateClient = func() *http.Client {
		return &http.Client{
			Timeout: 10 * time.Minute,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if req.URL.Scheme != "https" {
					return fmt.Errorf("refusing a redirect to %s: downloads must stay on https", req.URL.Redacted())
				}
				if len(via) >= 5 {
					return errors.New("too many redirects")
				}
				return nil
			},
		}
	}
	selfPath = os.Executable
)

var versionWord = regexp.MustCompile(`^[0-9A-Za-z._-]+$`)

func (e *Env) update(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	version := fs.String("version", "", "install this release instead of the newest, for example 1.0.1")
	from := fs.String("from", "", "use waves-linux-<arch> and SHA256SUMS you already downloaded into this folder")
	force := fs.Bool("force", false, "update although executions are running (they are interrupted), or to an older release")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errUsage
	}
	if fs.NArg() != 0 {
		return errUsage
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if (given["version"] && *version == "") || (given["from"] && *from == "") {
		return errors.New("--version and --from need a value (is a shell variable empty?)")
	}
	want := strings.TrimPrefix(*version, "v")
	if given["version"] && !versionWord.MatchString(want) {
		return errors.New("--version takes a release number such as 1.0.1")
	}
	if runtime.GOOS != "linux" {
		return errors.New("Waves runs on Linux and WSL2 only")
	}
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("unsupported CPU %s: releases are built for amd64 and arm64", arch)
	}
	name := "waves-linux-" + arch

	exe, err := selfPath()
	if err != nil {
		return fmt.Errorf("find this binary: %w", err)
	}
	target, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", exe, err)
	}
	dir := filepath.Dir(target)
	// A run that was killed leaves its temporary folder: remove the ones an hour old, never one that may belong to a run in progress.
	if old, _ := filepath.Glob(filepath.Join(dir, ".waves-update-*")); len(old) > 0 {
		for _, p := range old {
			if st, err := os.Stat(p); err == nil && st.IsDir() && time.Since(st.ModTime()) > time.Hour {
				os.RemoveAll(p)
			}
		}
	}
	tmp, err := os.MkdirTemp(dir, ".waves-update-")
	if err != nil {
		return fmt.Errorf("cannot write next to %s (%w). Nothing was changed. Run the update as the user who owns that folder, or install again with install.sh", target, err)
	}
	defer os.RemoveAll(tmp)

	// Until the point of no return Ctrl-C ends the command; after it the update is finished or rolled back first.
	base := ctx
	ctx, stopSig := signal.NotifyContext(base, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stopSig()

	// 1. Get both files, then check the binary against the sums before anything else touches it.
	if *from != "" {
		err = copyFrom(*from, tmp, name)
	} else {
		err = e.download(ctx, want, tmp, name)
	}
	if err != nil {
		return err
	}
	cand := filepath.Join(tmp, name)
	if err := verifySum(filepath.Join(tmp, "SHA256SUMS"), cand, name); err != nil {
		return err
	}
	if err := os.Chmod(cand, 0o755); err != nil {
		return fmt.Errorf("make the downloaded binary executable: %w", err)
	}
	newVersion, err := reportedVersion(ctx, cand)
	if err != nil {
		return fmt.Errorf("the downloaded binary passed its checksum but does not run here (%w). Nothing was changed. Download it again, or report it", err)
	}
	if want != "" && newVersion != want {
		return fmt.Errorf("asked for %s but the downloaded binary reports %s. Nothing was changed. Check the release, or download again", want, newVersion)
	}
	if newVersion == Version {
		fmt.Fprintf(e.Out, "Waves is already %s. Nothing changed.\n", Version)
		return nil
	}
	if want == "" && !newer(newVersion, Version) && !*force {
		fmt.Fprintf(e.Out, "Waves %s is newer than the release found (%s). Nothing changed. --force installs it anyway.\n", Version, newVersion)
		return nil
	}
	if newer(Version, newVersion) && !*force {
		return fmt.Errorf("release %s is older than the installed %s, and an older version can refuse to start on a database a newer one has already migrated. Nothing was changed. Make a copy with waves backup, then run again with --force to install it anyway", newVersion, Version)
	}

	// 2. A service that waves start did not start cannot be stopped or restarted from here; running executions are not interrupted
	// unless told so.
	running := e.pid() != 0
	if !running && e.healthy(ctx) {
		return errors.New("a Waves service answers on this port but was not started with waves start (there is no matching process id file), so the update cannot stop and restart it. Nothing was changed. Stop that service yourself, then run the update again")
	}
	if running && !*force {
		n, err := e.runningExecutions(ctx)
		if err != nil {
			return fmt.Errorf("Waves could not check whether executions are running (%w). Nothing was changed. Run waves stop first, or run again with --force", err)
		}
		if n > 0 {
			return fmt.Errorf("%s running. Updating restarts the service and interrupts them; an interrupted task is never run again by itself. Nothing was changed. Finish or cancel them, or run again with --force", plural(n, "execution is", "executions are"))
		}
	}

	// Point of no return: signals are held back until the service is running again (or the old binary is).
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("interrupted before anything was changed: %w", err)
	}
	stopSig()
	hold := make(chan os.Signal, 1)
	signal.Notify(hold, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(hold)
	signal.Ignore(syscall.SIGPIPE) // a closed terminal must not end the update halfway through its output
	ctx = base

	// 3. The old binary stays as waves.previous (a hard link, so the file never goes missing); this comes before the service is
	// stopped so that a folder that cannot hold a link costs nothing. Then one rename puts the new file in place.
	prev := target + ".previous"
	if err := os.Remove(prev); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("could not clear an earlier %s (%w). Nothing was changed", prev, err)
	}
	if err := os.Link(target, prev); err != nil {
		return fmt.Errorf("could not keep a copy of the old binary as %s (%w). Nothing was changed. Hard links are needed in that folder; install again with install.sh", prev, err)
	}
	if running {
		if err := e.stop(ctx, nil); err != nil {
			return fmt.Errorf("could not stop the service (%w). The binary is unchanged. Check with waves status", err)
		}
	}
	if err := os.Rename(cand, target); err != nil {
		os.Remove(prev)
		return fmt.Errorf("could not put the new binary in place (%w). The old binary is unchanged.%s", err, e.resume(ctx, running, target))
	}

	// 4. Start the new service and make sure it is the new version that answers; otherwise put the old binary back.
	if running {
		pid, err := e.startBinary(ctx, target, startWait)
		if err == nil {
			err = e.expectVersion(ctx, newVersion)
		}
		if err != nil {
			e.reap(pid)
			if rerr := os.Rename(prev, target); rerr != nil {
				return fmt.Errorf("the new version did not start (%v) and the old binary could not be put back (%v). It is kept as %s: move it over %s yourself, then run waves start. If the old version refuses the database, run waves restore with the waves.db.pre-v<N>-<time>.bak next to waves.db", err, rerr, prev, target)
			}
			_, serr := e.startBinary(ctx, target, startWait)
			if serr == nil {
				serr = e.expectVersion(ctx, Version)
			}
			back := "The old binary is back and the old service is running again."
			if serr != nil {
				back = fmt.Sprintf("The old binary is back, but the old service is not confirmed to be running (%v).", serr)
			}
			return fmt.Errorf("the new version did not start (%v). %s If the old version refuses the database because the new one already upgraded it: waves stop, then waves restore with the waves.db.pre-v<N>-<time>.bak in the data folder, then waves start", err, back)
		}
	}
	fmt.Fprintf(e.Out, "Updated Waves %s -> %s.\n", Version, newVersion)
	fmt.Fprintf(e.Out, "The old binary is kept as %s. To go back before the new version has run: stop Waves and move it over %s. After it has run it has upgraded the database: also run waves restore with the waves.db.pre-v<N>-<time>.bak next to waves.db.\n", prev, target)
	if running {
		fmt.Fprintln(e.Out, "The service was restarted.")
	} else {
		fmt.Fprintln(e.Out, "Waves was not running. waves start applies the database upgrade, after writing a backup.")
	}
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// resume starts the service again after an update that was given up once the service had been stopped, and says how that went.
func (e *Env) resume(ctx context.Context, was bool, bin string) string {
	if !was {
		return ""
	}
	if _, err := e.startBinary(ctx, bin, startWait); err != nil {
		return fmt.Sprintf(" Waves could not start the service again (%v).", err)
	}
	return " The service is running again."
}

// reap stops a service process this command started and waits for it, so that no new version keeps running while the old file goes back.
func (e *Env) reap(pid int) {
	if pid > 0 {
		syscall.Kill(pid, syscall.SIGTERM)
		for i := 0; i < 100 && !gone(pid); i++ {
			time.Sleep(100 * time.Millisecond)
		}
		if !gone(pid) {
			syscall.Kill(pid, syscall.SIGKILL)
			time.Sleep(200 * time.Millisecond)
		}
	}
	if e.pid() != 0 {
		e.stop(context.Background(), nil)
	}
}

func (e *Env) serviceStatus(ctx context.Context) (contract.StatusResponse, error) {
	var s contract.StatusResponse
	c, err := e.client()
	if err != nil {
		return s, err
	}
	return s, c.get(ctx, "/status", &s)
}

func (e *Env) runningExecutions(ctx context.Context) (int, error) {
	s, err := e.serviceStatus(ctx)
	return s.Counts.Running, err
}

// expectVersion fails when the service that answers is not the version just installed (for example an old one that was never stopped).
func (e *Env) expectVersion(ctx context.Context, want string) error {
	s, err := e.serviceStatus(ctx)
	if err != nil {
		return fmt.Errorf("ask the service for its version: %w", err)
	}
	if s.ServerVersion != want {
		return fmt.Errorf("the service that answers reports version %s, not %s", s.ServerVersion, want)
	}
	return nil
}

func (e *Env) download(ctx context.Context, version, tmp, name string) error {
	base := updateBase + "/latest/download"
	if version != "" {
		base = updateBase + "/download/v" + version
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" {
		return fmt.Errorf("release address %q is not https. Nothing was changed", base)
	}
	fmt.Fprintf(e.Out, "Downloading %s ...\n", name)
	if err := fetch(ctx, base+"/SHA256SUMS", filepath.Join(tmp, "SHA256SUMS"), maxSumsBytes); err != nil {
		return err
	}
	return fetch(ctx, base+"/"+name, filepath.Join(tmp, name), maxBinaryBytes)
}

func fetch(ctx context.Context, from, to string, max int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, from, nil)
	if err != nil {
		return fmt.Errorf("download %s: %w", from, err)
	}
	res, err := updateClient().Do(req)
	if err != nil {
		return fmt.Errorf("could not download %s (%w). Nothing was changed. Check the network and, with --version, that the release exists", from, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("could not download %s: %s. Nothing was changed. With --version, check that the release exists", from, res.Status)
	}
	if err := writeCapped(to, res.Body, max); err != nil {
		return fmt.Errorf("download %s: %w. Nothing was changed", from, err)
	}
	return nil
}

// writeCapped writes at most max bytes of r to a new file, flushes it to disk and refuses a longer body.
func writeCapped(to string, r io.Reader, max int64) error {
	f, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, max+1))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > max {
		return fmt.Errorf("more than %d bytes: refused", max)
	}
	return nil
}

func copyFrom(folder, tmp, name string) error {
	for _, f := range []struct {
		name string
		max  int64
	}{{name, maxBinaryBytes}, {"SHA256SUMS", maxSumsBytes}} {
		src := filepath.Join(folder, f.name)
		// Opened without blocking and checked on the descriptor: a pipe or device put there would otherwise hang or be read.
		in, err := os.OpenFile(src, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return fmt.Errorf("%s not found: the folder must hold %s and SHA256SUMS. Nothing was changed", src, name)
		}
		st, err := in.Stat()
		if err != nil || !st.Mode().IsRegular() {
			in.Close()
			return fmt.Errorf("%s is not a regular file. Nothing was changed", src)
		}
		err = writeCapped(filepath.Join(tmp, f.name), in, f.max)
		in.Close()
		if err != nil {
			return fmt.Errorf("read %s: %w. Nothing was changed", src, err)
		}
	}
	return nil
}

// verifySum compares the SHA256 of file with the line of sums that names it.
func verifySum(sums, file, name string) error {
	f, err := os.Open(sums)
	if err != nil {
		return fmt.Errorf("read SHA256SUMS: %w", err)
	}
	defer f.Close()
	want := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && len(fields[0]) == 64 {
			sum := strings.ToLower(fields[0])
			if want != "" && want != sum {
				return fmt.Errorf("SHA256SUMS names %s twice with different checksums. Nothing was changed. Download it again; with --from, check that both files come from the same release", name)
			}
			want = sum
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read SHA256SUMS: %w", err)
	}
	if want == "" {
		return fmt.Errorf("SHA256SUMS has no entry for %s. Nothing was changed. Download it again; with --from, check that both files come from the same release", name)
	}
	g, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("read the downloaded binary: %w", err)
	}
	defer g.Close()
	h := sha256.New()
	if _, err := io.Copy(h, g); err != nil {
		return fmt.Errorf("read the downloaded binary: %w", err)
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return fmt.Errorf("checksum mismatch for %s: the file is not the one the release published. Nothing was changed. Download it again; with --from, check that both files come from the same release", name)
	}
	return nil
}

// reportedVersion runs the candidate's own `version` command, after its checksum matched.
func reportedVersion(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "version")
	// A bare environment, but a HOME: the binary resolves its folders before it reads the command. Nothing is created by `version`.
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + filepath.Dir(bin)}
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(out))
	if len(f) != 2 || f[0] != "waves" || !versionWord.MatchString(f[1]) {
		return "", fmt.Errorf("unexpected output %q", strings.TrimSpace(string(out)))
	}
	return f[1], nil
}

// newer reports whether a is a later release than b. Numbers are compared part by part; a version with a suffix (1.0.1-dev)
// is earlier than the same numbers without one; anything that is not numbers counts as the oldest.
func newer(a, b string) bool {
	an, as := splitVersion(a)
	bn, bs := splitVersion(b)
	for i := 0; i < 3; i++ {
		if an[i] != bn[i] {
			return an[i] > bn[i]
		}
	}
	return as == "" && bs != ""
}

func splitVersion(v string) ([3]int, string) {
	var n [3]int
	suffix := ""
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v, suffix = v[:i], v[i:]
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		x, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, suffix
		}
		n[i] = x
	}
	return n, suffix
}
