<script lang="ts">
  // One row per execution of the last hour. The bar's place and length are measured time; only a running bar has a wave at its
  // right end, which is now (motion.css), and that changes neither.
  import { visible } from './text';
  import { href } from './router.svelte';
  import { shortDuration } from './format';
  import { flashOnChange } from './flash';
  import { groupOf, span } from './timeline';
  import { status } from './status';
  import type { ExecutionSummary } from '../api/models';
  import Badge from './Badge.svelte';
  let { items, now }: { items: ExecutionSummary[]; now: number } = $props();
  const rows = $derived([...items].sort((a, b) => (b.started_at ?? '').localeCompare(a.started_at ?? '')));
</script>

<div class="tl">
  <div class="tl-row tl-axis" aria-hidden="true"><span></span><span class="tl-ticks"><span>60 min ago</span><span>45</span><span>30</span><span>15</span><span>now</span></span></div>
  <ul class="stagger" aria-label="Timeline">
    {#each rows as r (r.id)}
      {@const s = span(r, now)!}
      {@const g = groupOf(r)}
      {@const word = r.waiting_since ? 'Waiting for instruction' : status(r.state).label}
      <li class="tl-row" use:flashOnChange={JSON.stringify(r)}>
        <div class="min-w-0">
          <a class="tlink text-[13px]" title={r.id} href={href('/executions/' + r.id)}>{visible(r.task_title)}</a>
          <div class="mt-1 flex flex-wrap items-center gap-2"><Badge state={r.waiting_since ? 'waiting_for_instruction' : r.state} /><span class="text-xs text-fg-muted">{r.task_code} · {r.project_name}<span class="sr-only"> · {r.id}</span></span></div>
        </div>
        <div class="tl-track" role="img" aria-label="{word}, {shortDuration(r.duration_s)}">
          <span class="tl-bar" data-g={g} style="left: min({s.left}%, calc(100% - 8px)); width: max(8px, {s.width}%)"></span>
        </div>
      </li>
    {:else}
      <li class="empty">No execution in the last hour matches.</li>
    {/each}
  </ul>
</div>
