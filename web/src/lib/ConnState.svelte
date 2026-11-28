<script lang="ts">
  // Whether the page is being kept up to date, in words and a glyph.
  import { app } from './app.svelte';
  import Icon from './Icon.svelte';
  const a = app();
  const conn = $derived(a.state.conn);
  const tone = $derived(conn === 'live' ? 'text-st-ok' : conn === 'stale' ? 'text-st-danger' : 'text-st-warning');
  const word = $derived(conn === 'live' ? 'Live' : conn === 'stale' ? 'Stale' : conn === 'reconnecting' ? 'Reconnecting' : 'Connecting');
</script>

<span class="conn inline-flex items-center gap-2 text-[13px] font-medium text-fg-muted" role="status">
  <span class="inline-flex {tone}"><Icon name={conn === 'live' ? 'dot' : conn === 'stale' ? 'fail' : 'warn'} size={conn === 'live' ? 10 : 14} /></span>{word}
</span>
