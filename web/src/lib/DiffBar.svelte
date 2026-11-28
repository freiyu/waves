<script lang="ts">
  // Added and removed lines as two parts of one thin bar; the numbers are its accessible name.
  let { added, removed }: { added: number; removed: number } = $props();
</script>

<div class="flex h-1.5 gap-0.5 overflow-hidden rounded-full bg-muted" role="img" aria-label="{added} lines added, {removed} removed">
  {#if added > 0}<span class="rounded-full bg-st-ok" style="flex:{added} 1 0; min-width:6px"></span>{/if}
  {#if removed > 0}<span class="rounded-full bg-st-danger" style="flex:{removed} 1 0; min-width:6px"></span>{/if}
</div>
