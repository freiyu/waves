<script lang="ts">
  // How many validators passed, as a ring with the count in the middle. A full ring is a pass, never an approval.
  let { passed, total, failed = false }: { passed: number; total: number; failed?: boolean } = $props();
  const C = 2 * Math.PI * 35;
  const part = $derived(total > 0 ? Math.min(1, Math.max(0, passed / total)) : 0);
</script>

<svg width="80" height="80" viewBox="0 0 80 80" role="img" aria-label={total ? `${passed} of ${total} validators passed` : 'No validators'}>
  <circle cx="40" cy="40" r="35" fill="none" stroke="var(--surface-muted)" stroke-width="8" />
  {#if part > 0}<circle cx="40" cy="40" r="35" fill="none" stroke={failed ? 'var(--status-danger)' : part === 1 ? 'var(--status-ok)' : 'var(--status-warning)'} stroke-width="8" stroke-dasharray="{part * C} {C}" transform="rotate(-90 40 40)" />{/if}
  <text x="40" y="41" text-anchor="middle" dominant-baseline="middle" fill="var(--text)" font-size="19" font-weight="700">{passed}/{total}</text>
</svg>
