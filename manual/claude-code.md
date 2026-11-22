# Connecting Claude Code

Waves works with Claude Code through two project files. Waves writes these two files inside the project folder, plus a private token for the project in `~/.config/waves/mcp/` (removed again by `waves agent disconnect`). It changes nothing else.

## What `waves agent connect claude-code` writes

Run it inside a registered project. It prints the planned change as a diff, asks for `yes`
and your passphrase, and then writes:

| File | Purpose |
|---|---|
| `.mcp.json` | registers the `waves` MCP server (a stdio bridge, `waves mcp`) so Claude Code can fetch plans, claim tasks and report results |
| `.claude/settings.local.json` | adds the **guard hook**, which asks Waves whether a file-changing tool call is allowed, and three small **signal hooks** (`Stop`, `UserPromptSubmit`, `SessionEnd`) that only tell Waves when Claude Code stops to wait for you, when you reply, and when the session ends |

`waves agent disconnect` removes exactly these entries. `waves agent status` shows whether the project is
connected, and `waves guard status` shows the guard policy. Both files belong to your checkout. Do not hide them from git: Waves shows
changes to them in execution diffs, and an exclude rule would hide a tampered hook from that diff.

The first time you start `claude` in the project it may ask you to approve the `waves` MCP server. Approve it.
Claude Code may also ask permission before each Waves tool call; allow them, or the agent cannot work.

## What the agent can do

Through MCP the agent can read the planning request, submit a workflow proposal, claim a task, read its
context (including your feedback after a rejected phase review), report progress, report a blocker,
ask approval for a risky action, submit a result, and release a task it cannot finish. It **cannot approve anything**: there is no approval
operation in MCP, and the tokens it can reach are not allowed to approve.

A task is claimed with a **lease**. While the lease is active only that session works on that task. The lease lasts as
long as the agent's session stays connected to Waves (the `waves mcp` bridge that Claude Code starts keeps one
connection open to the service), so a long build or test run does not end it, and no timer does either. If the
session disappears (Claude Code or its bridge dies), the execution is shown as **Disconnected** at once and, unless the
session comes back within 30 seconds, it is marked **Interrupted** and the task is **Blocked**. A tool or MCP client that
does not keep such a connection is held by the older heartbeat rule instead: its lease ends after a few intervals (the Settings
heartbeat interval, 30 seconds by default) without a sign of life. **Waves never reruns an interrupted task
by itself**: you open the execution on the Executions page and choose to Retry the task, Accept partial result, or Mark failed.

## The guard hook and its policies

| Policy | Effect |
|---|---|
| `audit` (default) | records what would have been denied; never blocks |
| `enforce` | denies the file-editing tools (Write, Edit, MultiEdit, NotebookEdit) without an active lease, outside the task's workspace, or on protected files |
| `off` | the hook allows everything |

New projects start in `audit`: nothing is blocked until you run `waves guard enforce`. Change the policy with
`waves guard audit|enforce|off`; every change asks for your passphrase. Under `enforce` the hook **fails closed**: if the Waves service
is not running or does not answer, file changes are denied until it is back. Claude Code sessions that Waves has
not seen are allowed by default; the project setting for unmanaged sessions can deny them too.

**While a task is leased, shell commands (Bash) are not inspected.** An agent can change files outside its
workspace through the shell. The hook checks only the file-editing tools; after a lease ends, Bash is denied too.

The hook is a safety net against mistakes and drift. **It is not a sandbox**; see the [security model](security.md).

Waves checks that the hook in your project is still the one it installed. If it was removed, switched off or
edited, `waves doctor` shows `FAIL hook <project>`, `waves guard status` warns, and the guard is **not active**.
Fix the file (remove a `disableAllHooks` setting yourself), then run `waves guard <policy>` (it asks for your
passphrase) to write the hook back. The Guard tab of the project (also on the Harnesses page) shows the same warning. This is a check, not a lock: an
agent with a shell can edit the file during a task, and you will see it only afterwards.

## When Claude Code waits for you

Claude Code does not send anything to Waves while it waits for your answer in its terminal. The connection keeps the task's
lease alive anyway. The signal hooks only let Waves *show* that it is waiting:

- When Claude Code finishes a turn and waits for you, the execution is shown as **Waiting for instruction** on the
  Overview, Executions and Graph pages, with how long it has waited. Its state stays Running.
- A wait does not release anything: the lease, the workspace lock and the concurrency slot stay with the task.
- The wait ends when you reply in Claude Code, when the agent works again (a file-changing or shell tool call that
  reaches the guard hook, or a Waves tool call), or when the session ends.
- **There is no longest wait for a connected session**: a Claude Code window that you leave open keeps its task, its workspace lock
  and its slot until you reply, cancel or close it. If you do not want that, use **Release** on the waiting execution
  (Overview, Executions or Graph): it ends as Interrupted, then choose Retry the task, Accept partial result or Mark
  failed. Waves never reruns the task for you. The **Longest wait for instruction** setting (8 hours by default, 1 to 72,
  Settings, asks for your passphrase) applies only to a session without a connection.
- A file-changing or shell tool call that Waves sees through the guard hook ends a wait, and for a session without a connection it also renews the lease. Read-only tools such as
  Read and Grep do not reach the hook.

Projects connected with Waves 1.0.0 do not have the signal hooks. The Harnesses page says so, `waves doctor` prints a
notice, and connecting again installs them: run `waves agent connect claude-code` again, or use **Connect again** on the
Harnesses page. Both ask for your passphrase. The guard policy stays as it was.

## Things worth knowing

- **Observed** with Claude Code 2.1.x in headless mode (`claude -p`): MCP pickup from `.mcp.json`, the hook on
  every call, binding of a session to its lease, and fail-closed behavior when the service is down.
- **Observed** with Claude Code 2.1.325 in an interactive session: MCP pickup, the guard hook on the Waves tool calls,
  binding of the session to its lease, and the `Stop`, `UserPromptSubmit` and `SessionEnd` events.
- **Observed** with Claude Code 2.1.330 in headless mode: one foreground tool call of 200 seconds, and three tasks in a row with that call inside one of them, with no lease lost; killing Claude Code or its bridge closes the connection at once; approving a new workflow version while Claude Code works stops its next call.
- **Not verified:** a file-changing tool being denied in an interactive session, hook calls made by subagents,
  `NotebookEdit`, a session changing its working directory, and resuming a session that holds a lease. Do not assume
  `enforce` covers them.
- A project can be connected while Waves is stopped; the agent simply cannot claim tasks until `waves start`.
- Execution diffs include the untracked Waves files above. Do not be surprised to see them in a diff.
