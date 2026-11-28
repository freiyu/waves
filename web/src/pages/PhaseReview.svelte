<script lang="ts">
  import { visible } from '../lib/text';
  import { toast } from 'svelte-sonner';
  import { app } from '../lib/app.svelte';
  import { ApiError } from '../api/client';
  import { resource } from '../lib/resource.svelte';
  import { route, href } from '../lib/router.svelte';
  import { ago, plural, shortDuration } from '../lib/format';
  import type { ValidatorResult, PhaseReview } from '../api/models';
  import ProjectHead from '../lib/ProjectHead.svelte';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Btn from '../lib/Btn.svelte';
  import Badge from '../lib/Badge.svelte';
  import Icon from '../lib/Icon.svelte';
  import View from '../lib/View.svelte';
  import StatusDot from '../lib/StatusDot.svelte';
  import DiffBar from '../lib/DiffBar.svelte';
  import ValidatorRing from '../lib/ValidatorRing.svelte';
  import ReviewDecision from '../lib/ReviewDecision.svelte';
  import StepUpDialog from '../lib/StepUpDialog.svelte';

  // The phase id comes from the project area (the awaiting phase), or from the address of the older standalone page.
  let { phaseId }: { phaseId?: string } = $props();
  const rid = $derived(phaseId ?? route.params.id!);
  const a = app();
  // One load for the review and its approval. The review that is shown is read after the approval, so it is never older than
  // the content hash the decision will carry.
  const both = resource(async () => {
    const first = await a.api.phaseReview(rid);
    if (!first.approval_id) return { review: first, approval: null };
    const approval = await a.api.approval(first.approval_id);
    const review = await a.api.phaseReview(rid);
    if (review.approval_id !== approval.id) throw new ApiError(409, 'stale_subject', 'The review changed while it was loading.', undefined);
    return { review, approval };
  }, ['phase.changed', 'approval.changed']);
  const apr = $derived(both.data?.approval);
  const res = {
    get data() { return both.data?.review; },
    get error() { return both.error; },
    get loading() { return both.loading; },
    reload: both.reload,
    set: () => {},
  };
  // The service refused a decision because what it is about changed: say so and read it again.
  let stale = $state(false);
  // What the human just decided here. The service no longer names an approval once it is decided, so the page keeps it.
  let outcome = $state<{ id: string; state: string } | null>(null);
  // The review names validators; their commands are in the workflow version it was made from.
  const commands = resource(async () => {
    const r = res.data;
    if (!r) return new Map<string, string>();
    const v = (await a.api.workflow(r.project_id)).versions.find((x) => x.number === r.workflow_number);
    if (!v) return new Map<string, string>();
    return new Map((await a.api.workflowVersion(v.id)).phases.flatMap((p) => p.validators.map((x) => [x.id, x.command ?? ''] as [string, string])));
  });
  const active = $derived(route.name === 'review' || route.name === 'phase-review');
  let open = $state<string | undefined>();

  // A checked item counts as a passed validator and cannot be unchecked, so
  // confirming always goes through a dialog: with the passphrase, or with the
  // note that an elevated window covers it.
  let asking = $state<{ v: ValidatorResult; i: number } | null>(null);
  // After a rejected review the human sends finished tasks back to work.
  let reopenIds = $state<string[]>([]);
  let reopenReason = $state('');
  let reopening = $state(false);
  let reopened = $state(false);
  const resultText = (v: ValidatorResult) => {
    const tail = `${v.exit_code != null ? ' · exit ' + v.exit_code : ''}${v.duration_s != null ? ' · ' + shortDuration(v.duration_s) : ''}`;
    if (v.state === 'passed') return 'Passed' + tail;
    if (v.state === 'failed') return 'Failed' + tail;
    return v.items ? `${v.items.filter((i) => i.confirmed).length} of ${v.items.length} items confirmed` : 'Waiting';
  };
  const count = (r: PhaseReview, state: ValidatorResult['state']) => r.validators.filter((v) => v.state === state).length;
  const verdict = (r: PhaseReview) =>
    count(r, 'failed') ? `${count(r, 'failed')} failed` : count(r, 'waiting') ? `${count(r, 'waiting')} waiting` : r.validators.length ? 'All passed' : 'None';
</script>

