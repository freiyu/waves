<script lang="ts">
  // One project, five panes (Overview, Requirements, Plan, Review, Guard). The area loads once: every pane is mounted when the
  // area opens and stays mounted, so changing pane is a crossfade of data that is already there, not a new page and not a new
  // request. The area is rebuilt only when another project is opened.
  import { setContext } from 'svelte';
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { route, href } from '../lib/router.svelte';
  import PageHead from '../lib/PageHead.svelte';
  import ProjectTabs from '../lib/ProjectTabs.svelte';
  import Note from '../lib/Note.svelte';
  import Project from './Project.svelte';
  import Requirements from './Requirements.svelte';
  import Plan from './Plan.svelte';
  import PhaseReview from './PhaseReview.svelte';
  import Connect from './Connect.svelte';
  import ForkDialog from '../lib/ForkDialog.svelte';
  import Btn from '../lib/Btn.svelte';
  import { shellData } from '../lib/shell';
  import { forkText } from '../lib/waiting.svelte';
  import Badge from '../lib/Badge.svelte';

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
</script>

{#if p}
  <PageHead top title={p.name} sub="{p.path}{p.branch ? ' · ' + p.branch : ''}" crumbs={[{ label: 'Projects', href: href('/projects') }, { label: p.name }]}>
    {#snippet right()}{#if p.active}<span class="tag">Active Context</span>{/if}{#if forkLabel}<Badge state="fork" text={forkLabel} />{/if}<Btn sm onclick={() => (forking = true)}>Fork</Btn>{/snippet}
  </PageHead>
  <ProjectTabs {id} current={pane} project={p} />
{/if}
<div class="panes">
  <section class="pane" data-active={pane === 'overview'} aria-hidden={pane !== 'overview'} inert={pane !== 'overview'} aria-label="Overview"><div class="flex min-w-0 flex-col gap-6"><Project /></div></section>
  <section class="pane" data-active={pane === 'requirements'} aria-hidden={pane !== 'requirements'} inert={pane !== 'requirements'} aria-label="Requirements"><div class="flex min-w-0 flex-col gap-6"><Requirements /></div></section>
  <section class="pane" data-active={pane === 'plan'} aria-hidden={pane !== 'plan'} inert={pane !== 'plan'} aria-label="Plan"><div class="flex min-w-0 flex-col gap-6"><Plan /></div></section>
  <section class="pane" data-active={pane === 'review'} aria-hidden={pane !== 'review'} inert={pane !== 'review'} aria-label="Review">
    <div class="flex min-w-0 flex-col gap-6">
      {#if awaiting}<PhaseReview phaseId={awaiting.id} />
      {:else if p}<Note kind="neu" icon="ok"><b>Nothing to review.</b> No phase of this project is waiting for review.</Note>{/if}
    </div>
  </section>
  <section class="pane" data-active={pane === 'guard'} aria-hidden={pane !== 'guard'} inert={pane !== 'guard'} aria-label="Guard"><div class="flex min-w-0 flex-col gap-6"><Connect /></div></section>
</div>

{#if forking && p}
  <ForkDialog project={p} others={projects.data?.items ?? []} oncancel={() => (forking = false)} ondone={() => { forking = false; void res.reload(); void projects.reload(); }} />
{/if}
