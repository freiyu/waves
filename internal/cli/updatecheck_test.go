package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"waves/internal/config"
)

// latestServer answers /latest the way GitHub does: a redirect to the tag page of the newest release.
func latestServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	oldBase, oldClient := updateBase, updateClient
	updateBase = srv.URL
	updateClient = func() *http.Client { return srv.Client() }
	t.Cleanup(func() { updateBase, updateClient = oldBase, oldClient })
}

func redirectTo(tag string) http.HandlerFunc {
	return func(w http.ResponseWriter, q *http.Request) {
		if q.URL.Path != "/latest" || q.Method != http.MethodHead {
			http.NotFound(w, q)
			return
		}
		http.Redirect(w, q, "https://"+q.Host+"/tag/"+tag, http.StatusFound)
	}
}

func checkOnce(e *Env) string {
	return <-e.checkForUpdate(context.Background())
}

func TestStartAnnouncesNewerRelease(t *testing.T) {
	latestServer(t, redirectTo("v1.0.2"))
	e, _, out := updateEnv(t, "1.0.1")
	config.Save(e.Dirs, config.File{Port: 7317, UpdateCheck: true})
	if v := checkOnce(e); v != "1.0.2" {
		t.Fatalf("newer release not found: %q", v)
	}
	e.announceUpdate("1.0.2")
	if got := out.String(); got != "Version 1.0.2 is online, update waves for better performance using 'waves update'\n" {
		t.Fatalf("message: %q", got)
	}
}

func TestUpdateCheckStaysSilent(t *testing.T) {
	cases := []struct {
		name, version string
		h             http.HandlerFunc
		optIn         bool
	}{
		{"same release", "1.0.1", redirectTo("v1.0.1"), true},
		{"running newer than published", "1.0.2", redirectTo("v1.0.1"), true},
		{"dev build", "dev", redirectTo("v9.0.0"), true},
		{"pre-release build", "1.0.1-dev", redirectTo("v9.0.0"), true},
		{"not opted in (the default)", "1.0.1", redirectTo("v9.0.0"), false},
		{"no release yet", "1.0.1", http.NotFound, true},
		{"redirect to something else", "1.0.1", func(w http.ResponseWriter, q *http.Request) {
			http.Redirect(w, q, "https://"+q.Host+"/releases", http.StatusFound)
		}, true},
		{"redirect to another host", "1.0.1", func(w http.ResponseWriter, q *http.Request) {
			http.Redirect(w, q, "https://github.example/tag/v9.0.0", http.StatusFound)
		}, true},
		{"redirect to another repository path", "1.0.1", func(w http.ResponseWriter, q *http.Request) {
			http.Redirect(w, q, "https://"+q.Host+"/x/tag/v9.0.0", http.StatusFound)
		}, true},
		{"Location on a 200", "1.0.1", func(w http.ResponseWriter, q *http.Request) {
			w.Header().Set("Location", "https://"+q.Host+"/tag/v9.0.0")
		}, true},
		{"pre-release tag from the network", "1.0.1", redirectTo("v9.0.0-run-evil.sh-now"), true},
		{"four parts", "1.0.1", redirectTo("v9.0.0.1"), true},
		{"very long number", "1.0.1", redirectTo("v9.0." + strings.Repeat("9", 5000)), true},
		{"plain http redirect", "1.0.1", func(w http.ResponseWriter, q *http.Request) {
			http.Redirect(w, q, "http://"+q.Host+"/tag/v9.0.0", http.StatusFound)
		}, true},
		{"odd release number", "1.0.1", redirectTo("v9.0.0%0Ahello world"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			latestServer(t, c.h)
			e, _, _ := updateEnv(t, c.version)
			config.Save(e.Dirs, config.File{Port: 7317, UpdateCheck: c.optIn})
			if v := checkOnce(e); v != "" {
				t.Fatalf("announced %q", v)
			}
		})
	}
}

