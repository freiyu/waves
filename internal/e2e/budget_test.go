//go:build e2e

package e2e

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rss returns VmRSS and VmHWM of a process in bytes.
func rss(pid int) (cur, peak int) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), ":")
		n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(v), " kB"))
		switch k {
		case "VmRSS":
			cur = n * 1024
		case "VmHWM":
			peak = n * 1024
		}
	}
	return cur, peak
}

// cpuTicks returns utime+stime of a process in clock ticks.
func cpuTicks(pid int) int {
	b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	f := strings.Fields(string(b)[strings.LastIndexByte(string(b), 41)+2:])
	u, _ := strconv.Atoi(f[11])
	s, _ := strconv.Atoi(f[12])
	return u + s
}

func pct(ds []time.Duration, p float64) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[int(float64(len(s)-1)*p)]
}

const mb = 1 << 20

// TestBudget measures the resource budget on the release build and fails
// on any target that is missed. Numbers go to the test log.
func TestBudget(t *testing.T) {
	i := newInst(t)
	var rows [][3]string
	check := func(name, got, target string, ok bool) {
		rows = append(rows, [3]string{name, got, target})
		if !ok {
			t.Errorf("%s: %s, target %s", name, got, target)
		}
	}

	st, _ := os.Stat(bin)
	check("Binary size (release flags, UI embedded)", fmt.Sprintf("%.1f MB (%d B)", float64(st.Size())/mb, st.Size()), "≤ 30 MB", st.Size() <= 30*mb)

	// Cold start to ready: exec to the first 200 from /health.
	var starts []time.Duration
	for range 10 {
		i.stop()
		t0 := time.Now()
		i.start()
		starts = append(starts, time.Since(t0))
	}
	med := pct(starts, 0.5)
	check("Cold start to ready (median of 10)", med.Round(100*time.Microsecond).String(), "≤ 1 s", med <= time.Second)

	// Idle: no executions, nothing connected. CPU over 5 s shows there is no polling.
	time.Sleep(2 * time.Second)
	pid := i.srv.Process.Pid
	c0 := cpuTicks(pid)
	time.Sleep(5 * time.Second)
	idleCPU := cpuTicks(pid) - c0
	idle, _ := rss(pid)
	check("Idle memory (RSS)", fmt.Sprintf("%.1f MB", float64(idle)/mb), "≤ 40 MB", idle <= 40*mb)
	check("Idle CPU over 5 s (polling check)", fmt.Sprintf("%d ticks (%d ms)", idleCPU, idleCPU*10), "no polling", idleCPU <= 2)

	// Dashboard initial load: HTML, JS and CSS as the server sends them (gzip).
	page := 0
	get := func(path string) string {
		req, _ := http.NewRequest("GET", i.url(path), nil)
		req.Header.Set("Accept-Encoding", "gzip")
		res, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		page += len(raw)
		if res.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(strings.NewReader(string(raw)))
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(zr)
			return string(b)
		}
		return string(raw)
	}
	html := get("/")
	for _, m := range regexp.MustCompile(`(?:src|href)="(/[^"]+)"`).FindAllStringSubmatch(html, -1) {
		get(m[1])
	}
	check("Dashboard initial load (gzip as served)", fmt.Sprintf("%d B", page), "≤ 200 KB", page <= 200*1024)

	// Five active executions: five projects, each with a leased task and a
	// heartbeat, plus five dashboard event streams.
	i.setLimit(5)
	var hookCmd, session, target string
	var agents []*agent
	for n := range 5 {
		p, a := i.planned(fmt.Sprintf("budget%d", n))
		a.claim()
		agents = append(agents, a)
		if n == 0 {
			hookCmd = p.setPolicy(t, "enforce")
			session = "sess-budget"
			target = filepath.Join(p.dir, "hello.txt")
			i.runHook(hookCmd, map[string]any{"session_id": session, "cwd": p.dir, "hook_event_name": "PreToolUse",
				"tool_name": "mcp__waves__waves_claim_task", "tool_use_id": fmt.Sprintf("toolu_%s_%d", p.id, a.tool), "tool_input": map[string]any{}})
		}
	}
	for range 5 {
		c := i.dashboard()
		req, _ := http.NewRequest("GET", i.url("/api/v1/events"), nil)
		req.Header.Set("Cookie", "waves_session="+c.cookie)
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("event stream: %v %v", err, res)
		}
		defer res.Body.Close()
	}
	for k := range 40 {
		for _, a := range agents {
			a.must("waves_report_progress", map[string]any{"message": fmt.Sprintf("line %d", k)}, nil)
		}
	}
	for _, a := range agents {
		a.must("waves_heartbeat", map[string]any{}, nil)
	}
	if n := i.count(`SELECT count(*) FROM executions WHERE state = 'Running'`); n != 5 {
		t.Fatalf("%d running executions, want 5", n)
	}
	five, peak5 := rss(pid)
	check("Memory with 5 active executions (+5 SSE, 200 progress calls)", fmt.Sprintf("%.1f MB (peak %.1f MB)", float64(five)/mb, float64(peak5)/mb), "≤ 80 MB", five <= 80*mb)

	// Guard hook decision latency, the real path: enforce, a leased session
	// writing in its workspace, through /bin/sh as Claude Code runs it.
	var hooks []time.Duration
	for n := range 300 {
		code, d := i.runHook(hookCmd, writeEvent(session, filepath.Dir(target), target, fmt.Sprintf("toolu_bench_%d", n)))
		if code != 0 {
			t.Fatalf("leased write denied in the benchmark: exit %d", code)
		}
		hooks = append(hooks, d)
	}
	p95 := pct(hooks, 0.95)
	check("Guard hook latency p95 (n=300, enforce, leased write)", fmt.Sprintf("%s (p50 %s, max %s)", p95.Round(100*time.Microsecond), pct(hooks, 0.5).Round(100*time.Microsecond), pct(hooks, 1).Round(100*time.Microsecond)), "≤ 50 ms", p95 <= 50*time.Millisecond)
	// Passphrase step-up (argon2id t=2, m=19 MiB) end to end over HTTP.
	sc := i.dashboard()
	var ups []time.Duration
	for range 10 {
		t0 := time.Now()
		sc.stepUp("")
		ups = append(ups, time.Since(t0))
	}
	check("Step-up over HTTP (median of 10, not a 5.6 target)", pct(ups, 0.5).Round(100*time.Microsecond).String(), "—", true)
	_, peak := rss(pid)
	check("Peak memory of the whole run (VmHWM)", fmt.Sprintf("%.1f MB", float64(peak)/mb), "≤ 80 MB", peak <= 80*mb)

	// Agent heartbeat interval: the service refuses anything under 15 s.
	hc := i.dashboard()
	st14, _ := hc.call("PATCH", "/settings", map[string]int{"heartbeat_interval_s": 14}, nil, nil)
	st15, _ := hc.call("PATCH", "/settings", map[string]int{"heartbeat_interval_s": 15}, nil, nil)
	check("Heartbeat interval floor (PATCH 14 s / 15 s)", fmt.Sprintf("%d / %d", st14, st15), "≥ 15 s", st14 >= 400 && st15 < 300)
	var sb strings.Builder
	sb.WriteString("\n| Metric | Measured | Target |\n|---|---|---|\n")
	for _, r := range rows {
		fmt.Fprintf(&sb, "| %s | %s | %s |\n", r[0], r[1], r[2])
	}
	t.Log(sb.String())
}
