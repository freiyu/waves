package core

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// A validator left behind by a crashed service (and its children) is found
// by its run marker and stopped; other processes are left alone.
func TestStopLeftoversKillsOnlyMarkedRuns(t *testing.T) {
	run := NewID("vrun")
	marked := exec.Command("/bin/sh", "-c", "sleep 30 & sleep 30; wait")
	marked.Env = append(os.Environ(), runMarker+run)
	marked.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	other := exec.Command("/bin/sh", "-c", "sleep 30")
	other.Env = append(os.Environ(), runMarker+NewID("vrun"))
	for _, c := range []*exec.Cmd{marked, other} {
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
	}
	defer syscall.Kill(-marked.Process.Pid, syscall.SIGKILL)
	defer other.Process.Kill()
	time.Sleep(100 * time.Millisecond) // let the shell fork its children
	if n := stopLeftovers([]string{run}); n < 3 {
		t.Fatalf("stopped %d processes, want the shell and its two children", n)
	}
	done := make(chan error, 1)
	go func() { done <- marked.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the marked run is still alive")
	}
	if err := other.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("an unrelated run was stopped: %v", err)
	}
	if syscall.Kill(-marked.Process.Pid, 0) == nil {
		// The group may still hold zombies for a moment; none may be running.
		time.Sleep(100 * time.Millisecond)
		if syscall.Kill(-marked.Process.Pid, 0) == nil {
			t.Fatal("a child of the marked run survived")
		}
	}
}
