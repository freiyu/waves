<script lang="ts">
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { shellData } from '../lib/shell';
  import { href } from '../lib/router.svelte';
  import { ago, shortClock, plural } from '../lib/format';
  import { waitingOf, firstAndMore } from '../lib/waiting.svelte';
  import ReleaseLease from '../lib/ReleaseLease.svelte';
  import WaitingCallout from '../lib/WaitingCallout.svelte';
  import type { ApprovalSummary } from '../api/models';
  import PageHead from '../lib/PageHead.svelte';
  import Panel from '../lib/Panel.svelte';
  import Icon from '../lib/Icon.svelte';
  import Btn from '../lib/Btn.svelte';
  import IconBtn from '../lib/IconBtn.svelte';
  import Badge from '../lib/Badge.svelte';
  import View from '../lib/View.svelte';
  import DecideFlow from '../lib/DecideFlow.svelte';
  import { flashOnChange } from '../lib/flash';

  const a = app();
  const { status, projects } = shellData();
  const execs = resource(() => a.api.executions(), ['execution.changed']);
  const pending = resource(() => a.api.approvals('pending'), ['approval.changed']);

  // Where the decision is taken: a phase review in the project's Review pane, a workflow in its Plan pane, anything else on its own page.
  const subjectLink = (x: ApprovalSummary) =>
    x.category === 'phase_completion' ? `/projects/${x.project_id}/review` : x.category === 'workflow' ? `/projects/${x.project_id}/plan` : `/projects/${x.project_id}`;
  const badgeText = (x: ApprovalSummary) => (x.category === 'workflow' ? 'Proposal ready' : undefined);

  const broken = $derived(status.data?.audit.state === 'broken');
  let deciding = $state<{ id: string; mode: 'approve' | 'reject' } | null>(null);
  let releasing = $state<{ id: string; title: string; code: string } | null>(null);
  const LIVE = ['running', 'starting', 'waiting_approval', 'paused'];
</script>

