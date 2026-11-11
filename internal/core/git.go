package core

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// git runs git in an agent-controlled work tree without letting the
// repository's own configuration run programs or rewrite diffs: no system
// or global config, no fsmonitor, no hooks, no external diff or textconv,
// no pager. Diffs Waves stores and shows must be the real changes.
func git(ctx context.Context, root string, args ...string) *exec.Cmd {
	base := []string{"-C", root, "-c", "core.fsmonitor=", "-c", "core.hooksPath=/dev/null", "-c", "diff.external=", "-c", "core.pager=cat", "-c", "core.untrackedCache=false"}
	if len(args) > 0 && args[0] == "diff" {
		args = append([]string{"diff", "--no-ext-diff", "--no-textconv"}, args[1:]...)
	}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	return cmd
}

// gitFilters lists filter drivers the repository configures (clean, smudge,
// process). git would run them while comparing the work tree, so Waves does
// not diff or fingerprint such a work tree; reading the keys runs nothing.
func gitFilters(ctx context.Context, root string) []string {
	out, _ := git(ctx, root, "config", "--includes", "--name-only", "--get-regexp", `^filter\..*\.(clean|smudge|process)$`).Output()
	return strings.Fields(string(out))
}

// untrackedFiles lists the untracked paths git reports, plus the two files a
// Waves connection edits even when an ignore rule hides them (Claude Code
// itself keeps settings.local.json out of git): a change to the hook during a
// task must show in the diff and in the workspace fingerprint.
func untrackedFiles(ctx context.Context, root string) ([]string, error) {
	out, err := git(ctx, root, "ls-files", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		return nil, err
	}
	var list []string
	seen := map[string]bool{}
	for _, l := range strings.Split(string(out), "\x00") {
		if l != "" {
			list = append(list, l)
			seen[l] = true
		}
	}
	if extra, err := git(ctx, root, "ls-files", "--others", "-z", "--", ".claude/settings.local.json", ".mcp.json").Output(); err == nil {
		for _, l := range strings.Split(string(extra), "\x00") {
			if l != "" && !seen[l] {
				list = append(list, l)
			}
		}
	}
	return list, nil
}
