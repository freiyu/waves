<script lang="ts">
  import { toast } from 'svelte-sonner';
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { route, href } from '../lib/router.svelte';
  import { duration, shortClock, plural } from '../lib/format';
  import type { ReviewTask, ValidatorResult, PhaseReview } from '../api/models';
  import ProjectHead from '../lib/ProjectHead.svelte';
  import SectionHead from '../lib/SectionHead.svelte';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Btn from '../lib/Btn.svelte';
  import Badge from '../lib/Badge.svelte';
  import Table from '../lib/Table.svelte';
  import View from '../lib/View.svelte';
  import DecisionBox from '../lib/DecisionBox.svelte';
  import StepUpDialog from '../lib/StepUpDialog.svelte';
  import type { Column } from '../lib/table';

  const a = app();
  const res = resource(() => a.api.phaseReview(route.params.id!), ['phase.changed', 'approval.changed']);
  const apr = resource(async () => {
    const r = res.data;
    return r?.approval_id ? a.api.approval(r.approval_id) : null;
  }, ['approval.changed']);
  let open = $state<string | undefined>();
  let error = $state('');

  const valCols: Column<ValidatorResult>[] = [
    { key: 'name', label: 'Validator', text: (r) => `${r.name} (${r.kind})` },
    { key: 'res', label: 'Result', cell: vres },
    { key: 'dur', label: 'Duration', text: (r) => duration(r.duration_s) },
    { key: 'out', label: 'Checklist', cell: vact },
  ];
  const taskCols: Column<ReviewTask>[] = [
    { key: 'task', label: 'Task', text: (r) => `${r.code} ${r.title}`, sort: (r) => r.code },
    { key: 'state', label: 'State', cell: tstate },
    { key: 'dur', label: 'Duration', text: (r) => duration(r.duration_s) },
    { key: 'att', label: 'Attempts', text: (r) => String(r.attempts) },
    { key: 'files', label: 'Files changed', text: (r) => String(r.files_changed) },
  ];

  // A checked item counts as a passed validator and cannot be unchecked, so
  // confirming always goes through a dialog: with the passphrase, or with the
  // note that an elevated window covers it.
  let asking = $state<{ v: ValidatorResult; i: number } | null>(null);
  function confirm(v: ValidatorResult, i: number) {
    error = '';
    asking = { v, i };
  }
  // After a rejected review the human sends finished tasks back to work.
  let reopenIds = $state<string[]>([]);
  let reopenReason = $state('');
  let reopening = $state(false);
  let reopened = $state(false);
  const resultText = (v: ValidatorResult) =>
    v.state === 'passed' ? `Passed${v.exit_code != null ? ' · exit ' + v.exit_code : ''}` : v.state === 'failed' ? `Failed${v.exit_code != null ? ' · exit ' + v.exit_code : ''}` : v.items ? `${v.items.filter((i) => i.confirmed).length} of ${v.items.length} items confirmed` : 'Waiting';
  const failed = (r: PhaseReview) => r.validators.filter((v) => v.state === 'failed').length;
</script>