<View res={execs} label="the overview">
  {#snippet children(e)}
    {@const wait = waitingOf(e.items)}
    {@const running = e.items.filter((x) => (x.state === 'running' || x.state === 'starting') && !wait.has(x.id))}
    {@const waits = e.items.filter((x) => wait.has(x.id))}
    {@const stopped = e.items.filter((x) => x.state === 'interrupted' || x.state === 'disconnected')}
    {@const active = [...e.items.filter((x) => LIVE.includes(x.state)), ...stopped].slice(0, 6)}
    {@const items = pending.data?.items ?? []}
    {@const reviews = items.filter((x) => x.category === 'phase_completion').length}
    {@const flows = items.filter((x) => x.category === 'workflow').length}
    <PageHead title="Overview">
      {#snippet right()}<Btn href={href('/projects?register')}>Register</Btn>{/snippet}
    </PageHead>

    <div class="stats stagger">
      <button type="button" class="stat" onclick={() => document.getElementById('decisions')?.scrollIntoView({ behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth', block: 'start' })}><div class="l"><span class="inline-flex size-6 items-center justify-center rounded-md bg-st-attention-soft text-st-attention"><Icon name="wait" size={14} /></span>Needs your decision</div><div class="n">{items.length}</div><div class="muted">{items.length ? [reviews && plural(reviews, 'phase review'), flows && plural(flows, 'workflow'), items.length - reviews - flows > 0 && plural(items.length - reviews - flows, 'other decision')].filter(Boolean).join(', ') : 'none'}</div></button>
      <a class="stat" href={href('/executions')}><div class="l"><span class="inline-flex size-6 items-center justify-center rounded-md bg-st-active-soft text-st-active"><Icon name="run" size={14} /></span>Running now</div><div class="n">{running.length}</div><div class="muted">{running.length ? `across ${plural(new Set(running.map((x) => x.project_id)).size, 'project')}` : 'none'}</div></a>
      <a class="stat" href={href('/executions')}><div class="l"><span class="inline-flex size-6 items-center justify-center rounded-md bg-st-waiting-soft text-st-waiting"><Icon name="prompt" size={14} /></span>Waiting for instruction</div><div class="n">{waits.length}</div><div class="muted">{firstAndMore(waits)}</div></a>
      <a class="stat" href={href('/executions')}><div class="l"><span class="inline-flex size-6 items-center justify-center rounded-md bg-st-warning-soft text-st-warning"><Icon name="warn" size={14} /></span>Interrupted</div><div class="n">{stopped.length}</div><div class="muted">{firstAndMore(stopped)}</div></a>
    </div>

    <div class="cols">
      <div class="stack-lg">
        <View res={pending} label="the decisions">
          {#snippet children(p)}
            <span id="decisions" class="absolute"></span>
            <Panel title="Needs your decision" count={p.items.length}>
              {#if p.items.length}
                <div class="grid-head drow" aria-hidden="true"><span>Decision</span><span>Status</span><span></span></div>
                <ul class="items stagger" aria-label="Needs your decision">
                  {#each p.items as x (x.id)}
                    <li class="drow">
                      <div class="min-w-0"><a class="tlink" href={href(subjectLink(x))}>{x.category === 'phase_completion' ? `${x.subject} · review` : x.subject}</a><div class="muted small truncate">{x.project_name} · {x.category_label} · {ago(x.requested_at)}</div></div>
                      <div><Badge state="awaiting_approval" text={badgeText(x)} /></div>
                      <div class="acts"><IconBtn icon="lock" lock kind="pri" label="Approve" context={x.subject} disabled={broken} onclick={() => (deciding = { id: x.id, mode: 'approve' })} /><IconBtn icon="x" kind="dng" label="Reject" context={x.subject} disabled={broken} onclick={() => (deciding = { id: x.id, mode: 'reject' })} /></div>
                    </li>
                  {/each}
                </ul>
              {:else}
                <div class="empty" role="status"><b>Nothing needs you.</b> No approval is waiting.</div>
              {/if}
            </Panel>
          {/snippet}
        </View>

        <Panel title="Active executions">
          {#snippet right()}<a class="font-semibold no-underline" href={href('/executions')}>View all</a>{/snippet}
          {#if active.length}
            <div class="grid-head erow" aria-hidden="true"><span>Task</span><span>Status</span><span class="num">Started</span><span class="num">Duration</span></div>
            <ul class="items stagger" aria-label="Active executions">
              {#each active as r (r.id)}
                <li class="erow" use:flashOnChange={JSON.stringify(r)}>
                  <div class="min-w-0"><a class="tlink" href={href('/executions/' + r.id)}>{r.task_title}</a><div class="mono muted small truncate">{r.project_name} · {r.id}</div></div>
                  <div><Badge state={wait.has(r.id) ? 'waiting_for_instruction' : r.state} /></div>
                  <span class="num">{r.started_at ? shortClock(r.started_at) : '—'}</span>
                  <span class="num">{r.duration_s != null ? `${Math.floor(r.duration_s / 60)}m ${String(r.duration_s % 60).padStart(2, '0')}s` : '—'}</span>
                </li>
              {/each}
            </ul>
          {:else}
            <div class="empty">No execution is active. They appear here after a harness claims a task.</div>
          {/if}
        </Panel>
      </div>

      <div class="stack-lg">
        <WaitingCallout items={waits} since={wait} onrelease={(w) => (releasing = { id: w.id, title: w.task_title, code: w.task_code })} />
        <View res={projects} label="projects">
          {#snippet children(p)}
            <Panel title="Projects">
              {#snippet right()}<a class="font-semibold no-underline" href={href('/projects')}>All</a>{/snippet}
              <ul class="items" aria-label="Projects">
                {#each p.items as r (r.id)}
                  <li class="li block">
                    <div class="row"><div class="t"><a class="tlink" href={href('/projects/' + r.id)}>{r.name}</a><span class="muted small">{r.active_phase ? `${r.active_phase.code} ${r.active_phase.title}` : 'no workflow yet'} · {r.progress_pct}%</span></div></div>
                    <div class="bar mt-2" role="progressbar" aria-valuenow={r.progress_pct} aria-valuemin="0" aria-valuemax="100" aria-label="{r.name} progress"><i style="width:{r.progress_pct}%"></i></div>
                  </li>
                {/each}
              </ul>
            </Panel>
          {/snippet}
        </View>
      </div>
    </div>
  {/snippet}
</View>

{#if deciding}
  <DecideFlow approvalId={deciding.id} mode={deciding.mode} oncancel={() => (deciding = null)} ondone={() => { deciding = null; void pending.reload(); void status.reload(); }} />
{/if}
{#if releasing}
  <ReleaseLease target={releasing} oncancel={() => (releasing = null)} ondone={() => { releasing = null; void execs.reload(); }} />
{/if}
