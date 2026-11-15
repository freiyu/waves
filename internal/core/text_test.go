package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	c1csi = string(rune(0x9b))
	rlo   = string(rune(0x202e))
	lri   = string(rune(0x2066))
	rlm   = string(rune(0x200f))
	zwsp  = string(rune(0x200b))
	bom   = string(rune(0xfeff))
	lsep  = string(rune(0x2028))
)

func TestUnsafeRuneAndVisible(t *testing.T) {
	ok := []string{"go test ./...", "line one\nline two\ttabbed", "naïve café 日本語", "emoji 🙂", ""}
	for _, s := range ok {
		if r, bad := UnsafeRune(s); bad || Visible(s) != s {
			t.Errorf("%q flagged as unsafe (%U)", s, r)
		}
	}
	bad := map[string]string{
		"escape":         "rm -rf x\x1b[2K\rok",
		"carriage":       "safe\rrm -rf /",
		"bell":           "a\x07b",
		"delete":         "a\x7fb",
		"c1 csi":         "a\u009bb",
		"bidi override":  "ok " + rlo + " txt.exe",
		"bidi isolate":   "a" + lri + "b",
		"direction mark": "a" + rlm + "b",
		"zero width":     "a" + zwsp + "b",
		"bom":            "a" + bom + "b",
		"line separator": "a" + lsep + "b",
	}
	for name, s := range bad {
		if _, isBad := UnsafeRune(s); !isBad {
			t.Errorf("%s was not flagged", name)
		}
		if v := Visible(s); strings.ContainsAny(v, "\x1b\r\x07\x7f\u009b"+rlo+""+lri+""+rlm+""+zwsp+""+bom+""+lsep+"") || !strings.Contains(v, `\u{`) {
			t.Errorf("%s is not spelled out: %q", name, v)
		}
	}
	if Visible("a\x1bb") != `a\u{001B}b` {
		t.Errorf("spelling: %q", Visible("a\x1bb"))
	}
}

// What an agent writes for a human to decide on must not be able to hide or rewrite itself.
func TestAgentTextWithHidingCharactersIsRefused(t *testing.T) {
	for name, cmd := range map[string]string{"escape": "go test ./...\\u001b[2K\\rtrue", "bidi": "echo \\u202etrue"} {
		doc := strings.Replace(goodProposal, `"command": "go test ./..."`, `"command": "`+cmd+`"`, 1)
		if _, errs := ValidateProposal([]byte(doc)); len(errs) == 0 {
			t.Errorf("a validator command with %s characters was accepted", name)
		}
	}
	doc := strings.Replace(goodProposal, `"title": "Build"`, `"title": "Bu`+zwsp+`ild"`, 1)
	if _, errs := ValidateProposal([]byte(doc)); len(errs) == 0 {
		t.Error("a zero-width character in a title was accepted")
	}
	if _, errs := ValidateProposal([]byte(goodProposal)); len(errs) != 0 {
		t.Fatalf("the good proposal is refused: %v", errs)
	}

	a, r, _ := leasedSession(t)
	if _, err := a.s.RequestRiskyApproval(a.ctx, a.agent, "s1", r.LeaseToken, "rm -rf build\r# harmless", "clean", "deletes build output"); code(err) != "validation_failed" {
		t.Errorf("risky action with CR: %v", err)
	}
	if _, err := a.s.RequestRiskyApproval(a.ctx, a.agent, "s1", r.LeaseToken, "rm -rf build", "clean", "safe "+rlo+""); code(err) != "validation_failed" {
		t.Errorf("risky impact with a bidi override: %v", err)
	}
	if err := a.s.ReportBlocker(a.ctx, a.agent, "s1", r.LeaseToken, "stuck\x1b[31m"); code(err) != "validation_failed" {
		t.Errorf("blocker with ESC: %v", err)
	}
	if err := a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "done"+zwsp+"", "completed", nil); code(err) != "validation_failed" {
		t.Errorf("summary with a zero-width character: %v", err)
	}
	if a.count(t, `SELECT count(*) FROM approvals WHERE category IN ('risky_action','result')`) != 0 {
		t.Error("a refused text still produced an approval")
	}
	// Plain text, with a newline, is fine.
	if _, err := a.s.RequestRiskyApproval(a.ctx, a.agent, "s1", r.LeaseToken, "rm -rf build", "clean\nup", "deletes build output"); err != nil {
		t.Errorf("plain risky action: %v", err)
	}
}

// Claude Code may expand a leading ~ to the home folder, so the guard must not read it as inside the workspace.
func TestGuardDeniesATildeTarget(t *testing.T) {
	a := startedFlow(t)
	if err := a.s.SetProjectGuard(a.ctx, a.p, mustStepUp(t, a.env, a.p), a.project, "enforce", "deny"); err != nil {
		t.Fatal(err)
	}
	r, err := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t1"), "tu_t")
	if err != nil {
		t.Fatal(err)
	}
	_ = r
	a.hook(t, "cc", "mcp__waves__waves_claim_task", "tu_t", nil)
	for _, target := range []string{"~/.bashrc", "~root/x", "~"} {
		if d := a.hook(t, "cc", "Write", "t", map[string]any{"file_path": target}); d.Decision != "deny" || d.Reason != "outside_workspace" {
			t.Errorf("%q: %+v", target, d)
		}
	}
	if d := a.hook(t, "cc", "Write", "t", map[string]any{"file_path": filepath.Join(a.root, "ok.go")}); d.Decision != "allow" {
		t.Errorf("an ordinary write: %+v", d)
	}
}

// The web client keeps the same set (web/src/lib/text.ts) and tests it against the same list of examples.
func TestUnsafeRuneMatchesTheSharedList(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "lib", "unsafe.json"))
	if err != nil {
		t.Skip("the web sources are not here: " + err.Error())
	}
	var f struct{ Unsafe, Safe []rune }
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Unsafe) < 20 || len(f.Safe) < 5 {
		t.Fatalf("the shared list is too short: %d and %d", len(f.Unsafe), len(f.Safe))
	}
	for _, r := range f.Unsafe {
		if _, bad := UnsafeRune(string(r)); !bad {
			t.Errorf("U+%04X is on the shared list but not flagged", r)
		}
	}
	for _, r := range f.Safe {
		if _, bad := UnsafeRune(string(r)); bad {
			t.Errorf("U+%04X is safe on the shared list but flagged", r)
		}
	}
}
