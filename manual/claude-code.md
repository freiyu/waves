# Connecting Claude Code

Waves works with Claude Code through two project files. Waves writes only these two files, inside the project folder.

## What `waves agent connect claude-code` writes

Run it inside a registered project. It prints the planned change as a diff, asks for `yes`
and your passphrase, and then writes:

| File | Purpose |
|---|---|
| `.mcp.json` | registers the `waves` MCP server (a stdio bridge, `waves mcp`) so Claude Code can fetch plans, claim tasks and report results |
| `.claude/settings.local.json` | adds the **guard hook**, which asks Waves whether a file-changing tool call is allowed |

`waves agent disconnect` removes exactly these entries. `waves agent status` shows whether the project is
connected, and `waves guard status` shows the guard policy. Both files belong to your checkout. Do not hide them from git: Waves shows
changes to them in execution diffs, and an exclude rule would hide a tampered hook from that diff.

The first time you start `claude` in the project it may ask you to approve the `waves` MCP server. Approve it.
Claude Code may also ask permission before each Waves tool call; allow them, or the agent cannot work.

## What the agent can do

Through MCP the agent can read the planning request, submit a workflow proposal, claim a task, read its
context (including your feedback after a rejected result), send heartbeats and progress, report a blocker,
ask approval for a risky action, submit a result, and release a task it cannot finish. It **cannot approve anything**: there is no approval
operation in MCP, and the tokens it can reach are not allowed to approve.

A task is claimed with a **lease**. While the lease is active only that session works on that task. If the agent
disconnects, the lease runs out and the task is marked interrupted. **Waves never reruns an interrupted task
by itself**: you open the execution's Recovery page and choose to Retry the task, Accept partial result, or Mark failed.

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
passphrase) to write the hook back. The Guard tab of the Harnesses page shows the same warning. This is a check, not a lock: an
agent with a shell can edit the file during a task, and you will see it only afterwards.

## Things worth knowing

- **Observed** with Claude Code 2.1.x in headless mode (`claude -p`): MCP pickup from `.mcp.json`, the hook on
  every call, binding of a session to its lease, and fail-closed behavior when the service is down.
- **Not verified:** interactive (terminal UI) sessions, hook calls made by subagents, `NotebookEdit`, a session
  changing its working directory, and resuming a session that holds a lease. Do not assume `enforce` covers them.
- A project can be connected while Waves is stopped; the agent simply cannot claim tasks until `waves start`.
- Execution diffs include the untracked Waves files above. Do not be surprised to see them in a diff.
