# Backup, restore and upgrade

## Backup

```bash
waves backup                 # writes ~/.local/share/waves/backups/waves-<UTC time>.db
waves backup ~/safe/waves.db # or to a path you choose
```

`backup` needs the service running and does not stop it. The service writes only into its own backup folder
(mode 600); with a path, the command then moves that file to the path you name, as you, and refuses to overwrite
an existing file or follow a symlink. The copy is a consistent snapshot of the database. It holds project names and paths,
task text, approvals and the audit chain, so keep it private.

## Restore

```bash
waves stop
waves restore ~/safe/waves.db
waves start
```

Only restore a backup you made yourself. Restore needs no passphrase and replaces the passphrase, the approvals and
the audit log with the file's content, so a foreign file can bring approved validator commands with it.

Restore works only with the service stopped and in a terminal. You type `restore` to confirm. It copies the file once
and checks that copy before it replaces anything: it must open, pass SQLite's integrity check and have a schema this
version knows. The database it replaces is kept next to it as `waves.db.replaced-<time>`.

The `db.restored` record (scope `local_offline`, with the file's SHA-256) is written into the checked copy before it is
moved into place, so a restore never happens without it. It works even when the current database is damaged, which
is the usual reason to restore. After a restore, **Database was restored from a backup** stays under Needs attention until you acknowledge it on the Activity page.
Check that the passphrase, approvals and audit log are the ones you expect first.

If the audit chain in the restored file does not verify, Waves says so at start and blocks approvals until you
acknowledge it.

## Upgrade

Run `waves update`. It downloads the newest release from GitHub, checks the file against the `SHA256SUMS` of the
same release, and only then replaces the binary. The old binary stays next to it as `waves.previous` (an earlier
`waves.previous` is replaced). If the service was started with `waves start`, Waves stops it and starts the new
version for you, and puts the old binary back if the new one does not come up. It refuses, and changes nothing:

- while executions are running, or when it cannot tell (an update interrupts them, and an interrupted task is never
  run again by itself): finish or cancel them, or pass `--force`;
- when a service answers on the port that `waves start` did not start: stop that service yourself first;
- for an older release: it can refuse to start on a database a newer version has migrated. Make a copy with
  `waves backup`, then pass `--force`.

The network is used only while a command runs: nothing checks for updates in the background, and `waves start` does not
either unless you ask it to. To get a notice, put `"update_check": true` in `~/.config/waves/config.json` (for example
`{"port": 7317, "update_check": true}`). From then on `waves start` asks GitHub once whether a newer release exists and prints
`Version X is online, update waves ...` when there is one. The request is for the release page of this project. GitHub sees your IP
address and the page you asked for, and anyone on the network path can see your IP address and that you looked up github.com.
It carries a generic `waves` User-Agent and no version or name, and `HTTPS_PROXY` and similar variables are honored. It has a 2
second limit, so the command can finish up to 2 seconds after the service is up; a failure prints nothing.
Version 1.0.0 has no `waves update` and no such notice: to leave 1.0.0, run `install.sh` again. If you ever go back to 1.0.0
after setting `update_check`, delete that line from `config.json` first: 1.0.0 refuses a config file with a key it does not know.

- `waves update --version X` installs that release (an older one needs `--force`, see above).
- `waves update --from <folder>` uses `waves-linux-<arch>` and `SHA256SUMS` you downloaded yourself.
- The checksum catches a damaged download, not a compromised release: both files come from the same place.
- To install by hand instead: `install.sh` again (or copy the file), `waves stop`, `waves start`.

**From 1.0.1 to 1.2.0 there is no database migration.** `waves update` replaces the binary and restarts the service; projects that
are already connected keep working and need no `waves agent connect` again, and a result that was waiting for your approval in 1.0.1 can still be decided. The 1.0.1
binary opens the same database again, so going back is `waves update --version 1.0.1 --force`; what finished under 1.2.0 stays as it is.

On the first start a newer version that has migrations applies them. Before it changes anything it writes a
copy next to the database, `waves.db.pre-v<N>-<time>.bak`. Your projects, workflows, approvals and audit log are
kept. Running an **older** binary on a database from a newer one is refused, so you cannot damage it by going
back by mistake. To go back after the new version has started: `waves stop`, move `waves.previous` over `waves`,
`waves restore <waves.db.pre-v<N>-<time>.bak>` (the file next to `waves.db`), `waves start`. Everything done since
the upgrade is lost. Before the new version has started for the first time, moving `waves.previous` back is enough.

## Housekeeping

- `waves gc` deletes execution logs and validator output older than the retention setting (default 14 days).
  The database (with its audit log), diffs, context packages and backups are kept.
- Old `.bak` and `.replaced-*` files are yours to delete when you are sure you do not need them.

## Forgot the passphrase

```bash
waves stop
waves auth passphrase --reset
waves start
```

This needs a terminal on the machine, asks you to type `reset`, replaces the passphrase and signs out every
session. It is a local, offline action by design. Afterwards Waves lists **Passphrase was reset** under Needs
attention, and the audit log holds a `passphrase.reset` record with the scope `local_offline`. Open Activity,
check the record and acknowledge it with the new passphrase.
