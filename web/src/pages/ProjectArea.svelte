<script lang="ts">
  // One project, five panes (Overview, Requirements, Plan, Review, Guard). The area loads once: every pane is mounted when the
  // area opens and stays mounted, so changing pane is a crossfade of data that is already there, not a new page and not a new
  // request. The area is rebuilt only when another project is opened.
  import { setContext } from 'svelte';
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { route, href } from '../lib/router.svelte';
  import ProjectTabs from '../lib/ProjectTabs.svelte';
  import Note from '../lib/Note.svelte';
  import Project from './Project.svelte';
  import Requirements from './Requirements.svelte';
  import Plan from './Plan.svelte';
  import PhaseReview from './PhaseReview.svelte';
  import Connect from './Connect.svelte';
  import ForkDialog from '../lib/ForkDialog.svelte';
  import Btn from '../lib/Btn.svelte';
  import TopRow from '../lib/TopRow.svelte';
  import { shellData } from '../lib/shell';
  import { forkText } from '../lib/waiting.svelte';
  import Badge from '../lib/Badge.svelte';
  import { toast } from 'svelte-sonner';
  import { ApiError } from '../api/client';

  setContext('area', true);
  const a = app();
  const PANE: Record<string, 'overview' | 'requirements' | 'plan' | 'review' | 'guard'> = {
    project: 'overview', requirements: 'requirements', plan: 'plan', review: 'review', guard: 'guard',
  };
  const id = $derived(route.params.id!);
  const pane = $derived(PANE[route.name] ?? 'overview');
  const res = resource(() => a.api.project(id), ['project.changed', 'phase.changed', 'workflow.changed']);
  const p = $derived(res.data);
  const { projects } = shellData();
  const forkLabel = $derived(forkText(projects.data?.items, p?.fork_of));
  let forking = $state(false);
  // The Review pane follows the phase that is being reviewed, and keeps it while the human revises it after a rejection.
  const awaiting = $derived(p?.phases.find((x) => x.state === 'awaiting_phase_review') ?? p?.phases.find((x) => x.state === 'revision_required'));
  // The review stays on screen after the decision, until the human leaves the Review tab: they see what they decided.
  let held = $state<string>();
  $effect(() => {
    if (awaiting) held = awaiting.id;
    else if (pane !== 'review') held = undefined;
  });
  const reviewing = $derived(awaiting?.id ?? held);
  // With a phase under review the head gives way and the tabs move to the first line: the phase title leads the page.
  const compact = $derived(pane === 'review' && !!reviewing);
  let asking = $state(false);
  let error = $state('');
  async function requestPlanning() {
    asking = true;
    error = '';
    try {
      await a.api.requestPlanning(id);
      toast.success('Planning requested');
      await res.reload();
    } catch (x) {
      error = x instanceof ApiError ? `${x.message} ${x.nextStep}` : String(x);
    } finally {
      asking = false;
    }
  }
</script>

{#if p}
  <TopRow roomy={compact}>
    {#snippet crumb()}
      <nav class="crumb" aria-label="Breadcrumb">{#if compact}<a href={href('/projects/' + id)}>← {p.name}</a>{:else}<a href={href('/projects')}>← Projects</a>{/if}</nav>
    {/snippet}
    {#snippet actions()}
      {#if compact}
        <ProjectTabs {id} current={pane} project={p} />
      {:else}
        <div class="flex flex-wrap gap-2.5">
          <Btn sm onclick={() => (forking = true)}>Fork</Btn>
          {#if pane === 'overview'}<Btn disabled={asking || p.planning.request?.state === 'waiting'} title={p.planning.request?.state === 'waiting' ? 'A planning request is already waiting for a harness' : undefined} onclick={requestPlanning}>Request planning</Btn>{/if}
        </div>
      {/if}
    {/snippet}
  </TopRow>
  {#if error}<Note kind="dng" role="alert">{error}</Note>{/if}
  {#if compact}
  <h1 class="sr-only">{p.name}</h1>
  {:else}
  <div class="phead">
    <div class="min-w-0">
      <h1>{p.name}</h1>
      <div class="meta">
        <span class="mono">{p.path}</span>
        {#if p.branch}<span class="tag">branch {p.branch}</span>{/if}
        <span class="tag">guard: {p.guard_policy}</span>
        {#if p.active}<span class="tag">Active Context</span>{/if}
        {#if forkLabel}<Badge state="fork" text={forkLabel} />{/if}
      </div>
    </div>
    <ProjectTabs {id} current={pane} project={p} />
  </div>
  {/if}
{/if}
<div class="panes">
  <section class="pane" data-active={pane === 'overview'} aria-hidden={pane !== 'overview'} inert={pane !== 'overview'} aria-label="Overview"><div class="flex min-w-0 flex-col gap-6"><Project /></div></section>
  <section class="pane" data-active={pane === 'requirements'} aria-hidden={pane !== 'requirements'} inert={pane !== 'requirements'} aria-label="Requirements"><div class="flex min-w-0 flex-col gap-6"><Requirements /></div></section>
  <section class="pane" data-active={pane === 'plan'} aria-hidden={pane !== 'plan'} inert={pane !== 'plan'} aria-label="Plan"><div class="flex min-w-0 flex-col gap-6"><Plan /></div></section>
  <section class="pane" data-active={pane === 'review'} aria-hidden={pane !== 'review'} inert={pane !== 'review'} aria-label="Review">
    <div class="flex min-w-0 flex-col gap-6">
      {#if reviewing}{#key reviewing}<PhaseReview phaseId={reviewing} />{/key}
      {:else if p}<Note kind="neu" icon="ok"><b>Nothing to review.</b> No phase of this project is waiting for review.</Note>{/if}
    </div>
  </section>
  <section class="pane" data-active={pane === 'guard'} aria-hidden={pane !== 'guard'} inert={pane !== 'guard'} aria-label="Guard"><div class="flex min-w-0 flex-col gap-6"><Connect /></div></section>
</div>

{#if forking && p}
  <ForkDialog project={p} others={projects.data?.items ?? []} oncancel={() => (forking = false)} ondone={() => { forking = false; void res.reload(); void projects.reload(); }} />
{/if}
