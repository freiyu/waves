<script lang="ts">
  // Streams a log from the file by byte offset. New lines never move what the
  // reader is looking at: following stops when they scroll up.
  import { onMount, tick } from 'svelte';
  import { app } from './app.svelte';
  import Btn from './Btn.svelte';
  import Chips from './Chips.svelte';
  import Icon from './Icon.svelte';

  let { id, live }: { id: string; live: boolean } = $props();
  const a = app();
  const CAP = 2000;
  let lines = $state<string[]>([]);
  let partial = $state('');
  let dropped = $state(0);
  let offset = 0;
  let follow = $state(true);
  let unseen = $state(0);
  let box: HTMLDivElement;
  let busy = false;
  let again = false;
  let failed = $state('');
  const enc = new TextEncoder();
  // Tool chips: counts per tool from lines like "11:43:19 tool  Edit src/x.ts". Display only; the stream is untouched.
  let tool = $state('all');
  const toolOf = (l: string) => /^\S+\s+tool\s+(\w+)/.exec(l)?.[1];
  const counts = $derived.by(() => {
    const c: Record<string, number> = {};
    for (const l of lines) { const t = toolOf(l); if (t) c[t] = (c[t] ?? 0) + 1; }
    return c;
  });
  let q = $state('');
  let searching = $state(false);
  const shown = $derived.by(() => {
    const byTool = tool === 'all' ? lines : lines.filter((l) => toolOf(l) === tool);
    const n = q.trim().toLowerCase();
    return n ? byTool.filter((l) => l.toLowerCase().includes(n)) : byTool;
  });
  function toggleFollow() {
    follow = !follow;
    if (follow) { unseen = 0; box.scrollTop = box.scrollHeight; }
  }

  async function pull() {
    if (busy) return void (again = true);
    busy = true;
    try {
      do {
        again = false;
        const chunk = await a.api.log(id, offset);
        if (chunk) {
          offset += enc.encode(chunk).length;
          const parts = (partial + chunk).split('\n');
          partial = parts.pop() ?? '';
          for (const l of parts) lines.push(l); // not push(...parts): a very large chunk would overflow the call stack
          if (lines.length > CAP) {
            dropped += lines.length - CAP;
            lines = lines.slice(-CAP);
          }
          if (!follow) unseen += chunk.split('\n').length - 1;
          await tick();
          if (follow) box.scrollTop = box.scrollHeight;
        }
        failed = '';
      } while (again);
    } catch {
      failed = 'The log could not be read. It will be fetched again when the next line arrives.';
    } finally {
      busy = false;
    }
  }

  onMount(() => {
    const off = a.subscribe(['execution.log', 'execution.changed'], (_n, d) => {
      if ((d as { execution_id: string }).execution_id === id) void pull();
    });
    return off;
  });
  $effect(() => {
    void a.state.resync;
    void pull(); // also the first load
  });
  // A selected tool whose lines left the window would leave an empty log and no chip to clear it.
  $effect(() => {
    if (tool !== 'all' && !counts[tool]) tool = 'all';
  });

  function onScroll() {
    follow = box.scrollTop + box.clientHeight >= box.scrollHeight - 8;
    if (follow) unseen = 0;
  }
</script>

<div class="stack gap-2">
  <div class="row wrap gap-2">
    {#if Object.keys(counts).length}
      <Chips label="Filter log by tool" bind:value={tool} options={[{ id: 'all', label: 'All', count: lines.length }, ...Object.entries(counts).map(([k, n]) => ({ id: k, label: k, count: n }))]} />
    {/if}
    <span class="sp"></span>
    {#if searching}
      <span class="searchbox w-56"><Icon name="search" size={14} /><input type="search" aria-label="Search the log" placeholder="Search the log" bind:value={q} /></span>
    {/if}
    {#if live}<Btn pressed={follow} onclick={toggleFollow}>Follow</Btn>{/if}
    <button type="button" class="inline-flex size-8 cursor-pointer items-center justify-center rounded-md text-fg-muted hover:bg-muted" aria-label="Show log search" aria-expanded={searching} onclick={() => { searching = !searching; if (!searching) q = ''; }}><Icon name="search" size={16} /></button>
  </div>
  <!-- svelte-ignore a11y_no_noninteractive_tabindex: a scrollable region must be reachable by keyboard -->
  <div class="log mono" bind:this={box} onscroll={onScroll} tabindex="0" aria-label="Execution log" aria-live="off" role="log">{#if dropped}<div class="muted">… {dropped.toLocaleString('en')} earlier lines are not shown. The full log is in the file.</div>{/if}{#each shown as l}<div>{l}</div>{/each}{#if partial}<div>{partial}</div>{/if}{#if !lines.length && !partial}<div class="muted">The log is empty.</div>{/if}</div>
  <div class="row small muted" role="status">
    {#if failed}<span>{failed}</span>
    {:else if !live}<span>This execution is not running. The log is complete.</span>
    {:else if follow}<span>Following the log.</span>
    {:else}<span>Paused. {unseen} new {unseen === 1 ? 'line' : 'lines'} below.</span><Btn sm onclick={() => { follow = true; unseen = 0; box.scrollTop = box.scrollHeight; }}>Jump to latest</Btn>{/if}
  </div>
</div>
