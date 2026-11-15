package cli

// `waves start` tells the user when a newer release exists. It asks GitHub once, while the command runs, with a short deadline that
// overlaps the service start, so it never delays the command by more than updateCheckWait and a failure is silent. Nothing is
// downloaded or installed; nothing checks in the background. It is off until the user sets {"update_check": true} in config.json.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"waves/internal/config"
)

const updateCheckWait = 2 * time.Second

var plainRelease = regexp.MustCompile(`^[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}$`)

// latestRelease reads the newest release number from where /releases/latest redirects (…/releases/tag/v1.0.2). The request carries
// only what any HTTPS request carries (the address) and a generic User-Agent.
func latestRelease(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, updateBase+"/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "waves")
	c := updateClient()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := c.Do(req)
	if err != nil {
		return "", err
	}
	res.Body.Close()
	if res.StatusCode/100 != 3 {
		return "", errors.New("no release")
	}
	loc, err := res.Location()
	if err != nil {
		return "", errors.New("no release")
	}
	// The answer must stay on the release page of this repository, and only a plain release number (1.2.3) is ever printed.
	base, err := url.Parse(updateBase)
	if err != nil {
		return "", err
	}
	prefix := base.EscapedPath() + "/tag/v"
	if loc.Scheme != "https" || loc.User != nil || loc.Host != base.Host || !strings.HasPrefix(loc.EscapedPath(), prefix) {
		return "", errors.New("unexpected release address")
	}
	v := strings.TrimPrefix(loc.EscapedPath(), prefix)
	if !plainRelease.MatchString(v) {
		return "", errors.New("unexpected release number")
	}
	return v, nil
}

// checkForUpdate starts the check and returns a channel that yields the newer version, or nothing. A development build or a
// pre-release never announces anything.
func (e *Env) checkForUpdate(ctx context.Context) <-chan string {
	out := make(chan string, 1)
	cfg, err := config.Load(e.Dirs)
	n, suffix := splitVersion(Version)
	if err != nil || !cfg.UpdateCheck || n == [3]int{} || suffix != "" {
		close(out)
		return out
	}
	go func() {
		defer close(out)
		ctx, cancel := context.WithTimeout(ctx, updateCheckWait)
		defer cancel()
		if v, err := latestRelease(ctx); err == nil && newer(v, Version) {
			out <- v
		}
	}()
	return out
}

func (e *Env) announceUpdate(v string) {
	if v != "" {
		fmt.Fprintf(e.Out, "Version %s is online, update waves for better performance using 'waves update'\n", v)
	}
}