<View {res} label="the phase review">
  {#snippet children(r)}
    {@const done = r.tasks.filter((t) => t.state === 'done').length}
    <ProjectHead id={r.project_id} current="review" name={r.project_name} />
    <div class="flex flex-wrap items-end justify-between gap-4">
      <div class="hero min-w-0">
        <div class="text-xs text-fg-muted">Phase review · workflow v{r.workflow_number} · package generated {ago(r.generated_at)}</div>
        <h2 class="mt-2">{r.phase.code} {visible(r.phase.title)}<span class="sr-only"> · phase review</span></h2>
      </div>
      <Badge state={r.phase.state} />
    </div>
    {#if reopened}<Note kind="ok" role="status">Tasks reopened. Agents can claim them again and see your review feedback.</Note>{/if}

    <div class="sumstrip">
      <div>
        <span class="l {done === r.tasks.length ? 'text-st-ok' : 'text-st-warning'}">Tasks</span>
        <span class="n">{done} of {r.tasks.length} done</span>
        <span class="flex flex-wrap gap-2.5">{#each r.tasks as t (t.id)}<StatusDot state={t.state} label="{t.code}: {t.state.replace('_', ' ')}" />{/each}</span>
      </div>
      <div class="!flex-row items-center gap-5">
        <ValidatorRing passed={count(r, 'passed')} total={r.validators.length} failed={count(r, 'failed') > 0} />
        <div class="flex flex-col gap-1.5">
          <span class="l {count(r, 'failed') ? 'text-st-danger' : count(r, 'waiting') ? 'text-st-warning' : 'text-st-ok'}">Validators</span>
          <div class="text-[20px] font-extrabold">{verdict(r)}</div>
          <div class="text-xs text-fg-muted">A pass is not an approval.</div>
        </div>
      </div>
      <div>
        <span class="l">Changes</span>
        <span class="n">{plural(r.diff.files, 'file')}</span>
        <span class="self-stretch"><DiffBar added={r.diff.added} removed={r.diff.removed} /></span>
        <span class="mono !text-xs text-fg-muted">+{r.diff.added} −{r.diff.removed} · <a href={href('/executions')}>{plural(r.diff.executions, 'execution')}</a></span>
      </div>
    </div>

    <div class="reviewcols">
      <div>
        <section class="flex flex-col gap-3" aria-label="Every task of the phase">
          <div class="sect"><h3 class="h2">Every task of the phase</h3></div>
          <ul class="tasklist">
            {#each r.tasks as t (t.id)}
              <li>
                <span class="tmark {t.state === 'done' ? 'bg-st-ok-soft text-st-ok' : ''}">{#if t.state === 'done'}<span class="inline-flex" role="img" aria-label="done" title="done"><Icon name="check" size={14} /></span>{:else}<StatusDot state={t.state} label={t.state.replace('_', ' ')} />{/if}</span>
                <div class="min-w-0">
                  <div class="flex flex-wrap items-baseline justify-between gap-x-4">
                    <b class="text-[15px]"><span class="mr-2">{t.code}</span>{visible(t.title)}</b>
                    <span class="text-xs text-fg-muted">{t.duration_s != null ? shortDuration(t.duration_s) + ' · ' : ''}{plural(t.attempts, 'attempt')} · {plural(t.files_changed, 'file')}</span>
                  </div>
                  {#if t.state !== 'done'}<div class="mt-1"><Badge state={t.state} /></div>{/if}
                  {#if t.summary}<p class="mt-1 text-[13px] text-fg-2">{visible(t.summary)}</p>{/if}
                </div>
              </li>
            {/each}
          </ul>
          <p class="text-xs text-fg-muted">What the agent said is AI-generated and not verified.</p>
        </section>

        <section class="flex flex-col gap-3" aria-label="Validator results">
          <div class="sect"><h3 class="h2">Validator results</h3></div>
          {#each r.validators as v (v.id)}
            <div class="panel px-5 py-4">
              <div class="flex flex-wrap items-center justify-between gap-3">
                <div class="min-w-0">
                  <b class="text-sm">{visible(v.name)}</b>
                  <div class="mono !text-xs whitespace-pre-wrap text-fg-muted">{v.kind === 'command' ? (commands.error ? 'The command could not be loaded. Open the Plan tab to read it.' : visible(commands.data?.get(v.id) ?? '')) : visible((v.items ?? []).map((i) => i.text).join(' · '))}</div>
                </div>
                <span class="flex items-center gap-1.5 text-[13px] font-bold {v.state === 'passed' ? 'text-st-ok' : v.state === 'failed' ? 'text-st-danger' : 'text-st-warning'}"><Icon name={v.state === 'passed' ? 'check' : v.state === 'failed' ? 'x' : 'wait'} size={14} />{resultText(v)}</span>
              </div>
              {#if v.items}
                <button type="button" class="linkbtn mt-2 text-[13px] font-semibold" aria-expanded={open === v.id} onclick={() => (open = open === v.id ? undefined : v.id)}>Confirm items</button>
                {#if open === v.id}
                  <ul class="stack mt-3" style="list-style:none;padding:0">
                    {#each v.items as it, i}
                      <li class="row"><input type="checkbox" id="chk-{v.id}-{i}" checked={it.confirmed} disabled={it.confirmed} onclick={(e) => { e.preventDefault(); asking = { v, i }; }} /><label for="chk-{v.id}-{i}">{visible(it.text)}</label></li>
                    {/each}
                    <li class="small muted">A confirmation records that you checked the item. It is not an approval. It needs your passphrase or an elevated window.</li>
                  </ul>
                {/if}
              {/if}
            </div>
          {:else}
            <div class="panel empty">This phase has no validator.</div>
          {/each}
        </section>

        <section class="flex flex-col gap-3" aria-label="Outside the plan">
          <div class="sect"><h3 class="h2">Outside the plan <span class="ml-2">{r.findings.length}</span></h3></div>
          {#each r.findings as f}
            <div class="finding">
              <div class="flex items-center gap-1.5 text-[13px] font-bold"><Icon name="warn" size={14} />{f.kind === 'unattributed' ? 'Unattributed' : 'Out of scope'}</div>
              <div class="mono">{visible(f.path)}</div>
              <div class="text-[13px]">{visible(f.detail)}</div>
            </div>
          {:else}<div class="panel empty">Every changed file belongs to a task of this phase.</div>{/each}
        </section>
      </div>

      <div>
        <Panel level={3} title="Summary" cls="quiet">
          <div class="pb stack">
            <div class="text-[19px] leading-7">{visible(r.summary)}</div><div class="muted small">AI-generated, not verified.</div>
            {#if r.recommendation}<div><b>Recommendation:</b> {visible(r.recommendation)}</div>{/if}
          </div>
        </Panel>
        {#if r.phase.state === 'revision_required'}
          <Panel level={3} title="Revise this phase">
            <div class="pb stack">
              <div class="small muted">The review was rejected. Choose the tasks to redo. They go back to Ready, the agent gets your review feedback, and the validators run again before a new review.</div>
              <ul class="stack" style="list-style:none;padding:0;margin:0">
                {#each r.tasks.filter((t) => t.state === 'done') as t (t.id)}
                  <li class="row"><input type="checkbox" id="ro-{t.id}" value={t.id} bind:group={reopenIds} /><label for="ro-{t.id}">{t.code} {visible(t.title)}</label></li>
                {:else}
                  <li class="small muted">No finished task to reopen.</li>
                {/each}
              </ul>
              <div><label class="lbl" for="ro-reason">Reason (required)</label><textarea id="ro-reason" class="field" bind:value={reopenReason}></textarea></div>
              <div><Btn kind="pri" lock disabled={!reopenIds.length || !reopenReason.trim()} onclick={() => (reopening = true)}>Reopen {reopenIds.length || ''} {reopenIds.length === 1 ? 'task' : 'tasks'}</Btn></div>
            </div>
          </Panel>
        {/if}
        <Panel level={3} title="Your decision" cls="decide">
          <div class="pb stack">
            {#if r.requires_override_reason}<div class="text-[13px] font-semibold text-st-warning">{count(r, 'failed') ? `${plural(count(r, 'failed'), 'validator')} failed, so approving needs a written reason.` : 'Approving needs a written reason: see the recommendation.'}</div>{/if}
            {#if r.findings.length}<div class="text-[13px] font-semibold text-st-attention">{plural(r.findings.length, 'change')} outside the plan. Read the list under "Outside the plan" before you decide.</div>{/if}
            {#if stale}<Note kind="warn" role="alert"><b>The review changed after you opened it.</b> Nothing was decided. Read it again before you decide.</Note>{/if}
            {#if apr?.state === 'pending' && apr.id !== outcome?.id}
              {#key apr.id + apr.subject_hash}
                <ReviewDecision approval={apr} overrideRequired={r.requires_override_reason} {active} outdated={!!both.error} ondecided={(x) => { stale = false; outcome = { id: x.id, state: x.state }; void both.reload(); }} onstale={() => { stale = true; void both.reload(); }} />
              {/key}
            {:else if outcome || apr}{@const result = outcome?.state ?? apr!.state}<Note kind={result === 'approved' ? 'ok' : 'dng'} role="status">This phase review was <b>{result}</b>.</Note>
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
        done="Reopened"
        onconfirm={async (p) => { await a.runElevated(p, (secret) => a.api.reopenTasks(rid, reopenIds, reopenReason.trim(), secret)); reopening = false; reopened = true; reopenIds = []; reopenReason = ''; void res.reload(); }}
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
        done="Confirmed"
        onconfirm={async (p) => { await a.runElevated(p, (secret) => a.api.confirmValidation(rid, q.v.id, q.i, secret)); asking = null; toast.success('Checklist item confirmed'); void res.reload(); }}
        oncancel={() => (asking = null)}
      />
    {/if}
  {/snippet}
</View>
