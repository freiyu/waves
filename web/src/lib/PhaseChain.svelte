<script lang="ts">
  // The phases of one project as chains of task dots, one group per phase.
  import { visible } from './text';
  import StatusDot from './StatusDot.svelte';
  import { status } from './status';
  type Task = { id: string; code: string; title: string; state: string };
  let { phases }: { phases: Array<{ id: string; code: string; title: string; note?: string; tasks: Task[] }> } = $props();
</script>

<ul class="flex flex-wrap gap-x-9 gap-y-4">
  {#each phases as p (p.id)}
    <li class="min-w-0">
      <div class="mb-2 text-xs text-fg-muted">{p.code}<span class="ml-2">{visible(p.title)}</span>{#if p.note}<span class="mx-1.5" aria-hidden="true">·</span>{p.note}{/if}</div>
      <ol class="flex flex-wrap items-center gap-y-2" aria-label="Tasks of {p.code} {p.title}">
        {#each p.tasks as t, i (t.id)}
          <li class="flex items-center">
            {#if i}<span class="h-px w-3.5 bg-ghost" aria-hidden="true"></span>{/if}
            <StatusDot state={t.state} label="{t.code} {t.title}: {status(t.state).label}" />
          </li>
        {/each}
      </ol>
    </li>
  {/each}
</ul>
