# Waves for AI agents

This page is written for a coding agent (such as Claude Code) that works in a project connected to Waves.
A human owns every decision. You do the work inside the limits below. Waves refuses anything outside them, and
`waves_get_planning_request` and `waves_get_task_context` return the current rules and schema: trust those over this page.

## What Waves is

Waves keeps the plan, hands out one task at a time, and lets nothing move to the next phase without the human's
passphrase. You talk to it through the `waves` MCP server (tools named `waves_*`, from `.mcp.json`). A guard hook
in the project also checks your file-editing tool calls.

## You cannot approve anything

No MCP tool approves, and none will. Workflows, phase starts, phase completions, results and risky actions all wait for
the human. A passing test or validator is not an approval. Do not look for a way around this, such as editing Waves
files, the database, tokens, or `.claude/settings.local.json` and `.mcp.json`: the attempt is refused or recorded.

## Plan: when asked for a workflow proposal

1. Call `waves_get_planning_request`. It returns the requirements, the proposal schema, the rules, and the errors
   of your last invalid proposal.
2. Call `waves_submit_workflow_proposal` with `planning_request_id` and a `proposal` that follows that schema exactly.
3. If `valid` is false, fix every item in `errors` (each has a JSON pointer in `path`) and submit again. A valid proposal
   becomes a draft. The human reviews and approves it; you do not.

Write validator commands the human can read and trust: they are shown for approval and run later with `/bin/sh -c`.

## Work: one task at a time

1. `waves_list_available_tasks`, then `waves_claim_task` with one `task_id`. The lease belongs to this session.
2. `waves_get_task_context`: the task, acceptance criteria, phase objective, requirements, any feedback from a rejected
   result, and the rules. Read the rules.
3. Do the work inside the project folder. Call `waves_heartbeat` at the interval the claim gave (30 s by default) and
   `waves_report_progress` as you go. A heartbeat tells you if the lease is still valid, if a cancel was requested, and
   how a pending approval was decided.
4. Before a risky action (deleting data, changing CI, touching secrets): `waves_request_approval` with the action,
   reason and impact. Then wait. If it is rejected, do not do it.
5. If you cannot continue: `waves_report_blocker`. The task waits for the human.
6. Finish with `waves_submit_result` (`status` is `completed` or `failed`, with a summary that says how the
   acceptance criteria are met), or give the task back with `waves_release_task`.

## Limits that apply to you

- Work only on the task you hold. Without a lease, the guard may deny your file edits. Under the `enforce` policy it
  also denies edits outside the task's workspace and to `.git`, the Claude settings and `.mcp.json`. A `read_only`
  task must not write files.
- If a heartbeat shows a cancel request, stop and call `waves_release_task`.
- If you go silent, the execution is marked Disconnected after 2 missed intervals and the lease ends after 6. The
  task is then blocked for the human. **Waves never reruns an interrupted task by itself**, and neither should you
  assume it will.
- Shell commands are not inspected while you hold a lease. That is a gap in the guard, not permission: stay inside the
  task. Waves records the diff of every execution and flags changes that no execution accounts for.
- Do not put secrets, tokens or passphrases in summaries, progress messages or files.

## When something is refused

Errors carry a message and a next step. Read them and follow them. A refusal such as `wrong_scope`, `stepup_required`
or a denied tool call is a boundary, not a bug to work around. Report it with `waves_report_blocker` if it stops you.

For the human's side (install, approvals, backup), see the [quick start](quick-start.md) and the
[security model](security.md).
