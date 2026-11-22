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

The script picks the binary for your CPU, checks it against `SHA256SUMS` from the same release and installs it to
`~/.local/bin/waves`. That catches a damaged download, not a compromised release. It uses no sudo and changes nothing else. If you would rather read it first, download `install.sh`, read it, then
run `sh install.sh`. Options: `--version 1.2.0`, `--dir <folder>`, `--from <folder with the downloaded files>`.

To update later, run `waves update`. It downloads the newest release, checks it against `SHA256SUMS`, keeps the old
binary as `waves.previous` and restarts a running service. `waves start` can tell you when a newer release exists: it is off until you put `"update_check": true` in `config.json` (one request to GitHub, no background checks). See [backup, restore and upgrade](manual/backup-restore.md).

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

## Your first workflow: a walkthrough for beginners

### The idea in plain words

You do not hand Claude Code a big job and hope. You write down what you want, Claude Code turns it into a plan, **you read
and approve the plan**, and then Claude Code does the work in small steps. Waves sits in the middle: it keeps the plan,
gives Claude Code one small task at a time, and stops at the end of every phase until you look at what was done and
approve it with your passphrase. If you do not approve, nothing moves on.

| Word | What it means |
|---|---|
| **Project** | a folder on your computer that is a git repository |
| **Requirement** | what you want, in your own words (a sentence, or a file) |
| **Workflow** | the plan Claude Code proposes: a list of phases |
| **Phase** | one stage of the plan, for example "build" and then "polish" |
| **Task** | one small piece of work inside a phase. Claude Code does a phase's tasks one after another |
| **Validator** | a command that checks a phase (for example your tests). Waves runs it for you |
| **Approval** | your yes, confirmed with your passphrase. Claude Code can never give it for you |

### Step by step

You need Waves installed and `waves setup` done once (see above). Then:

1. **Start Waves and open the dashboard.** Run `waves start`, then `waves open`, and open the link it prints in a browser on
   the same computer. The link works once and for 60 seconds. If it expired, run `waves open` again.
2. **Register your project.** In a terminal, go to the project folder and run:

   ```bash
   cd ~/work/my-project          # a git repository
   waves project add .
   ```

3. **Write what you want and ask for a plan.** Describe it in one or two sentences, as you would to a colleague:

   ```bash
   waves requirements add --text "Add a /health endpoint that returns 200, with a test"
   waves plan request
   ```

4. **Connect Claude Code to the project.** Run `waves agent connect claude-code`. Waves shows you exactly which two files
   it will write in the project, asks you to type `yes`, and asks for your passphrase. It also stores a private access
   token for this project in `~/.config/waves/mcp/` and changes nothing else outside the project folder.
5. **Let Claude Code write the plan.** In the project folder start `claude` and say:

   > Fetch the Waves planning request and submit a workflow proposal.

   The first time, Claude Code asks you to approve the `waves` server it found in `.mcp.json`: say yes. It may also ask
   before each Waves tool call: allow them. If it offers no Waves tools, see [troubleshooting](manual/troubleshooting.md).
6. **Read the plan and approve it.** In the dashboard open your project, then its **Plan** tab. You see the phases, the
   tasks and the validator commands. **Read the validator commands: Waves will run them on your computer, as soon as the last task of a phase is done and before you review that phase, so they run on what Claude Code just wrote.** If the plan looks wrong, do
   not approve it; ask Claude Code to propose another. If it looks right, press **Submit**, then **Approve**, and type your
   passphrase.
7. **Approve the start of phase 1.** The dashboard shows it under **Needs your decision**. Press the lock icon (approve) and
   type your passphrase. Until you do, Claude Code cannot start.
8. **Tell Claude Code to work.** In `claude` say:

   > Work through the Waves tasks of this project one after another. Claim a task, read its context, do the work, and submit the result. Then take the next one.

   Claude Code now does the tasks of the phase in order. **You are not asked to approve each task.** You can watch on the
   dashboard: the **Executions** page shows what is running. If Claude Code stops and asks you something in its own
   terminal, the dashboard shows **Waiting for instruction**: answer it there, in `claude`.
