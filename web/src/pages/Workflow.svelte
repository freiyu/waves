<script lang="ts">
  import { toast } from 'svelte-sonner';
  // One page for both routes: /projects/:id/workflow picks a version and
  // redirects, /workflow/:vid shows it.
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { route, href, go } from '../lib/router.svelte';
  import { ApiError } from '../api/client';
  import type { Validator, WorkflowTask } from '../api/models';
  import ProjectHead from '../lib/ProjectHead.svelte';
  import SectionHead from '../lib/SectionHead.svelte';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Btn from '../lib/Btn.svelte';
  import Badge from '../lib/Badge.svelte';
  import Table from '../lib/Table.svelte';
  import View from '../lib/View.svelte';
  import type { Column } from '../lib/table';

  const a = app();
  const vid = $derived(route.params.vid);
  // Without a version id, find the newest draft (or the current version) and go there.
  const pick = resource(async () => {
    if (route.params.vid) return null;
    const w = await a.api.workflow(route.params.id!);
    const target = w.versions.find((v) => v.state === 'draft') ?? w.versions.find((v) => v.id === w.current_id) ?? w.versions[0];
    if (target) location.replace('#/workflow/' + target.id); // replace, so Back does not return to this redirect
    return w;
  });
  const ver = resource(async () => (route.params.vid ? a.api.workflowVersion(route.params.vid) : null), ['workflow.changed']);
  const info = resource(async () => (ver.data ? a.api.workflow(ver.data.project_id) : null), ['workflow.changed']);
  let error = $state('');

  const taskCols: Column<WorkflowTask>[] = [
    { key: 'code', label: 'Task', cell: tcode },
    { key: 'title', label: 'Title', text: (r) => r.title },
    { key: 'mode', label: 'Mode', text: (r) => r.mode },
    { key: 'acc', label: 'Acceptance', text: (r) => `${r.acceptance.length} criteria` },
  ];
  const valCols: Column<Validator>[] = [
    { key: 'kind', label: 'Type', text: (r) => r.kind },
    { key: 'name', label: 'Name', text: (r) => r.name },
    { key: 'cmd', label: 'Exact command or items', cell: vcmd },
    { key: 'to', label: 'Timeout', text: (r) => (r.timeout_s ? `${r.timeout_s} s` : 'none') },
  ];

  async function submit(id: string) {
    error = '';
    try {
      const r = await a.api.submitWorkflow(id);
      toast.success('Workflow submitted for approval');
      go('/approvals/' + r.approval_id);
    } catch (x) {
      error = x instanceof ApiError ? `${x.message} ${x.nextStep}` : String(x);
    }
  }
  async function revise(id: string) {
    error = '';
    try {
      const v = await a.api.newWorkflowVersion(id);
      toast.success('New workflow version created');
      go('/workflow/' + v.id);
    } catch (x) {
      error = x instanceof ApiError ? `${x.message} ${x.nextStep}` : String(x);
    }
  }
</script>

{#snippet tcode(r: WorkflowTask)}<span class="mono">{r.code}</span>{/snippet}
{#snippet vcmd(r: Validator)}{#if r.command}<span class="mono">{r.command}</span>{:else}{r.items?.join('; ')}{/if}{/snippet}

{#if !vid}
  <ProjectHead id={route.params.id!} current="workflow" />
  <View res={pick} label="the workflow">
    {#snippet children(w)}<Note kind="neu">This project has no workflow version yet. Ask a harness to plan first: <a href={href('/projects/' + route.params.id + '/plan')}>Plan</a>.</Note>{/snippet}
  </View>
{:else}
  <View res={ver} label="this workflow version">
    {#snippet children(v)}
      {#if v}
        <ProjectHead id={v.project_id} current="workflow" name={v.project_name} />
        <SectionHead title="Workflow review · version {v.number}" sub="content hash {v.content_hash}{v.proposal_id ? ' · from proposal ' + v.proposal_id : ''}">
          {#snippet right()}
            <Badge state={v.state} />
            {#if v.state === 'draft'}<Btn kind="pri" onclick={() => submit(v.id)}>Submit for approval</Btn>{/if}
            {#if v.state === 'approved' || v.state === 'rejected'}<Btn onclick={() => revise(v.id)}>New version from this</Btn>{/if}
          {/snippet}
        </SectionHead>
        {#if error}<Note kind="dng" icon="fail" role="alert">{error}</Note>{/if}
        {#if v.state === 'awaiting_approval'}<Note kind="att" icon="wait">This version is waiting for approval. <a href={href('/approvals')}>Open the approval inbox</a>.</Note>{/if}
        <Note kind="warn"><b>Waves will run these commands</b> on your machine when a phase reaches validation. Approving this workflow approves exactly the strings below. Changing one creates a new version.</Note>
        {#each v.phases as ph (ph.id)}
          <Panel title="{ph.code} {ph.title}" level={3}>
            <div class="pb stack">
              <div class="muted">{ph.objective}</div>
              <h4>Tasks <span class="count">{ph.tasks.length}</span></h4>
              <div class="boxed"><Table label="{ph.code} tasks" rows={ph.tasks} columns={taskCols} rowKey={(r) => r.code} /></div>
              <h4>Validators <span class="count">{ph.validators.length}</span></h4>
              <div class="boxed"><Table label="{ph.code} validators" rows={ph.validators} columns={valCols} rowKey={(r) => r.id} /></div>
            </div>
          </Panel>
        {:else}<Note kind="neu">This version has no phases.</Note>{/each}
        {#if info.data && info.data.versions.length > 1}
          <Panel title="Versions" level={3}>
            {#each info.data.versions as x (x.id)}
              <div class="list-row"><a href={href('/workflow/' + x.id)} aria-current={x.id === v.id ? 'page' : undefined}>Version {x.number}</a><Badge state={x.state} /><span class="mono muted small">{x.content_hash}</span>{#if x.id === info.data.current_id}<span class="tag">current</span>{/if}</div>
            {/each}
          </Panel>
        {/if}
      {/if}
    {/snippet}
  </View>
{/if}
