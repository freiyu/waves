<script lang="ts">
  import { getContext, type Snippet } from 'svelte';
  // Inside a project area the area owns the page's one h1; a pane's heading is an h2.
  let { title, sub = '', crumbs = [], above, right, top }: { title: string; sub?: string; crumbs?: Array<{ label: string; href?: string }>; above?: Snippet; right?: Snippet; top?: boolean } = $props();
  const asH1 = $derived(top ?? getContext('area') !== true);
</script>

<div class="head">
  <div>
    {#if crumbs.length}
      <nav class="crumbs" aria-label="Breadcrumb">
        {#each crumbs as c, i}{#if i}<span class="mx-1.5" aria-hidden="true">/</span>{/if}{#if c.href}<a href={c.href}>{c.label}</a>{:else}<span>{c.label}</span>{/if}{/each}
      </nav>
    {/if}
    {#if above}<div class="row mb-1.5 gap-2">{@render above()}</div>{/if}
    <svelte:element this={asH1 ? 'h1' : 'h2'} class={asH1 ? '' : 'text-xl font-semibold'}>{title}</svelte:element>
    {#if sub}<div class="muted">{sub}</div>{/if}
  </div>
  <span class="sp"></span>
  {@render right?.()}
</div>
