<script lang="ts">
  // One state as a 16 px dot. The shape differs for every kind, so colour is never the only difference.
  import { status, type Tone } from './status';
  let { state, label }: { state: string; label?: string } = $props();
  const s = $derived(status(state));
  const SHAPE: Record<Tone, string> = {
    ok: 'rounded-full bg-st-ok',
    active: 'rounded-full border-2 border-st-active bg-[linear-gradient(90deg,currentColor_50%,transparent_50%)] text-st-active',
    waiting: 'rounded-full border-2 border-st-waiting bg-[radial-gradient(circle,currentColor_0_3px,transparent_3.5px)] text-st-waiting',
    attention: 'rounded-full border-4 border-st-attention',
    warning: 'rounded-full border-2 border-dashed border-st-warning',
    danger: 'rotate-45 scale-[0.82] rounded-[3px] bg-st-danger',
    neutral: 'rounded-full border-2 border-queued',
    accent: 'rounded-full border-2 border-queued',
  };
  const name = $derived(label ?? s.label);
</script>

<span class="inline-block size-4 flex-none {SHAPE[s.tone]}" data-tone={s.tone} role="img" aria-label={name} title={name}></span>
