<script lang="ts">
  import { toast } from 'svelte-sonner';
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { route, href, go } from '../lib/router.svelte';
  import { clock, plural } from '../lib/format';
  import PageHead from '../lib/PageHead.svelte';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Btn from '../lib/Btn.svelte';
  import Badge from '../lib/Badge.svelte';
  import View from '../lib/View.svelte';
  import Confirm from '../lib/Confirm.svelte';

  const a = app();
  const res = resource(() => a.api.execution(route.params.id!), ['execution.changed']);
  type D = 'fail' | 'complete_partial' | 'retry_fresh';
  let ask = $state<D | 'orphans' | null>(null);
  const COPY: Record<D, { title: string; what: string; confirm: string; body: string; done: string; cmd?: string; danger?: boolean }> = {
    complete_partial: { title: 'Accept partial result', what: 'Accept', confirm: 'Accept partial result', body: 'Marks the execution Completed (partial) and sends the task to review. You still decide the result.', done: 'Partial result accepted and the task sent to review' },
    retry_fresh: { title: 'Retry the task', what: 'Retry', confirm: 'Retry task', body: 'The task goes back to Ready for an agent to claim. Waves does not change the workspace: files the agent wrote stay as they are, and this diff stays for reference.', done: 'Task sent back to Ready' },
    fail: { title: 'Mark the execution failed', what: 'Mark failed', confirm: 'Mark failed', body: 'The execution is recorded as Failed and the diff stays available. The task stays Blocked: retry it later, or cancel it from a terminal with', cmd: 'waves task cancel <task id> "<reason>"', danger: true, done: 'Execution marked failed' },
  };
  async function resolve(e: { id: string }, d: D) {
    await a.api.resolve(e.id, d);
    ask = null;
    toast.success(COPY[d].done);
    go('/executions/' + e.id);
  }
</script>

<View {res} label="the recovery view">
  {#snippet children(e)}
    {#if !e.recovery}
      <PageHead title="Recovery · {e.task_code}" sub="{e.id} · {e.project_name} · {e.task_title}" crumbs={[{ label: 'Executions', href: href('/executions') }, { label: e.id, href: href('/executions/' + e.id) }, { label: 'Recovery' }]} />
      <Note kind="neu">This execution needs no recovery decision. <a href={href('/executions/' + e.id)}>Back to the execution</a></Note>
    {:else}
      {@const r = e.recovery}
      <PageHead title="Recovery · {e.task_code} needs your decision" sub="{e.id} · {e.project_name} · {e.task_title}" crumbs={[{ label: 'Executions', href: href('/executions') }, { label: e.id, href: href('/executions/' + e.id) }, { label: 'Recovery' }]}>
        {#snippet above()}<Badge state="blocked" text="Blocked · recovery required" /><span class="mono muted">{e.id}</span>{/snippet}
      </PageHead>
      <Note kind="warn"><b>{r.reason}</b> The agent may already have changed files, so you decide what happens next.</Note>
      <div class="cols">
        <div class="stack-lg">
          <Panel title="What was kept">
            <div class="pb"><dl class="kv">
              <dt>Partial diff</dt><dd>{plural(r.partial_files, 'file')} · +{r.partial_added} −{r.partial_removed} <a href={href('/executions/' + e.id)}>Inspect diff</a></dd>
              <dt>Log</dt><dd>{plural(r.log_lines, 'line')}, last at {clock(r.last_log_at)}</dd>
              <dt>Workspace</dt><dd>{e.workspace_locked ? 'locked for this execution until you decide' : 'free'}</dd>
              <dt>Orphan check</dt><dd>{#if r.orphan}{plural(r.orphan.count, 'process', 'processes')} from this session still running: <span class="mono">{r.orphan.command}</span> <button type="button" class="linkbtn" onclick={() => (ask = 'orphans')}>Stop it</button>{:else}No process from this session is left.{/if}</dd>
            </dl></div>
          </Panel>
          <Note kind="neu"><b>Resume the session.</b> Waves cannot resume a session it did not start.{#if r.resume_session}{' '}To continue it yourself, run <span class="mono">claude --resume {r.resume_session}</span> in the project folder.{/if} Or choose Retry.</Note>
          <Panel title="Choose one">
            {#each ['retry_fresh', 'complete_partial', 'fail'] as d (d)}
              {@const c = COPY[d as D]}
              <div class="list-row">
                <div><b>{c.title}</b><div class="muted small">{c.body}{#if c.cmd} <span class="mono">{c.cmd}</span>{/if}</div></div>
                <span class="sp"></span>
                <Btn kind={d === 'retry_fresh' ? 'pri' : c.danger ? 'dng' : ''} onclick={() => (ask = d as D)}>{c.what}</Btn>
              </div>
            {/each}
          </Panel>
        </div>
        <Panel title="Timeline"><ul class="timeline">{#each [...r.timeline].reverse() as t}<li><span class="dot"></span><span class="t">{t.detail ?? t.state}</span><span class="mono muted small">{clock(t.at)}</span></li>{/each}</ul></Panel>
      </div>
      {#if ask === 'orphans'}
        <Confirm title="Stop the leftover process?" confirm="Stop it" danger oncancel={() => (ask = null)} onconfirm={async () => { await a.api.stopOrphans(e.id); ask = null; toast.success(`Stop signal sent to ${r.orphan?.command ?? 'the process'}`); await res.reload(); }}>
          <div>Waves sends a stop signal to <span class="mono">{r.orphan?.command}</span>. The action is recorded in the audit log.</div>
        </Confirm>
      {:else if ask}
        <Confirm title={COPY[ask].title + '?'} confirm={COPY[ask].confirm} danger={COPY[ask].danger} oncancel={() => (ask = null)} onconfirm={() => resolve(e, ask as D)}>
          <div>{COPY[ask].body}{#if COPY[ask].cmd} <span class="mono">{COPY[ask].cmd}</span>{/if}</div>
        </Confirm>
      {/if}
    {/if}
  {/snippet}
</View>
