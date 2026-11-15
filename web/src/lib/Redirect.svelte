<script lang="ts">
  // Old addresses that no longer have a page of their own send the human to the nearest page that does.
  import { onMount } from 'svelte';
  let { to }: { to: string } = $props();
  onMount(() => location.replace('#' + to));
</script>
<div class="empty" role="status">Taking you to the new page…</div>
