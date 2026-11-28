<script lang="ts">
  import Icon from './Icon.svelte';
  import { status, type Tone } from './status';
  import type { IconName } from './Icon.svelte';
  // `plain` leaves the glyph out where one already stands beside the chip (a Graph node); the words stay.
  let { state: key, text, icon, plain = false }: { state: string; text?: string; icon?: IconName; plain?: boolean } = $props();
  const s = $derived(status(key));
  // A state that turns ok confirms itself with one pulse (never on the first drawing).
  // svelte-ignore state_referenced_locally
  let prev = key;
  let pulsing = $state(false);
  $effect(() => {
    if (key === prev) return;
    prev = key;
    if (s.tone === 'ok') {
      pulsing = true;
      setTimeout(() => (pulsing = false), 300);
    }
  });
  // Full class names, so Tailwind can see them.
  const TONE: Record<Tone, string> = {
    active: 'bg-st-active-soft text-st-active', attention: 'bg-st-attention-soft text-st-attention', ok: 'bg-st-ok-soft text-st-ok',
    danger: 'bg-st-danger-soft text-st-danger', warning: 'bg-st-warning-soft text-st-warning', neutral: 'bg-st-neutral-soft text-st-neutral',
    waiting: 'bg-st-waiting-soft text-st-waiting', accent: 'bg-st-neutral-soft text-st-neutral',
  };
</script>

<span class:confirm-pulse={pulsing} class="badge inline-flex items-center gap-1.5 whitespace-nowrap rounded-full py-[5px] pr-3 text-xs leading-[18px] font-semibold {plain ? 'pl-3' : 'pl-2.5'} {TONE[s.tone]}">{#if !plain}<span class:pulse={s.tone === 'active'} class="inline-flex"><Icon name={icon ?? s.icon} size={12} /></span>{/if}{text ?? s.label}</span>
