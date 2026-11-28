<script lang="ts">
  // The phases of a project as one line of large nodes: where it has been, where it is, what is still ahead.
  import { visible } from './text';
  import { href } from './router.svelte';
  import { plural } from './format';
  import type { ProjectDetail, PhaseRef } from '../api/models';
  import Icon from './Icon.svelte';
  let { project }: { project: ProjectDetail } = $props();
  // An approved phase has only been approved to start; it is behind us once it is completed.
  const DONE = ['completed'];
  const cur = $derived(project.phases.findIndex((x) => !DONE.includes(x.state)));
  function note(p: PhaseRef): string {
    const t = project.phase_tasks.filter((x) => x.phase_id === p.id);
    const done = t.filter((x) => x.state === 'done').length;
    if (p.state === 'awaiting_phase_review') return 'awaiting your review';
    if (p.state === 'revision_required') return 'revision required';
    if (p.state === 'planned') return `queued, ${plural(t.length, 'task')}`;
    if (DONE.includes(p.state)) return `${done} of ${t.length} done`;
    const running = t.filter((x) => x.state === 'in_progress').length;
    return `${done} of ${t.length} done${running ? `, ${running} running` : ''}`;
  }
</script>

<section class="flex flex-col gap-4" aria-label="Phases">
  <div class="sect"><h2>Journey</h2><span class="aside">Phase {cur < 0 ? project.phases.length : cur + 1} of {project.phases.length} · {project.progress_pct}% done</span></div>
  <ol class="journey" aria-label="Phases">
    {#each project.phases as p, i (p.id)}
      {@const done = DONE.includes(p.state)}
      {@const review = p.state === 'awaiting_phase_review'}
      <li class:done class:cur={i === cur} class:review>
        <span class="node-c" aria-hidden="true">{#if done}<Icon name="check" size={18} />{:else if review}<Icon name="lock" size={18} />{:else}{i + 1}{/if}</span>
        <span class="text-xs text-fg-muted">{p.code}</span>
        <b class="text-[15px]">{#if review}<a class="tlink" href={href(`/phases/${p.id}/review`)}>{visible(p.title)}</a>{:else}{visible(p.title)}{/if}</b>
        <span class="text-xs text-fg-muted">{note(p)}</span>
      </li>
    {/each}
  </ol>
</section>
