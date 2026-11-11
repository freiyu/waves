<script lang="ts">
  // A page's own heading under the project tabs: an h2, one muted line, actions on the right.
  import type { Snippet } from 'svelte';
  let { title, sub = '', right }: { title: string; sub?: string; right?: Snippet } = $props();
</script>

<div class="head">
  <div>
    <h2 class="text-xl leading-7">{title}</h2>
    {#if sub}<div class="muted">{sub}</div>{/if}
  </div>
  <span class="sp"></span>
  {@render right?.()}
</div>
