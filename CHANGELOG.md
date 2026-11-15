# Changelog

## 1.0.1

A redesigned dashboard, tasks that no longer go to **recovery required** while Claude Code waits for you (once the
project is connected again), and `waves update`.

**Dashboard**
- A new look in a light and a dark theme. The Overview page lists **Needs your decision** and the executions that wait
  for instruction.
- **Graph**: every project and its phases on one canvas. Drag nodes and frames, move the end of a line to any side of a
  node, open a phase or task for its summary, and approve or reject from the phase's detail pane (the same passphrase
  confirmation). There is also a Table view. Positions are saved in the Waves database. A project can be marked as a
  fork of another; the Graph draws the line. A project whose proposed workflow waits shows **Proposal ready**, which
  opens its Plan tab.
- A project opens once and its tabs (Overview, Requirements, Plan, Review, Guard) switch without loading again.
- Approvals live in their project. The separate Approvals page and the Workflow page are gone: the workflow is shown
  and approved in the Plan tab, a phase completion in the Review tab, results and risky actions from the project
  Overview. Old addresses open the Overview, or the project's Plan or Review tab.
- The Guard tab shows the guard state, a changed or removed hook, and what the guard does not defend.
- Keyboard focus is visible on tab panels and the theme menu; the page uses the whole screen on phones with a notch.

**Waiting for instruction**
- A Claude Code session that finished its turn and waits for you is shown as **Waiting for instruction** instead of being
  taken for a dead one. **Release** ends the wait. Waves installs three more hooks (Stop, UserPromptSubmit, SessionEnd) to know. Projects connected
  with 1.0.0 need **Connect again** once. The longest wait is a setting (8 hours by default).
- A guard decision for a leased session renews its lease, at most once every 15 seconds.

**Safer text**
- Text written by an agent that hides itself (escape codes, right-to-left overrides, zero-width characters) is refused
  when it arrives and shown with its characters spelled out in the dashboard and the terminal.

**`waves update`**
- Replaces the binary with the newest release after checking it against `SHA256SUMS`, keeps the old one as
  `waves.previous` (replacing an earlier copy), restarts a service started with `waves start`, and puts the old binary
  back if the new one does not start. It refuses while executions are running, and for an older release, unless you
  pass `--force`.

**Update notice**
- Off by default. With `"update_check": true` in `config.json`, `waves start` asks GitHub once whether a newer release
  exists and tells you when there is one. It adds at most 2 seconds to `waves start`, fails silently and downloads
  nothing. To go back to 1.0.0 afterwards, remove the `update_check` line first (1.0.0 refuses unknown keys). Nothing checks in the background, and nothing leaves your machine unless you turn this on.

**Upgrading from 1.0.0**: run `install.sh` again (1.0.0 has no `waves update`; from 1.0.1 on, use `waves update`). The first start migrates the database after writing a
backup next to it (`waves.db.pre-v<N>-<time>.bak`). If you restart the service while a task is being worked on (with
`install.sh` or by hand; `waves update` refuses), that task is shown as **recovery required** and waits for your
decision; nothing is run again by itself. Older binaries refuse the migrated database: to go back, restore the backup.

Requirements are unchanged: Linux or WSL2 on amd64 or arm64, `git`, Claude Code. No new runtime dependency.
