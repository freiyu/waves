<script lang="ts">
  import type { Snippet } from 'svelte';
  let { title = '', count, right, pad = false, cls = '', level = 2, children }: { title?: string; count?: number; right?: Snippet; pad?: boolean; cls?: string; level?: 2 | 3; children: Snippet } = $props();
</script>

<section class="panel {cls}" aria-label={title || undefined}>
  {#if title || right}<div class="ph"><svelte:element this={`h${level}`}>{title}</svelte:element>{#if count !== undefined}<span class="count">{count}</span>{/if}<span class="sp"></span>{@render right?.()}</div>{/if}
  <div class={pad ? 'pb' : ''}>{@render children()}</div>
</section>