{#snippet vres(r: ValidatorResult)}<Badge state={r.state === 'waiting' ? 'waiting_approval' : r.state} text={resultText(r)} />{/snippet}
{#snippet vact(r: ValidatorResult)}{#if r.items}<button type="button" class="linkbtn" aria-expanded={open === r.id} onclick={() => (open = open === r.id ? undefined : r.id)}>Confirm items</button>{/if}{/snippet}
{#snippet tstate(r: ReviewTask)}<Badge state={r.state} text={r.state_detail ?? undefined} />{/snippet}

<View {res} label="the phase review">
  {#snippet children(r)}
    <ProjectHead id={r.project_id} current="review" name={r.project_name} />
    <SectionHead title="{r.phase.code} {r.phase.title} · phase review" sub="workflow v{r.workflow_number} · package generated {shortClock(r.generated_at)}">
      {#snippet right()}<Badge state={r.phase.state} />{/snippet}
    </SectionHead>
    {#if error}<Note kind="dng" icon="fail" role="alert">{error}</Note>{/if}
    {#if reopened}<Note kind="ok" role="status">Tasks reopened. Agents can claim them again and see your review feedback.</Note>{/if}
    <div class="cols">
      <div class="stack-lg">
        {#if r.phase.state === 'revision_required'}
          <Panel level={3} title="Revise this phase">
            <div class="pb stack">
              <div class="small muted">The review was rejected. Choose the tasks to redo. They go back to Ready, the agent gets your review feedback, and the validators run again before a new review.</div>
              <ul class="stack" style="list-style:none;padding:0;margin:0">
                {#each r.tasks.filter((t) => t.state === 'done') as t (t.id)}
                  <li class="row"><input type="checkbox" id="ro-{t.id}" value={t.id} bind:group={reopenIds} /><label for="ro-{t.id}">{t.code} {t.title}</label></li>
                {:else}
                  <li class="small muted">No finished task to reopen.</li>
                {/each}
              </ul>
              <div><label class="lbl" for="ro-reason">Reason (required)</label><textarea id="ro-reason" class="field" bind:value={reopenReason}></textarea></div>
              <div><Btn kind="pri" lock disabled={!reopenIds.length || !reopenReason.trim()} onclick={() => (reopening = true)}>Reopen {reopenIds.length || ''} {reopenIds.length === 1 ? 'task' : 'tasks'}</Btn></div>
            </div>
          </Panel>
        {/if}
        <Panel level={3} title="Validator results ({failed(r)} failed)">
          <Table label="Validator results" rows={r.validators} columns={valCols} rowKey={(v) => v.id} />
          {#each r.validators.filter((v) => v.items && open === v.id) as v}
            <ul style="list-style:none;padding:12px 16px;border-top:1px solid var(--border)" class="stack">
              {#each v.items ?? [] as it, i}
                <li class="row"><input type="checkbox" id="chk-{v.id}-{i}" checked={it.confirmed} disabled={it.confirmed} onclick={(e) => { e.preventDefault(); void confirm(v, i); }} /><label for="chk-{v.id}-{i}">{it.text}</label></li>
              {/each}
              <li class="small muted">A confirmation records that you checked the item. It is not an approval. It needs your passphrase or an elevated window.</li>
            </ul>
          {/each}
        </Panel>
        <Panel level={3} title="Tasks"><Table label="Tasks" rows={r.tasks} columns={taskCols} rowKey={(t) => t.code} /></Panel>
        <Panel level={3} title="Unattributed and out-of-scope changes ({r.findings.length})">
          {#each r.findings as f}
            <div class="list-row"><Badge state="blocked" text={f.kind === 'unattributed' ? 'Unattributed' : 'Out of scope'} /><span class="mono">{f.path}</span><span class="muted">{f.detail}</span></div>
          {:else}<div class="empty">Every changed file belongs to a task of this phase.</div>{/each}
        </Panel>
      </div>
      <div class="stack-lg">
        <Panel level={3} title="Summary">
          <div class="pb stack">
            <div>{r.summary}</div><div class="muted small">AI-generated, not verified.</div>
            {#if r.recommendation}<div><b>Recommendation:</b> {r.recommendation}</div>{/if}
          </div>
        </Panel>
        <Panel level={3} title="Diff summary">
          <div class="pb stack"><div>{plural(r.diff.files, 'file')} · +{r.diff.added} −{r.diff.removed}</div><div class="muted small">{plural(r.diff.executions, 'execution')}. <a href={href('/executions')}>Open executions</a></div></div>
        </Panel>
        <Panel level={3} title="Decision">
          <div class="pb stack">
            <div class="small muted">A passing validator is never an approval.{#if r.requires_override_reason}{' '}{failed(r) ? `${failed(r)} ${failed(r) === 1 ? 'validator' : 'validators'} failed, so approving needs a written reason.` : 'Approving needs a written reason: see the recommendation.'}{/if}</div>
            {#if apr.data}
              <DecisionBox approval={apr.data} overrideRequired={r.requires_override_reason} ondecided={() => { void res.reload(); void apr.reload(); }} />
              {#if apr.data.state !== 'pending'}<Note kind={apr.data.state === 'approved' ? 'ok' : 'dng'}>This phase review was <b>{apr.data.state}</b>.</Note>{/if}
            {:else if apr.loading}<div class="muted" role="status">Loading the decision…</div>
            {:else}<div class="muted">No approval is open for this phase.</div>{/if}
          </div>
        </Panel>
      </div>
    </div>
    {#if reopening}
      <StepUpDialog
        title="Reopen tasks"
        kicker="Reopening needs your passphrase"
        fieldLabel="Passphrase"
        needPassphrase={!a.elevationFor()}
        facts={[{ label: 'Phase', value: `${r.phase.code} ${r.phase.title}` }, { label: 'Tasks', value: r.tasks.filter((t) => reopenIds.includes(t.id)).map((t) => t.code).join(', ') }, { label: 'Reason', value: reopenReason.trim() }]}
        confirm="Reopen"
        onconfirm={async (p) => { await a.runElevated(p, (secret) => a.api.reopenTasks(route.params.id!, reopenIds, reopenReason.trim(), secret)); reopening = false; reopened = true; reopenIds = []; reopenReason = ''; await res.reload(); }}
        oncancel={() => (reopening = false)}
      />
    {/if}
    {#if asking}
      {@const q = asking}
      <StepUpDialog
        title="Confirm checklist item"
        kicker="Confirming needs your passphrase"
        fieldLabel="Passphrase"
        needPassphrase={!a.elevationFor()}
        facts={[{ label: 'Validator', value: q.v.name }, { label: 'Item', value: q.v.items?.[q.i]?.text ?? '' }]}
        confirm="Confirm"
        onconfirm={async (p) => { await a.runElevated(p, (secret) => a.api.confirmValidation(route.params.id!, q.v.id, q.i, secret)); asking = null; toast.success('Checklist item confirmed'); await res.reload(); }}
        oncancel={() => (asking = null)}
      />
    {/if}
  {/snippet}
</View>
