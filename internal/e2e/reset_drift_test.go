//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"waves/internal/api/contract"
)

const resetPass = "reset horse battery staple" // guard:allow fake test fixture

// An offline reset needs no old passphrase. Through the real binary: it is recorded as local_offline,
// it stays on the attention list, the old passphrase stops working, and no non-human credential can clear it.
func TestPassphraseResetThroughTheBinary(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	i.stop()
	if out, err := i.tty([]string{"auth", "passphrase", "--reset"}, "reset\n"+resetPass+"\n"+resetPass+"\n"); err != nil {
		t.Fatalf("reset: %v\n%s", err, out)
	}
	if n := i.count(`SELECT count(*) FROM audit WHERE event = 'passphrase.reset' AND actor_scope = 'local_offline'`); n != 1 {
		t.Fatalf("reset records with scope local_offline: %d", n)
	}
	if n := i.count(`SELECT count(*) FROM audit WHERE event = 'passphrase.reset' AND actor_scope = 'human'`); n != 0 {
		t.Fatalf("a reset was recorded as a human action")
	}
	i.start()
	listed := func() bool {
		var st contract.StatusResponse
		i.operator().must("GET", "/status", nil, &st)
		for _, a := range st.NeedsAttention {
			if a.Kind == "passphrase_reset" {
				return true
			}
		}
		return false
	}
	if !listed() {
		t.Fatal("the reset is not on the attention list after a restart")
	}
	dash := i.dashboard()
	if st, _ := dash.call("POST", "/auth/stepup", map[string]string{"passphrase": testPass}, nil, nil); st != 403 {
		t.Fatalf("the old passphrase was accepted after a reset: %d", st)
	}
	time.Sleep(1200 * time.Millisecond) // a wrong passphrase delays the next try by one second
	var r contract.StepUpResponse
	dash.must("POST", "/auth/stepup", map[string]string{"passphrase": resetPass}, &r)
	// Neither the operator token nor the guard token can acknowledge, and a refused try changes nothing.
	for name, c := range map[string]*client{"operator": i.operator(), "guard": i.as(i.guard)} {
		if st, _ := c.call("POST", "/audit/acknowledge", nil, nil, elev(r.ElevationSecret)); st < 400 {
			t.Errorf("the %s token acknowledged the reset: %d", name, st)
		}
	}
	if !listed() {
		t.Fatal("a refused acknowledgement cleared the alert")
	}
	dash.must("POST", "/audit/acknowledge", nil, nil, elev(r.ElevationSecret))
	if listed() {
		t.Fatal("the alert stays after a human acknowledged it")
	}
}

// The guard is only active while the hook is in the project. Through the real binary: tampering shows in the
// guard view, in `waves guard status` and in `waves doctor`, and setting the policy again repairs it.
func TestHookDriftThroughTheBinary(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	withFakeClaude(t, i)
	p := i.newProject("drift")
	var g contract.GuardInfo
	p.dash.must("GET", "/projects/"+p.id+"/guard", nil, &g)
	if !g.Installed || g.Drift != "" {
		t.Fatalf("fresh connection: %+v", g)
	}
	settings := filepath.Join(p.dir, ".claude", "settings.local.json")
	good, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	// What an agent with a shell could do while it holds a lease: drop the hook.
	if err := os.WriteFile(settings, []byte(`{"enabledMcpjsonServers":["waves"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	g = contract.GuardInfo{} // decoding into a used struct would keep the old drift text
	p.dash.must("GET", "/projects/"+p.id+"/guard", nil, &g)
	if g.Installed || !strings.Contains(g.Drift, "missing") {
		t.Fatalf("a removed hook is not reported: %+v", g)
	}
	out, code := i.run("", "doctor")
	if code != 1 || !strings.Contains(out, "FAIL  hook drift") {
		t.Errorf("doctor: exit %d\n%s", code, out)
	}
	out, _ = i.run("", "guard", "status")
	if !strings.Contains(out, "hook installed no") || !strings.Contains(out, "The guard is not active") {
		t.Errorf("guard status: %s", out)
	}
	// Repair: the policy set again with the passphrase writes the hook back.
	p.dash.must("PATCH", "/projects/"+p.id+"/settings", map[string]string{"guard_policy": "audit"}, nil, elev(p.dash.stepUp("")))
	g = contract.GuardInfo{}
	p.dash.must("GET", "/projects/"+p.id+"/guard", nil, &g)
	if !g.Installed || g.Drift != "" {
		t.Fatalf("after repair: %+v", g)
	}
	if now, _ := os.ReadFile(settings); string(now) != string(good) {
		t.Errorf("the repaired hook differs from the original:\n%s\nvs\n%s", now, good)
	}
	out, code = i.run("", "doctor")
	if code != 0 || !strings.Contains(out, "ok    hook drift") {
		t.Errorf("doctor after repair: exit %d\n%s", code, out)
	}
}
