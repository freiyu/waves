<script lang="ts">
  // One notice for every harness that stopped and waits for the human: a row each, with how long and the way to let go of it.
  import Icon from './Icon.svelte';
  import IconBtn from './IconBtn.svelte';
  import { href } from './router.svelte';
  import { waitedFor } from './waiting.svelte';
  import type { ExecutionSummary } from '../api/models';

  let { items, since, onrelease }: { items: ExecutionSummary[]; since: Map<string, string>; onrelease: (e: ExecutionSummary) => void } = $props();
</script>

{#if items.length}
  <section class="callout waiting" aria-label="Harnesses waiting for you">
    <h2 class="inline-flex items-center gap-2 text-sm"><Icon name="prompt" size={14} />{items.length === 1 ? 'A harness is waiting for you' : `${items.length} harnesses are waiting for you`}</h2>
    <ul class="plain m-0 flex flex-col gap-3">
      {#each items as w (w.id)}
        <li class="flex items-start gap-3">
          <div class="min-w-0 flex-1">
            <a class="tlink" href={href('/executions/' + w.id)}>{w.task_title}</a>
            <p class="m-0 max-w-[65ch]">{w.project_name} · task {w.task_code}{since.get(w.id) ? ` · waiting ${waitedFor(since.get(w.id)!)}` : ''}</p>
          </div>
          <IconBtn icon="release" label="Release" context={w.task_title} onclick={() => onrelease(w)} />
        </li>
      {/each}
    </ul>
  </section>
{/if}
