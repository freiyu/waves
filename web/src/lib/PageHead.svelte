<script lang="ts">
  import type { Snippet } from 'svelte';
  let { title, sub = '', crumbs = [], above, right }: { title: string; sub?: string; crumbs?: Array<{ label: string; href?: string }>; above?: Snippet; right?: Snippet } = $props();
</script>

<div class="head">
  <div>
    {#if crumbs.length}
      <nav class="crumbs" aria-label="Breadcrumb">
        {#each crumbs as c, i}{#if i}<span class="mx-1.5" aria-hidden="true">/</span>{/if}{#if c.href}<a href={c.href}>{c.label}</a>{:else}<span>{c.label}</span>{/if}{/each}
      </nav>
    {/if}
    {#if above}<div class="row mb-1.5 gap-2">{@render above()}</div>{/if}
    <h1>{title}</h1>
    {#if sub}<div class="muted">{sub}</div>{/if}
  </div>
  <span class="sp"></span>
  {@render right?.()}
</div>
