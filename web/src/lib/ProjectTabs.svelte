<script lang="ts" module>
  // Where the underline was on the previous project page. The bar is rebuilt on every page change,
  // so the next one starts there and slides to its own tab.
  let last: { x: number; w: number } | null = null;
</script>

<script lang="ts">
  // The tab bar every project page shares: Overview, Requirements, Plan, Workflow, Review.
  import { onMount } from 'svelte';
  import { Tooltip } from 'bits-ui';
  import { href } from './router.svelte';
  import type { ProjectDetail } from '../api/models';
  // The project may still be loading: the bar then shows no counts.
  let { id, current, project }: { id: string; current: 'overview' | 'requirements' | 'plan' | 'workflow' | 'review'; project?: ProjectDetail } = $props();

  let nav = $state<HTMLElement | null>(null);
  // svelte-ignore state_referenced_locally
  let line = $state({ x: last?.x ?? 0, w: last?.w ?? 0, slide: last !== null });
  function measure() {
    const t = nav?.querySelector<HTMLElement>('[aria-current=page]');
    if (t) { line.x = t.offsetLeft; line.w = t.offsetWidth; last = { x: line.x, w: line.w }; requestAnimationFrame(() => (line.slide = true)); }
  }
  onMount(() => {
    void nav?.offsetWidth; // flush the starting position first, so the change below is animated
    measure();
  });
  // Counts arrive after the first paint and change tab widths.
  $effect(() => { void tabs; queueMicrotask(measure); });
  const review = $derived(project?.phases.filter((p) => p.state === 'awaiting_phase_review') ?? []);
  const tabs = $derived([
    { k: 'overview', label: 'Overview', to: `/projects/${id}` },
    { k: 'requirements', label: 'Requirements', to: `/projects/${id}/requirements`, n: project?.requirements.length },
    { k: 'plan', label: 'Plan', to: `/projects/${id}/plan` },
    { k: 'workflow', label: 'Workflow', to: `/projects/${id}/workflow`, off: project ? !project.phases.length && !project.planning.latest_proposal : false, why: 'No workflow yet. Ask for a plan first.' },
    { k: 'review', label: 'Review', to: review[0] ? `/phases/${review[0].id}/review` : '', n: review.length || undefined, off: !review[0], why: project ? 'No phase is waiting for review.' : 'Loading the project…' },
  ] as Array<{ k: string; label: string; to: string; n?: number; off?: boolean; why?: string }>);
</script>

<svelte:window onresize={measure} />
<Tooltip.Provider delayDuration={200}>
<nav bind:this={nav} class="ptabs" aria-label="Project pages">
  {#each tabs as t}
    {#if t.k === current}
      <span class="ptab" aria-current="page">{t.label}{#if t.n}<span class="n">{t.n}</span>{/if}</span>
    {:else if t.off}
      <!-- Not a link, but reachable by keyboard, and the reason shows on hover and on focus. -->
      <Tooltip.Root>
        <Tooltip.Trigger>
          {#snippet child({ props })}
            <span {...props} class="ptab off" role="link" aria-disabled="true" tabindex="0">{t.label}<span class="sr-only"> ({t.why})</span></span>
          {/snippet}
        </Tooltip.Trigger>
        <Tooltip.Portal>
          <Tooltip.Content sideOffset={6} class="tip z-50 rounded-md bg-fg px-2 py-1 text-xs font-semibold text-bg">{t.why}</Tooltip.Content>
        </Tooltip.Portal>
      </Tooltip.Root>
    {:else}
      <a class="ptab" href={href(t.to)}>{t.label}{#if t.n}<span class="n">{t.n}</span>{/if}</a>
    {/if}
  {/each}
  <span class="tab-line pointer-events-none absolute bottom-0 left-0 h-0.5 w-px bg-primary" style="transform: translateX({line.x}px) scaleX({line.w}); {line.slide ? '' : 'transition: none'}" aria-hidden="true"></span>
</nav>
</Tooltip.Provider>
