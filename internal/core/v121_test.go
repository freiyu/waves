package core

import (
	"testing"
	"time"
)

// The dashboard draws every phase of the shown workflow version as a chain of its tasks.
func TestProjectDetailListsTasksOfEveryPhase(t *testing.T) {
	a := startedFlow(t)
	d, err := a.s.ProjectDetail(a.ctx, a.project)
	if err != nil {
		t.Fatal(err)
	}
	want := a.count(t, `SELECT count(*) FROM tasks t JOIN phases ph ON ph.id = t.phase_id WHERE ph.workflow_version_id = ? AND t.parent_task_id IS NULL`, a.version)
	if len(d.PhaseTasks) != want || want <= len(d.Tasks) {
		t.Fatalf("phase_tasks %d, tasks of the active phase %d, tasks of the version %d", len(d.PhaseTasks), len(d.Tasks), want)
	}
	of := map[string]bool{}
	for _, p := range d.Phases {
		of[p.ID] = true
	}
	for _, x := range d.PhaseTasks {
		if !of[x.PhaseID] || x.State == "" || x.Code == "" {
			t.Fatalf("task without a phase of this version: %+v", x)
		}
	}
}

// An execution that stopped without finishing has an end: the moment it stopped. Its duration does not keep growing.
func TestStoppedExecutionHasAnEnd(t *testing.T) {
	a := startedFlow(t)
	r, err := a.s.ClaimTask(a.ctx, a.agent, "s1", a.task(t, "p1-t1"), "")
	if err != nil {
		t.Fatal(err)
	}
	a.clock.Add(time.Minute)
	read := func() (string, *time.Time, int) {
		d, err := a.s.ExecutionDetail(a.ctx, r.ExecutionID)
		if err != nil {
			t.Fatal(err)
		}
		return d.State, d.EndedAt, *d.DurationS
	}
	if st, end, _ := read(); st != "running" || end != nil {
		t.Fatalf("running execution: %s ended %v", st, end)
	}
	if err := a.s.tx(a.ctx, func(tx *Tx) error {
		_, err := a.s.transition(a.ctx, tx, "execution", r.ExecutionID, "Interrupted", sysActor, "lease expired", "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	stopped := a.clock.Now()
	a.clock.Add(time.Hour)
	st, end, dur := read()
	if st != "interrupted" || end == nil || !end.Equal(stopped) || dur != 60 {
		t.Fatalf("%s ended %v duration %d s, want interrupted at %v after 60 s", st, end, dur, stopped)
	}
}
