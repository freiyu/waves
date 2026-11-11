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

1. Install the new binary over the old one (`install.sh` again, or copy the file).
2. `waves stop`, then `waves start`.

On the first start a newer version applies its database migrations. Before it changes anything it writes a
copy next to the database, `waves.db.pre-v<N>-<time>.bak`. Your projects, workflows, approvals and audit log are
kept. Running an **older** binary on a database from a newer one is refused, so you cannot damage it by going
back by mistake. To go back, restore a backup made before the upgrade.

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
