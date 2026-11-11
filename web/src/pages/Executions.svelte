<script lang="ts">
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { href, go } from '../lib/router.svelte';
  import { duration, shortClock } from '../lib/format';
  import { label } from '../lib/status';
  import type { ExecutionSummary } from '../api/models';
  import PageHead from '../lib/PageHead.svelte';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Btn from '../lib/Btn.svelte';
  import Badge from '../lib/Badge.svelte';
  import Table from '../lib/Table.svelte';
  import View from '../lib/View.svelte';
  import type { Column, Filter } from '../lib/table';
  import RowMenu from '../lib/RowMenu.svelte';
  import { copyText } from '../lib/copy';

  const a = app();
  const res = resource(() => a.api.executions(), ['execution.changed']);
  const cols: Column<ExecutionSummary>[] = [
    { key: 'id', label: 'Execution', sort: (r) => r.id, cell: eid },
    { key: 'task', label: 'Task', text: (r) => r.task_title, sort: (r) => r.task_title },
    { key: 'project', label: 'Project', text: (r) => r.project_name, sort: (r) => r.project_name },
    { key: 'harness', label: 'Harness', text: (r) => r.harness },
    { key: 'state', label: 'State', sort: (r) => r.state, cell: st },
    { key: 'started', label: 'Started', sort: (r) => r.started_at ?? '', text: (r) => (r.started_at ? shortClock(r.started_at) : 'none') },
    { key: 'dur', label: 'Duration', sort: (r) => r.duration_s ?? -1, text: (r) => duration(r.duration_s) },
    { key: 'menu', label: 'More', cell: menu },
  ];
  const filters: Filter<ExecutionSummary>[] = [
    { id: 'state', label: 'State', chips: true, row: true, pick: (r) => r.state, options: ['running', 'waiting_approval', 'disconnected', 'interrupted', 'failed', 'queued', 'completed'].map((v) => ({ value: v, label: label(v) })) },
    { id: 'project', label: 'Project', pick: (r) => r.project_name },
    { id: 'harness', label: 'Harness', pick: (r) => r.harness },
  ];
</script>

{#snippet eid(r: ExecutionSummary)}<a class="mono" href={href('/executions/' + r.id)}>{r.id}</a>{/snippet}
{#snippet menu(r: ExecutionSummary)}<RowMenu label="Actions for {r.id}" items={[{ label: 'Open execution', icon: 'exec', run: () => go('/executions/' + r.id) }, { label: 'Copy id', icon: 'copy', run: () => copyText(r.id) }]} />{/snippet}
{#snippet st(r: ExecutionSummary)}<Badge state={r.state} text={r.state_detail ? `${label(r.state)} · ${r.state_detail}` : undefined} />{/snippet}

<PageHead title="Executions" sub="All projects. Switching Active Context never hides or stops these." />
<View {res} label="executions">
  {#snippet children(d)}
    <Panel>
      <Table label="Executions" rows={d.items} columns={cols} {filters} rowKey={(r) => r.id} search={{ placeholder: 'Search task or id', text: (r) => `${r.id} ${r.task_title} ${r.task_code}` }} initialSort={{ key: 'started', dir: 'desc' }}>
        {#snippet empty()}
          <b class="text-fg">No executions yet</b>
          <div class="mt-1">Executions appear here after an agent claims a task. Approve a phase first, then start Claude Code in the project.</div>
          <div class="mt-3"><Btn href={href('/approvals')}>Open approvals</Btn></div>
        {/snippet}
      </Table>
    </Panel>
    {@const queued = d.items.filter((x) => x.state === 'queued')}
    {#if queued.length}
      <Note kind="neu"><b>{queued.length} queued.</b> {queued.length === 1 ? 'Open it to see why it has not started:' : 'Open any of them to see why they have not started:'} {#each queued.slice(0, 3) as q, i}{#if i}, {/if}<a class="mono" href={href('/executions/' + q.id)}>{q.id}</a>{/each}{queued.length > 3 ? ` and ${queued.length - 3} more` : ''}.</Note>
    {/if}
  {/snippet}
</View>
