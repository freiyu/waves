<script lang="ts" generics="T">
  // Loading, error and content states for one resource. A failed load never
  // looks like an empty list: it says what happened and offers a retry.
  import type { Snippet } from 'svelte';
  import Note from './Note.svelte';
  import Btn from './Btn.svelte';
  import type { Res } from './res';
  let { res, label, children }: { res: Res<T>; label: string; children: Snippet<[T]> } = $props();
</script>

{#if res.data !== undefined}
  {#if res.error}
    <Note kind="warn" role="alert"><b>Showing older data.</b> The last refresh failed: {res.error.message} <Btn sm onclick={() => res.reload()}>Retry</Btn></Note>
  {/if}
  {@render children(res.data)}
{:else if res.error}
  <Note kind="dng" icon="fail" role="alert">
    <b>Could not load {label}.</b> {res.error.message}
    {#if res.error.status}<span class="mono small muted"> · {res.error.status}{res.error.body?.request_id ? ` · request ${res.error.body.request_id}` : ''}</span>{/if}
    {#if res.error.nextStep}<div>{res.error.nextStep}</div>{/if}
    <div class="row mt-2"><Btn onclick={() => res.reload()}>Retry</Btn></div>
  </Note>
{:else}
  <div class="panel skel" role="status" aria-label="Loading {label}"><i class="skeleton w-[60%]"></i><i class="skeleton w-[85%]"></i><i class="skeleton w-[72%]"></i><i class="skeleton w-[90%]"></i></div>
{/if}