9. **Review the phase.** When the last task of a phase is done, Waves runs the validators and opens the **Review** tab of the
   project. It lists **every task of the phase with what the agent said it did**, the files that changed, and the result of each
   validator. Look at it. A passing validator is not an approval, and "done" only means the agent said so. If it is good,
   press **Approve** and type your passphrase: the phase is complete and the next phase waits for your approval to start. If it
   is not good, press **Reject** and write what is wrong. Then use **Revise this phase**, pick the tasks to redo, and press
   **Reopen** (passphrase). Tell Claude Code to work through the tasks again (step 8); it gets your note with each reopened task.
10. **Repeat** steps 7 to 9 for each phase (in step 8, tell Claude Code to continue with the next phase). When the last phase is
    approved, the work is finished.

### What you decide, and when

- **Once:** approve the plan (the workflow).
- **At the start of each phase:** approve the start.
- **At the end of each phase:** review and approve the result.
- **Sometimes:** a risky action, when Claude Code asks permission for something like deleting data. Read it carefully.

Everything else Claude Code does by itself, inside the project folder.

### Good to know

- **Changing your mind in the middle.** If you approve a new plan while a task is running, the running work is cancelled and
  is not rerun; Claude Code takes its tasks from the new plan. The approval screen lists what will be cancelled before you
  type your passphrase.
- **Long jobs are fine.** A long build or test run does not make Waves think Claude Code died. Waves watches the
  connection to Claude Code, not a clock. The other side: a Claude Code that hangs but keeps its connection keeps its task,
  its workspace lock and its slot with no time limit, until you press **Release** on its execution.
- **If Claude Code or the computer stops.** Waves never reruns work on its own. Open the **Executions** page: the task is
  shown as **Blocked** and the execution as **Disconnected** or **Interrupted**. Choose **Retry**, **Accept partial result** or
  **Mark failed**. Accepting a partial result needs only the operator token on your machine, not your passphrase; the task
  becomes Done and the phase review marks it. Why it happened is in the **Activity** page.
- **The passphrase protects you.** Choose one that you do not keep on this computer. Claude Code runs as you and can read any
  file you can read, so it must never be able to find the passphrase.
- **You can do all of this from a terminal** instead of the dashboard: `waves status` shows what needs you, `waves approval list`
  lists the approvals, and `waves approval review <id>` lets you approve or reject.

The [quick start](manual/quick-start.md) has the same steps in the manual's words, and the [security model](manual/security.md)
says what Waves enforces and what it does not.

## Day to day

| Command | What it does |
|---|---|
| `waves status` | what needs you now |
| `waves task list` | tasks of the active project |
| `waves execution list` / `waves execution logs <id> -f` | running and past agent work |
| `waves approval list` | approvals waiting for you |
| `waves guard enforce` | deny Claude Code's file-editing tools (Write, Edit, MultiEdit, NotebookEdit) without a lease, outside the task's workspace, or on protected files. The default policy `audit` only records. **Shell commands are not inspected while a task is leased.** Sessions Waves never saw are allowed unless the project's unmanaged-sessions setting is `deny`. Changing the policy asks for your passphrase |
| `waves update` | replace the binary with the newest release (`--version X` for a specific one, `--from <folder>` for files you downloaded). Refuses while executions are running, and an older release, unless you pass `--force` |
| `waves backup` | copy the database (`waves restore <file>` with the service stopped) |
| `waves stop` | stop the service |

## When something goes wrong

- **Needs your decision** on the dashboard Overview lists the approvals waiting for you. **Needs attention** on the
  Executions page lists interrupted, failed and waiting executions, each with its actions. An execution that shows
  **Waiting for instruction** is a Claude Code session that stopped and waits for your answer in its own terminal.
- If an agent disconnects or Waves crashes, **nothing is rerun automatically**. Open the execution on the
  Executions page and choose: Retry the task, Accept partial result, or Mark failed.
  From a terminal: `waves execution cancel <id>`, then `waves task retry <task id> "<reason>"`.
- Forgot the passphrase: stop the service and run `waves auth passphrase --reset` in a terminal.

## What Waves guarantees, and what it does not

- **Validators run before you review.** The validator commands you approve with the workflow run on your machine as soon as the
  last task of a phase is Done, on what the agent just wrote. Read them when you approve the workflow.
- **A connected session has no time limit.** A Claude Code session that stays connected keeps its task, its workspace lock
  and its slot, even if it hangs, until you **Release** it.
- **Partial work needs no passphrase.** Accepting a partial result after an interruption needs only the operator token. The
  task becomes Done and the phase review marks it; completing the phase still needs your passphrase.
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
