<script lang="ts" generics="T">
  // Table with sort, optional search and filters. Data arrives already loaded;
  // search, filter and sort run in the browser on that list.
  import type { Snippet } from 'svelte';
  import type { Column, Filter } from './table';
  import { flashOnChange } from './flash';
  import Chips from './Chips.svelte';
  import Icon from './Icon.svelte';
  interface Props {
    rows: T[];
    columns: Column<T>[];
    rowKey: (r: T) => string;
    label: string;
    selected?: string;
    search?: { placeholder: string; text: (r: T) => string };
    filters?: Filter<T>[];
    empty?: Snippet;
    initialSort?: { key: string; dir: 'asc' | 'desc' };
  }
  let { rows, columns, rowKey, label, selected, search, filters = [], empty, initialSort }: Props = $props();

  // The full text as a tooltip only when it is long enough to matter.
  const longText = (t?: string) => (t && t.length > 40 ? t : undefined);
  const slug = $derived(label.toLowerCase().replace(/[^a-z0-9]+/g, '-'));
  let q = $state('');
  let picked = $state<Record<string, string>>({});
  // svelte-ignore state_referenced_locally
  let sort = $state<{ key: string; dir: 'asc' | 'desc' } | undefined>(initialSort);

  const opts = (f: Filter<T>) =>
    f.options ?? [...new Set(rows.map(f.pick))].sort().map((v) => ({ value: v, label: v }));

  const view = $derived.by(() => {
    let r = rows.filter((x) => filters.every((f) => !picked[f.id] || f.pick(x) === picked[f.id]));
    const needle = q.trim().toLowerCase();
    if (search && needle) r = r.filter((x) => search.text(x).toLowerCase().includes(needle));
    const col = columns.find((c) => c.key === sort?.key);
    if (col?.sort && sort) {
      const dir = sort.dir === 'asc' ? 1 : -1;
      const by = col.sort;
      r = [...r].sort((a, b) => {
        const x = by(a), y = by(b);
        return (x < y ? -1 : x > y ? 1 : 0) * dir;
      });
    }
    return r;
  });

  function toggle(key: string) {
    sort = sort?.key === key ? { key, dir: sort.dir === 'asc' ? 'desc' : 'asc' } : { key, dir: 'asc' };
  }
  const ariaSort = (c: Column<T>) => (sort?.key === c.key ? (sort.dir === 'asc' ? 'ascending' : 'descending') : c.sort ? 'none' : undefined);
</script>

{#snippet chips(f: Filter<T>)}
  <Chips label="{f.label} filter" bind:value={() => picked[f.id] || 'all', (v) => (picked[f.id] = v === 'all' ? '' : v)}
    options={[{ id: 'all', label: 'All', count: rows.length }, ...opts(f).map((o) => ({ id: o.value, label: o.label, count: rows.filter((x) => f.pick(x) === o.value).length })).filter((o) => o.count > 0 || picked[f.id] === o.id)]} />
{/snippet}

{#if rows.length > 0 || !empty}
{#if search || filters.length}
  <div class="toolbar" style="padding:12px 16px;border-bottom:1px solid var(--border)">
    {#if search}
      <span class="searchbox"><Icon name="search" size={14} />
        <label class="sr-only" for="tbl-q-{slug}">Search {label}</label>
        <input id="tbl-q-{slug}" type="search" placeholder={search.placeholder} bind:value={q} />
      </span>
    {/if}
    {#each filters.filter((f) => f.chips && !f.row) as f}{@render chips(f)}{/each}
    <span class="sp"></span>
    {#each filters.filter((f) => !f.chips) as f}
      <label class="sr-only" for="tbl-{slug}-{f.id}">{f.label}</label>
      <select id="tbl-{slug}-{f.id}" class="field" bind:value={picked[f.id]}>
        <option value="">{f.label}: all</option>
        {#each opts(f) as o}<option value={o.value}>{o.label}</option>{/each}
      </select>
    {/each}
    <span class="small muted" role="status">{view.length} of {rows.length}</span>
    {#each filters.filter((f) => f.chips && f.row) as f}<div class="basis-full">{@render chips(f)}</div>{/each}
  </div>
{/if}
<div class="tablewrap">
  <table class="cards" aria-label={label}>
    <thead>
      <tr>
        {#each columns as c}
          <th scope="col" aria-sort={ariaSort(c)}>
            {#if c.sort}<button type="button" onclick={() => toggle(c.key)}>{c.label}<span aria-hidden="true">{sort?.key === c.key ? (sort.dir === 'asc' ? '▲' : '▼') : ''}</span></button>{:else}{c.label}{/if}
          </th>
        {/each}
      </tr>
    </thead>
    <tbody>
      {#each view as r (rowKey(r))}
        <tr use:flashOnChange={JSON.stringify(r)} class:sel={selected === rowKey(r)} aria-current={selected === rowKey(r) ? 'true' : undefined}>
          {#each columns as c, i}
            <td class:mono={c.mono} class:first={i === 0} data-label={c.label} data-key={c.key} title={c.cell ? undefined : longText(c.text?.(r))}>{#if c.cell}{@render c.cell(r)}{:else}{c.text?.(r) ?? ''}{/if}</td>
          {/each}
        </tr>
      {/each}
    </tbody>
  </table>
</div>
{/if}
{#if view.length === 0}
  <div class="empty">{#if rows.length === 0 && empty}{@render empty()}{:else if search || filters.length}No {label.toLowerCase()} match this search or filter. Clear it to see all.{:else}No {label.toLowerCase()} to show.{/if}</div>
{/if}
