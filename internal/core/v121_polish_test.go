package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A stream whose request was authenticated just before the project was disconnected must hold nothing afterwards.
func TestStreamOfARevokedCredentialHoldsNothing(t *testing.T) {
	a := startedFlow(t)
	a.clock.Add(time.Second)
	if _, err := a.s.Disconnect(a.ctx, a.p, mustStepUp(t, a.env, a.p), a.project); err != nil {
		t.Fatal(err)
	}
	if err := a.s.StreamOpen(a.ctx, a.agent, "s1"); code(err) != "wrong_scope" {
		t.Fatalf("a revoked credential opened a stream: %v", err)
	}
	if a.s.SessionConnected(a.project, "s1") {
		t.Fatal("a revoked credential's session counts as connected")
	}
}

// The confirmation of a phase completion names changes outside the plan, so no place that offers it can leave them out.
func TestCompletionApprovalNamesChangesOutsideThePlan(t *testing.T) {
	a := startedFlow(t)
	base := filepath.Join(a.root, "base.txt")
	os.WriteFile(base, []byte("one\n"), 0o600)
	for _, args := range [][]string{{"add", "base.txt"}, {"commit", "-qm", "base"}} {
		if out, err := exec.Command("git", append([]string{"-C", a.root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	for _, k := range []string{"p1-t1", "p1-t2"} {
		r, err := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, k), "")
		if err != nil {
			t.Fatal(err)
		}
		if err := a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "done", "completed", nil); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(base, []byte("changed after the tasks ended\n"), 0o600)
	if err := a.s.RunValidation(a.ctx, a.phase1); err != nil {
		t.Fatal(err)
	}
	d, err := a.s.ApprovalDetail(a.ctx, a.pendingApproval(t, "phase_completion", a.phase1))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range d.Facts {
		if f.Label == "Outside the plan" {
			if !strings.HasPrefix(f.Value, "1 change") {
				t.Fatalf("fact: %q", f.Value)
			}
			return
		}
	}
	t.Fatalf("no fact about the change outside the plan: %+v", d.Facts)
}

// With a git filter configured Waves compares nothing: the confirmation must say that, not count it as one change.
func TestCompletionApprovalSaysWhenTheWorkTreeWasNotCompared(t *testing.T) {
	a := startedFlow(t)
	for _, k := range []string{"p1-t1", "p1-t2"} {
		r, err := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, k), "")
		if err != nil {
			t.Fatal(err)
		}
		if err := a.s.SubmitResult(a.ctx, a.agent, "s1", r.LeaseToken, "done", "completed", nil); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "-C", a.root, "config", "filter.x.clean", "cat").CombinedOutput(); err != nil {
		t.Fatalf("git config: %v %s", err, out)
	}
	if err := a.s.RunValidation(a.ctx, a.phase1); err != nil {
		t.Fatal(err)
	}
	d, err := a.s.ApprovalDetail(a.ctx, a.pendingApproval(t, "phase_completion", a.phase1))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range d.Facts {
		if f.Label == "Outside the plan" {
			if !strings.HasPrefix(f.Value, "The work tree was not compared") {
				t.Fatalf("fact: %q", f.Value)
			}
			return
		}
	}
	t.Fatalf("no fact: %+v", d.Facts)
}
