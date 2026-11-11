package core

import (
	"errors"
	"os/exec"
	"testing"
)

// The installed enforce command denies (exit 2) when the binary is missing.
func TestEnforceWrapperFailsClosed(t *testing.T) {
	for _, c := range []struct {
		bin, policy string
		want        int
	}{
		{"/nonexistent/waves", "enforce", 2},
		{"/bin/false", "enforce", 2},
		{"/bin/true", "enforce", 0},
		{"/nonexistent/waves", "audit", 127}, // audit never blocks: Claude Code treats any exit but 2 as allow
	} {
		cmd := exec.Command("/bin/sh", "-c", hookCommand(c.bin, c.policy))
		err := cmd.Run()
		got := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			got = ee.ExitCode()
		}
		if got != c.want {
			t.Errorf("%s %s: exit %d, want %d (%s)", c.bin, c.policy, got, c.want, hookCommand(c.bin, c.policy))
		}
	}
}
