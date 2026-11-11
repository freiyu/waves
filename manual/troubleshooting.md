# Troubleshooting

Start with `waves doctor`. It checks the folders and their permissions, both token files, `git`, `claude` and the
service; with the service running it also checks the audit chain and the Claude Code version. Each line says `ok`
or `FAIL` with the path or version it looked at; a stopped service also tells you to run `waves start`.

| Symptom | What to do |
|---|---|
| `waves start`: "the service did not come up" | Read `~/.local/state/waves/service.log`. The usual cause is a port already in use: set another one in `~/.config/waves/config.json` (`{"port": 7400}`). |
| The sign-in link says expired or used | Links last 60 seconds and work once. Run `waves open` for a new one. |
| `waves setup` says it needs a terminal | It asks for the passphrase, so it will not run from a pipe or a script. Run it in a normal terminal. |
| A wrong passphrase, then a wait | Waits grow after each failure, up to 5 minutes. Wait, then try again. If you forgot it, see [Backup, restore and upgrade](backup-restore.md#forgot-the-passphrase). |
| An approval asks for your passphrase again | Phase gates always ask. Other approvals ask unless an unlocked window is active. |
| The dashboard shows an audit chain warning, and approvals are blocked | A record in the audit log does not match its chain. Open Activity in the dashboard, review the record, and acknowledge the break with your passphrase, or restore a backup. Do not ignore it: it can mean the database was edited outside Waves. |
| Claude Code does not offer the Waves tools | Check that `.mcp.json` exists in the project (`waves agent connect claude-code` writes it), start `claude` from that folder and approve the `waves` server when asked. |
| `waves doctor` shows `FAIL hook <project>`, or `waves guard status` says the guard is not active | The hook in the project was removed, switched off or edited. Find out why (an execution diff shows changes to `.claude/settings.local.json`), then run `waves project switch <project>` and `waves guard <policy>` to write it back. |
| **Passphrase was reset** is listed under Needs attention | The passphrase was reset from a terminal without the old one. If that was you, open Activity, check the `passphrase.reset` record, and acknowledge it. If it was not you, treat approvals since then as untrusted and review the audit log. |
| **Database was restored from a backup** is listed under Needs attention | `waves restore` replaced the database. If that was you, open Activity, check the `db.restored` record, and acknowledge it. If it was not you, treat approvals since then as untrusted. |
| Claude Code cannot write files under `enforce` | The hook fails closed. Check `waves status` and `waves doctor`, start the service, or switch policy with `waves guard audit`. A write outside the task's workspace or to a protected file is denied on purpose. |
| A task is blocked after a crash or disconnect | Expected: Waves never reruns work by itself. Open the execution's Recovery page and choose Retry the task, Accept partial result, or Mark failed. From a terminal: `waves execution cancel <id>`, then `waves task retry <task id> "<reason>"`. |
| A rejected phase needs more work | In the phase review use **Reject with feedback**, then **Revise this phase**, pick the tasks and **Reopen** them (passphrase). |
| Logs take too much space | `waves gc` applies the retention setting. Lower it in Settings. |
| `restore` or `auth passphrase --reset` says to stop the service | Run `waves stop` first. These work only offline. |

## Still stuck

Collect `waves version`, the output of `waves doctor`, and the last lines of `~/.local/state/waves/service.log`,
remove anything private, and open an issue on the project's GitHub page.
