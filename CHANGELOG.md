# Changelog

## 1.2.1

A new look for the dashboard, and fixes for problems found in 1.2.0.

**Fixed (these were in 1.2.0)**
- **A guard change on the Harnesses page could land on another project.** The dialogs for the guard policy, the
  unmanaged-sessions setting and **Disconnect** followed the active project, which another client holding the operator token
  can change while you type your passphrase, and two of them did not name a project. Each dialog now names its project and
  changes only that one, and the page stays on the project it shows. If you changed these settings on that page and have more
  than one project, check each one on the Harnesses page.
- A connected agent could be shown **Disconnected** while it was quiet: a connection that opened very quickly was not counted.
- A Claude Code session now answers only the project credential that opened it. Another project's credential that knows the
  session id is refused, and the attempt is recorded in the audit log.
- With very many sessions, Waves could forget which connections were still open. It now drops only closed ones.
- A connection opened with a credential at the moment it is revoked no longer holds a lease.
- A restore that was cut off no longer blocks the next one.
- The dashboard showed **Reconnecting** again after it had said **Stale**. It stays **Stale** until it is connected, with a
  notice on the page.

**A new dashboard**
- New light and dark colours and a rounded font. The fonts are inside the binary, nothing is loaded from the internet, and
  their licences are served under `/licenses/`.
- A narrow sidebar that opens with the page names when you point at it or reach it with the keyboard.
- **Overview** shows what needs you as cards, the phases of each project, and what is running now. A project page shows its
  phases and its tasks as cards of one size; a long task title is cut and shown whole when you point at it.
- A phase review is decided on the page, next to what you are judging, and the approval of a finished phase names changes
  outside the plan wherever you decide it. The **Plan** tab puts a waiting decision first.
- **Executions** shows the last hour as a timeline.
- **Graph**: it shows the tasks of every phase. Opening a phase makes room around it, projects and phases do not lie on each
  other (the phase you bring close makes the other one step aside), and lines join the sides that face each other. A side
  you picked by hand is dropped when two phases come to face each other differently.
- Buttons are labelled and coloured by what they do. A wrong passphrase shakes the field, and an accepted decision is shown
  on the screen with the word for it.

**For agents**
- The planning request now asks for task titles of 60 characters or fewer. Longer titles, up to 160, are still accepted.

**Upgrading from 1.2.0 or 1.0.1**: run `waves update`. There is no database migration, projects that are connected keep
working, and the 1.2.0 binary opens the same database again (`waves update --version 1.2.0 --force`). **From 1.0.0**: run
`install.sh` again; the first start migrates the database after writing a backup.

Requirements are unchanged: Linux or WSL2 on amd64 or arm64, `git`, Claude Code. No new runtime dependency.

## 1.2.0

Claude Code keeps its task during a long build or test run, you approve once per phase instead of once per task, and a new
workflow can replace one that is running.

**A lease is held by a connection, not a clock**
- Before, a task whose agent said nothing to Waves for three minutes (a long build or test run is enough) was marked
  **Disconnected** and then **recovery required**, and the agent's result was refused. Now the lease lasts as long as the
  agent's session stays connected to Waves. A tool call of any length is fine.
- If the session disappears (Claude Code or its bridge dies), the execution is **Disconnected** at once and, unless the
  session returns within 30 seconds, **Interrupted**, with the task **Blocked** for your decision. Nothing is rerun by itself.
- Agents report three things: the task is taken (claim), ongoing (`waves_report_progress`) and done
  (`waves_submit_result`). A heartbeat is no longer required; `waves_heartbeat` stays for checking the lease, a cancel
  request and the decision on a risky action.
- A tool that does not keep a connection open is held by the old heartbeat rules, and the heartbeat interval setting
  still applies to it.
- A connected Claude Code window that waits for you keeps its task, its workspace lock and its slot with no time limit; use
  **Release** to end it. The longest-wait setting applies only to a session without a connection.

**One approval per phase**
- Tasks of a phase are done one after another, and a finished task is **Done** at once. You no longer approve each result.
  At the end of a phase the **Review** tab lists every task with what the agent said, the files that changed and the
  validator results, and you approve (or reject with feedback and reopen tasks) with your passphrase.
- Phase starts, phase completions, workflows and risky actions still need your passphrase every time they did before.
- A completed result must come with a summary. Results that were waiting for approval when you upgrade can still be decided.
- Validator commands now run as soon as the last task of a phase is Done, before you review it, so they run on what the
  agents just wrote. Read them when you approve the workflow.

**Replacing a workflow while work runs**
- Approving a new workflow version no longer waits for the old one to finish. The work still open under the old version is
  cancelled and is never rerun, approvals waiting on it are withdrawn, and the agent's next call is refused. The approval screen
  lists what will be cancelled, and goes stale if that list changes while you read it.

**Dashboard**
- A **Blocked**, **Disconnected** or **Interrupted** task shows its state and nothing else on every page. The reason is on the
  **Activity** page, which now reads state changes as words (for example "Running → Disconnected · session left").
- Fixed: a task kept the reason of an earlier block on its badge after you retried it.
- The Execution page says when a lease is held by the session's connection.
- Settings explain that the heartbeat interval and the longest wait apply only to a session without a connection.

**Safer**
- An unknown session cannot attach a connection to another project's lease; a connection opened with a credential that is
  later revoked is let go.
- Accepting partial work after an interruption needs only the operator credential (no passphrase) and says so in the phase review.

**Upgrading from 1.0.1**: run `waves update`. There is no database migration, projects that are connected keep working, and
the 1.0.1 binary opens the same database again (`waves update --version 1.0.1 --force`). **From 1.0.0**: run `install.sh`
again; the first start migrates the database after writing a backup.

Requirements are unchanged: Linux or WSL2 on amd64 or arm64, `git`, Claude Code. No new runtime dependency.

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
