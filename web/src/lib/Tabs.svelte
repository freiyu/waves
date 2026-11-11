<script lang="ts">
  // bits-ui Tabs with an underline that slides to the new tab (transform only, 180 ms).
  import type { Snippet } from 'svelte';
  import { Tabs } from 'bits-ui';
  let { tabs, active = $bindable(), label, children }: { tabs: Array<{ id: string; label: string; count?: number }>; active: string; label: string; children: Snippet<[string]> } = $props();
  let list = $state<HTMLElement | null>(null);
  let x = $state(0);
  let w = $state(0);
  let ready = $state(false); // no slide on the first paint
  function measure() {
    const t = list?.querySelector<HTMLElement>('[role=tab][data-state=active]');
    if (t) { x = t.offsetLeft; w = t.offsetWidth; requestAnimationFrame(() => (ready = true)); }
  }
  $effect(() => { void active; void tabs; queueMicrotask(measure); });
</script>

<svelte:window onresize={measure} />
<Tabs.Root bind:value={active}>
  <Tabs.List bind:ref={list} aria-label={label} class="relative flex gap-1 overflow-x-auto border-b border-line">
    {#each tabs as t}
      <Tabs.Trigger value={t.id} class="inline-flex cursor-pointer items-center gap-1.5 whitespace-nowrap px-3 py-2 outline-offset-[-2px] text-fg-muted transition-colors duration-100 hover:text-fg data-[state=active]:font-semibold data-[state=active]:text-fg">
        {t.label}{#if t.count !== undefined}<span class="rounded-full bg-muted px-1.5 text-xs leading-4 font-normal text-fg-muted">{t.count}</span>{/if}
      </Tabs.Trigger>
    {/each}
    <span class="tab-line pointer-events-none absolute bottom-0 left-0 h-0.5 w-px bg-primary" style="transform: translateX({x}px) scaleX({w}); {ready ? '' : 'transition: none'}" aria-hidden="true"></span>
  </Tabs.List>
  <Tabs.Content value={active} class="outline-none">{@render children(active)}</Tabs.Content>
</Tabs.Root>
