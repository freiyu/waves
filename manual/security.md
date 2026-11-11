# Security model

Waves is local-first. Its central rule is that **no phase starts or completes without a human**: the service
enforces it, and a few limits (below) remain on the same machine.

## What is enforced

- **Approval is enforced by the service, not by prompts.** A phase gate, a result, a workflow or a risky action
  moves only when the Waves service records an approval from the `human` scope. Database triggers refuse state
  changes that skip this.
- **The human scope exists only after a passphrase step-up.** Operator tokens, guard tokens, lease tokens, MCP
  and agents can never approve. Phase gates ask for the passphrase **every time**. Other approvals can use an
  unlocked window you configure (default 5 minutes; with 0, each unlock covers a single action).
- **Guessing is slow.** Wrong passphrases are delayed, with the delay doubling up to 5 minutes, and repeated
  failures raise an alert.
- **Local only.** The service binds to `127.0.0.1`. Requests need a token or session, and Host, Origin and
  fetch-site checks apply to the dashboard. Sign-in links last 60 seconds and work once.
- **Approvals, state changes and refusals are recorded** in an append-only audit log with a hash chain: decisions,
  state transitions, refused attempts, guard denials and policy changes. It is verified at
  start, on every status request and before every approval. If a record was altered, approvals are blocked until
  you review and acknowledge it.
- **Interrupted work is never rerun automatically**, there is no automatic merge, and a workflow changes only
  through a new version that you approve.
- **Waves stores no AI provider credentials.** The database holds only hashes of tokens and of the passphrase.
  The token files in `~/.config/waves` hold the usable tokens (mode 600), so any process of your user can read them.

## The guard hook

The Claude Code hook (see [Connecting Claude Code](claude-code.md)) denies unleased edits by the file-editing tools
under `enforce` and fails closed. New projects start in `audit`, which blocks nothing. **While a task is leased,
shell commands are not inspected**, so an agent can change files outside its workspace through the shell. The hook
lowers the chance of an agent touching files outside its task. It is **not a sandbox**.

Waves compares the hook in your project with the one it installed. A hook that is missing, switched off
(`disableAllHooks`) or edited shows as not installed in `waves doctor`, `waves guard status` and as a red notice on
the Guard tab of the Harnesses page. That is detection after the fact, not prevention: while a task is leased an
agent can still edit the file through the shell, and nothing alerts you until you look. The check covers only the
Waves hook entry and `disableAllHooks` in `.claude/settings.local.json`. It does not look at other hooks in that
file, its `env` setting, other settings files, or `.mcp.json`. Changes to the two connection files also show in execution diffs, even if git ignores them.

## Not defended

**A process running as your user with unrestricted file or `ptrace` access can
tamper with Waves directly**. It can edit the database, read process memory, read the token files, or log
keystrokes, including the passphrase. The audit chain can detect some of that after the fact; Waves does not
prevent it.

Also: whoever can run `waves` as your user in a terminal can reset the passphrase without knowing the old one
(`waves auth passphrase --reset`). An agent with shell access can open a pseudo-terminal, so it counts. The reset is not
prevented. It is recorded in the audit log with the scope `local_offline` (never as a human action), the record
names the audit head it replaced the checkpoints at, and **Passphrase was reset** stays under Needs attention
until you acknowledge it on the Activity page. If you did not reset it yourself, treat every approval since then as
untrusted. Acknowledging clears every open security alert (this one, repeated wrong passphrases and bypass
attempts), so read them all first; the records themselves stay in the audit log.

Practical consequences:

- Do not store the approval passphrase on the machine. Type it; do not put it in files, environment variables or
  shell history.
- Agents run with your permissions. If you need stronger isolation, run the agent under a separate OS account
  or in a container or VM, and keep Waves and its folders outside the agent's reach.
- Waves does not defend against another local user with root, or against malware on your account.

## Where secrets and data live

| Item | Location | Notes |
|---|---|---|
| Operator and guard tokens | `~/.config/waves/*.token` | mode 600; refused if group or other can read them |
| Passphrase | database | argon2id hash only |
| Database, diffs, backups | `~/.local/share/waves` | contains project names, paths and task text |
| Logs | `~/.local/state/waves` | secrets are never logged; logs can contain agent output |

Treat backups like the database. Do not commit or share them without reviewing them.
Only restore a backup you made yourself: restore needs no passphrase, and it replaces the passphrase,
approvals and audit log with the file's content. A restore is recorded as `local_offline` and listed under Needs
attention until you acknowledge it, but it is not prevented.

## Reporting a problem

Open an issue on the project's GitHub page. For a security problem, do not include tokens, the database or
logs in the report.