func TestUpdateCheckNeverHangsStart(t *testing.T) {
	release := make(chan struct{})
	latestServer(t, func(w http.ResponseWriter, q *http.Request) { <-release })
	defer close(release)
	e, _, _ := updateEnv(t, "1.0.1")
	config.Save(e.Dirs, config.File{Port: 7317, UpdateCheck: true})
	begin := time.Now()
	if v := checkOnce(e); v != "" {
		t.Fatalf("announced %q", v)
	}
	if d := time.Since(begin); d > updateCheckWait+time.Second {
		t.Fatalf("the check held the command for %v", d)
	}
}

func TestConfigKeepsOldShape(t *testing.T) {
	d := config.Dirs{Config: t.TempDir()}
	if err := config.Save(d, config.File{Port: 7400}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(d.File())
	if strings.Contains(string(b), "update") {
		t.Fatalf("a default config must stay readable by 1.0.0: %s", b)
	}
	if err := config.Save(d, config.File{Port: 7400, UpdateCheck: true}); err != nil {
		t.Fatal(err)
	}
	if f, err := config.Load(d); err != nil || !f.UpdateCheck {
		t.Fatalf("opt-in not read back: %v %v", f, err)
	}
}

// The real address has a repository path in front of /latest; the fake keeps one so the prefix logic is exercised.
func repoServer(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	oldBase, oldClient := updateBase, updateClient
	updateBase = srv.URL + "/o/r/releases"
	updateClient = func() *http.Client { return srv.Client() }
	t.Cleanup(func() { updateBase, updateClient = oldBase, oldClient })
}

func TestLatestReleaseUnderARepositoryPath(t *testing.T) {
	repoServer(t, func(w http.ResponseWriter, q *http.Request) {
		if q.URL.Path != "/o/r/releases/latest" {
			http.NotFound(w, q)
			return
		}
		http.Redirect(w, q, "https://"+q.Host+"/o/r/releases/tag/v1.2.3", http.StatusFound)
	})
	if v, err := latestRelease(context.Background()); err != nil || v != "1.2.3" {
		t.Fatalf("got %q, %v", v, err)
	}
}

func TestLatestReleaseRejects(t *testing.T) {
	to := func(loc string) http.HandlerFunc {
		return func(w http.ResponseWriter, q *http.Request) {
			w.Header().Set("Location", strings.ReplaceAll(loc, "HOST", q.Host))
			w.WriteHeader(http.StatusFound)
		}
	}
	for name, h := range map[string]http.HandlerFunc{
		"other repository path":     to("https://HOST/o/x/releases/tag/v9.0.0"),
		"slash encoded in the repo": to("https://HOST/o%2Fr/releases/tag/v9.0.0"),
		"userinfo":                  to("https://someone@HOST/o/r/releases/tag/v9.0.0"),
		"other host":                to("https://github.example/o/r/releases/tag/v9.0.0"),
		"dot segments":              to("https://HOST/o/r/releases/x/../../../../tag/v9.0.0"),
		"encoded dots":              to("https://HOST/o/r/releases/tag/v%2e%2e"),
		"trailing newline":          to("https://HOST/o/r/releases/tag/v9.0.0%0A"),
		"leading zeros too long":    to("https://HOST/o/r/releases/tag/v00009.0.0"),
		"no Location":               func(w http.ResponseWriter, q *http.Request) { w.WriteHeader(http.StatusFound) },
		"Location on a 200": func(w http.ResponseWriter, q *http.Request) {
			w.Header().Set("Location", "https://"+q.Host+"/o/r/releases/tag/v9.0.0")
		},
	} {
		t.Run(name, func(t *testing.T) {
			repoServer(t, h)
			if v, err := latestRelease(context.Background()); err == nil {
				t.Fatalf("accepted %q", v)
			}
		})
	}
}

func TestNoRequestUnlessOptedIn(t *testing.T) {
	var hits atomic.Int32
	latestServer(t, func(w http.ResponseWriter, q *http.Request) { hits.Add(1); redirectTo("v9.0.0")(w, q) })
	e, _, _ := updateEnv(t, "1.0.1") // config.json has no update_check key
	if v := checkOnce(e); v != "" || hits.Load() != 0 {
		t.Fatalf("announced %q with %d requests to the server", v, hits.Load())
	}
}
