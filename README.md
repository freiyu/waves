# Waves

Waves is a local orchestrator for coding agents. Claude Code plans and does the work; Waves keeps the plan,
hands out one task at a time, and **nothing moves to the next phase until you approve it with your passphrase**.
Everything runs on your machine: one binary, one SQLite database, a dashboard on `127.0.0.1`.

## Requirements

- Linux or WSL2 (Ubuntu) on amd64 or arm64, `git`
- Claude Code (`claude` on your PATH)
- To build from source only: Go 1.27 or newer, Node.js 20.19+ or 22.12+ with npm (Node is only needed to build
  the dashboard, never to run Waves)

## Install

From a release (no build tools needed):

```bash
curl -fsSL https://github.com/freiyu/waves/releases/latest/download/install.sh | sh
waves version
```

The script picks the binary for your CPU, checks it against `SHA256SUMS` and installs it to `~/.local/bin/waves`.
It checks the download against `SHA256SUMS` from the same release, so it catches a damaged file, not a compromised release. It uses no sudo and changes nothing else. If you would rather read it first, download `install.sh`, read it, then
run `sh install.sh`. Options: `--version 1.0.0`, `--dir <folder>`, `--from <folder with the downloaded files>`.

From source:

```bash
make build                     # installs the pinned web packages, builds the dashboard and the static binary bin/waves
                               # (use make, not a bare `go build`: the binary embeds the built dashboard)
install -m 755 bin/waves ~/.local/bin/waves
waves help                     # lists every command
```

## First run

```bash
waves setup     # creates config, database and tokens; asks for your approval passphrase
waves start     # runs the service in the background (http://127.0.0.1:7317)
waves open      # prints a one-time sign-in link for the dashboard (valid 60 s)
waves doctor    # optional: checks folders, tokens, git, claude and, with the service running, the audit chain
```

After a reboot the service is not running: run `waves start` again.

Choose a passphrase you do not store on this machine. Every approval asks for it, and agents can read files
your user can read.

## Your first workflow

Register a project, add a requirement, ask for a plan, connect Claude Code, let it plan, then approve the plan,
each phase and each result with your passphrase. The steps are in the [quick start](manual/quick-start.md).
You can also decide from a terminal: `waves approval list`, then `waves approval review <id>`.

## Day to day

| Command | What it does |
|---|---|
| `waves status` | what needs you now |
| `waves task list` | tasks of the active project |
| `waves execution list` / `waves execution logs <id> -f` | running and past agent work |
| `waves approval list` | approvals waiting for you |
| `waves guard enforce` | deny Claude Code's file-editing tools (Write, Edit, MultiEdit, NotebookEdit) without a lease, outside the task's workspace, or on protected files. The default policy `audit` only records. **Shell commands are not inspected while a task is leased.** Sessions Waves never saw are allowed unless the project's unmanaged-sessions setting is `deny`. Changing the policy asks for your passphrase |
| `waves backup` | copy the database (`waves restore <file>` with the service stopped) |
| `waves stop` | stop the service |

## When something goes wrong

- **Needs attention** on the dashboard Overview lists everything waiting for you: approvals, blocked tasks,
  failed or interrupted executions.
- If an agent disconnects or Waves crashes, **nothing is rerun automatically**. Open the execution's
  **Recovery** page and choose: Retry the task, Accept partial result, or Mark failed.
  From a terminal: `waves execution cancel <id>`, then `waves task retry <task id> "<reason>"`.
- Forgot the passphrase: stop the service and run `waves auth passphrase --reset` in a terminal.

## What Waves guarantees, and what it does not

- Phase approval is enforced by the Waves service: agents, the MCP server and the operator token cannot
  approve anything; only a passphrase step-up can. Refused attempts are recorded in a tamper-evident audit log.
- The guard hook lowers the chance of unleased edits by Claude Code's file-editing tools. It is **not a sandbox**:
  while a task is leased, shell commands are not inspected, so an agent can change files through the shell.
- **Not defended:** a process running as your user with unrestricted file or `ptrace` access can tamper with
  Waves directly (edit its database, read process memory, log keystrokes), and anyone who can run `waves` in
  a terminal as your user can reset the passphrase without the old one (that is recorded as `local_offline` and
  stays under Needs attention until you acknowledge it). The audit chain detects some of this; Waves does not
  prevent it. For stronger isolation, run agents under a separate OS account or container.

## More

The `manual/` folder goes deeper:
[quick start](manual/quick-start.md) ·
[connecting Claude Code](manual/claude-code.md) ·
[for AI agents](manual/for-agents.md) ·
[security model](manual/security.md) ·
[backup, restore and upgrade](manual/backup-restore.md) ·
[troubleshooting](manual/troubleshooting.md)

## License

MIT, see [LICENSE](LICENSE).
