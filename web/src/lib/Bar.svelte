<script lang="ts">
  // The thin bar. `running`: no known amount, a wave travels left and right (see motion.css). Otherwise `value` is a percentage.
  let { label, value = 0, running = false, class: cls = '' }: { label: string; value?: number; running?: boolean; class?: string } = $props();
  const pct = $derived(Math.min(100, Math.max(0, value)));
</script>

<div class="bar {cls}" class:run={running} role="progressbar" aria-label={label} aria-valuenow={running ? undefined : pct} aria-valuemin={running ? undefined : 0} aria-valuemax={running ? undefined : 100}><i style={running ? undefined : `width:${pct}%`}></i></div>
