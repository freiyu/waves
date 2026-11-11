<script lang="ts">
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { shellData } from '../lib/shell';
  import { href } from '../lib/router.svelte';
  import { ago, dayLabel, duration, plural, shortClock } from '../lib/format';
  import { label } from '../lib/status';
  import type { AttentionItem, Project } from '../api/models';
  import PageHead from '../lib/PageHead.svelte';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Icon from '../lib/Icon.svelte';
  import { flashOnChange } from '../lib/flash';
  import Btn from '../lib/Btn.svelte';
  import Badge from '../lib/Badge.svelte';
  import View from '../lib/View.svelte';

  const a = app();
  const { status, projects } = shellData();
  const execs = resource(() => a.api.executions(), ['execution.changed']);
  const audit = resource(() => a.api.audit(), ['approval.changed', 'execution.changed', 'task.changed', 'phase.changed']);

  const link = (t: string, id: string) => (t === 'approval' ? `/approvals/${id}` : t === 'execution' ? `/executions/${id}` : '/activity');
  const badgeKey = (i: AttentionItem) => (i.kind === 'approval' ? 'awaiting_approval' : i.kind === 'execution_failed' ? 'failed' : i.kind === 'execution_disconnected' ? 'disconnected' : i.kind === 'task_blocked' ? 'blocked' : 'broken');
  const idle = (p: Project) => !p.running_executions && !p.failed_executions && !p.pending_approvals;
  const live = ['running', 'waiting_approval', 'queued', 'disconnected', 'paused', 'starting'];

</script>

<View res={status} label="the overview">
  {#snippet children(s)}
    {@const approvals = s.needs_attention.filter((i) => i.kind === 'approval')}
    {@const reviews = approvals.filter((i) => i.label === 'Phase Completion').length}
    {@const failed = [...new Set(s.needs_attention.filter((i) => i.kind === 'execution_failed').map((i) => i.project_name))].join(', ')}
    <PageHead
      title="Overview"
      sub="{dayLabel()} · {plural(projects.data?.items.length ?? 0, 'project')}"
    >
      {#snippet right()}<Btn href={href('/projects?register')}><Icon name="plus" size={14} />Register project</Btn>{/snippet}
    </PageHead>

    <div class="stats">
      <div class="stat"><div class="l text-st-attention"><Icon name="wait" size={14} />Awaiting you</div><div class="n">{approvals.length}</div><div class="muted">{approvals.length - reviews} {approvals.length - reviews === 1 ? 'approval' : 'approvals'}, {reviews} phase {reviews === 1 ? 'review' : 'reviews'}</div></div>
      <div class="stat"><div class="l text-st-active"><Icon name="run" size={14} />Running</div><div class="n">{s.counts.running}</div><div class="muted">{s.counts.disconnected} disconnected</div></div>
      <div class="stat"><div class="l text-st-danger"><Icon name="fail" size={14} />Failed</div><div class="n">{s.counts.failed}</div><div class="muted">{failed || 'none'}</div></div>
      <div class="stat"><div class="l"><Icon name="folder" size={14} />Projects</div><div class="n">{projects.data?.items.length ?? 0}</div><div class="muted">{projects.data?.items.filter(idle).length ?? 0} idle</div></div>
    </div>

    <div class="cols">
      <div class="stack-lg">
        {#if s.needs_attention.length}
          <Panel title="Needs attention" count={s.needs_attention.length}>
            {#snippet right()}<span class="muted">Oldest first</span>{/snippet}
            {#if s.next_step}
              <div class="safe" role="status"><Icon name="wait" /><span><b>Safe next step:</b> {s.next_step.text}{#if s.next_step.ref_type && s.next_step.ref_id} <a href={href(link(s.next_step.ref_type, s.next_step.ref_id))}>Open</a>{/if}</span></div>
            {/if}
            <ul class="items" aria-label="Needs attention">
              {#each s.needs_attention as r (r.ref_id + r.kind)}
                <li class="li">
                  <Badge state={badgeKey(r)} text={r.label} />
                  <div class="t"><b>{r.subject}</b><span class="muted small">{r.project_name ?? ''} · {ago(r.since)}</span></div>
                  <Btn sm href={href(link(r.ref_type, r.ref_id))}>{r.kind === 'approval' ? 'Review' : 'Open'}</Btn>
                </li>
              {/each}
            </ul>
          </Panel>
        {:else}
          <Note kind="ok" icon="ok" role="status"><b>Nothing needs you.</b> No approval is waiting and no execution has failed.</Note>
        {/if}
        <View res={execs} label="executions">
          {#snippet children(e)}
            {@const running = e.items.filter((x) => live.includes(x.state))}
            <Panel title="Running now">
              {#snippet right()}<a class="inline-flex items-center gap-1.5 font-semibold no-underline" href={href('/executions')}>All executions <Icon name="arrow" size={14} /></a>{/snippet}
              <ul class="items" aria-label="Active executions">
                {#each running as r (r.id)}
                  <li class="li" use:flashOnChange={JSON.stringify(r)}>
                    <div class="t"><a class="title" href={href('/executions/' + r.id)}>{r.task_title}</a><span class="muted small"><span class="mono">{r.id}</span> · {r.project_name} · {r.harness}</span></div>
                    <Badge state={r.state} text={r.state_detail ? `${label(r.state)} · ${r.state_detail}` : undefined} />
                    <span class="mono muted">{duration(r.duration_s)}</span>
                  </li>
                {:else}
                  <li class="empty">No execution is active. They appear here after an agent claims a task.</li>
                {/each}
              </ul>
            </Panel>
          {/snippet}
        </View>
      </div>

      <div class="stack-lg">
        <View res={projects} label="projects">
          {#snippet children(p)}
            <Panel title="Projects">
              {#snippet right()}<a class="font-semibold no-underline" href={href('/projects')}>All</a>{/snippet}
              <ul class="items" aria-label="Projects">
                {#each p.items as r (r.id)}
                  <li class="li block">
                    <div class="row"><div class="t"><a class="title" href={href('/projects/' + r.id)}>{r.name}</a><span class="muted small">{r.active_phase ? `${r.active_phase.code} ${r.active_phase.title}` : 'no workflow yet'} · {r.progress_pct}%</span></div>
                      <span class="muted small">{[r.running_executions && `${r.running_executions} running`, r.failed_executions && `${r.failed_executions} failed`, r.pending_approvals && `${r.pending_approvals} pending`].filter(Boolean).join(' · ') || 'idle'}</span></div>
                    <div class="bar mt-2" role="progressbar" aria-valuenow={r.progress_pct} aria-valuemin="0" aria-valuemax="100" aria-label="{r.name} progress"><i style="width:{r.progress_pct}%"></i></div>
                  </li>
                {/each}
              </ul>
            </Panel>
          {/snippet}
        </View>
        <View res={audit} label="recent activity">
          {#snippet children(au)}
            <Panel title="Recent activity">
              {#snippet right()}<a class="font-semibold no-underline" href={href('/activity')}>View all</a>{/snippet}
              <ul class="timeline">
                {#each au.items.slice(0, 6) as r (r.seq)}
                  <li><span class="dot"></span><span class="t"><span class="mono">{r.event}</span> {r.subject}</span><span class="mono muted small">{shortClock(r.at)}</span></li>
                {/each}
              </ul>
            </Panel>
          {/snippet}
        </View>
      </div>
    </div>
  {/snippet}
</View>
