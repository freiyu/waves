# Quick start

Waves keeps the plan for your coding agent, hands it one task at a time, and moves to the next phase only after
you approve with your passphrase. This page takes you from install to your first approved workflow.

## 1. Install

```bash
curl -fsSL https://github.com/freiyu/waves/releases/latest/download/install.sh | sh
waves version
```

You need Linux or WSL2, `git` and Claude Code (`claude` on your PATH). The binary is one static file; nothing
else is installed and Node.js is not needed. See the README for the other install options.

## 2. Set up

```bash
waves setup
```

`setup` needs a terminal. It creates the folders below, two access tokens for local tools, and asks for your
**approval passphrase** (at least 12 characters). Every approval asks for it. Choose one you do not keep on this
machine: agents run as your user and can read any file you can read.

| What | Where |
|---|---|
| Config and tokens (`config.json`, `operator.token`, `guard.token`) | `~/.config/waves` |
| Database, backups, diffs | `~/.local/share/waves` |
| Logs and the service log | `~/.local/state/waves` |

All folders are private to you (mode 700, files 600). The standard `XDG_*` variables move them.

## 3. Start and sign in

```bash
waves start     # runs in the background on http://127.0.0.1:7317
waves open      # prints a one-time sign-in link, valid for 60 seconds
waves doctor    # checks folders, tokens, git, claude, and the audit chain while the service runs
```

Open the link in a browser on the same machine. Signing in lasts 12 hours. The service listens on
`127.0.0.1` only. To use another port, put `{"port": 7400}` in `~/.config/waves/config.json` and run
`waves stop` then `waves start`.

## 4. Your first workflow

```bash
cd ~/work/my-project           # a git repository
waves project add .
waves requirements add --text "Add a /health endpoint that returns 200 and a test for it"
waves plan request
waves agent connect claude-code
```

`connect` shows exactly which two files it will write in the project, asks for `yes` and your passphrase, and
writes them. See [Connecting Claude Code](claude-code.md).

Then, in the project folder, start `claude` and say:

> Fetch the Waves planning request and submit a workflow proposal.

In the dashboard:

1. Open the project, read the phases, the tasks and the **validator commands** (Waves runs those commands, so
   read them), then **Submit** and **Approve** the workflow.
2. Approve the **Phase Start** of phase 1. Phase gates always ask for your passphrase.
3. In `claude` say: *Work on the next Waves task: claim it, get its context, do the work, heartbeat, and submit the
   result.* (Claude Code may ask permission for each Waves tool: allow them.) Each result waits for you: **Approve**, or **Reject with feedback**.
4. When every task is done, Waves runs the validators and opens the **phase review**. **Approve** completes the
   phase and opens the next one's start gate.

The README has the same walk-through with the terminal alternatives (`waves approval list`,
`waves approval review <id>`).

## Next

After a reboot the service is not running: run `waves start` again.

- [Security model](security.md): what is enforced and what is not
- [Backup, restore and upgrade](backup-restore.md)
- [Troubleshooting](troubleshooting.md)
