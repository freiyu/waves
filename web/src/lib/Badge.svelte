<script lang="ts">
  import Icon from './Icon.svelte';
  import { status, type Tone } from './status';
  let { state, text }: { state: string; text?: string } = $props();
  const s = $derived(status(state));
  // Full class names, so Tailwind can see them.
  const TONE: Record<Tone, string> = {
    active: 'bg-st-active-soft text-st-active', attention: 'bg-st-attention-soft text-st-attention', ok: 'bg-st-ok-soft text-st-ok',
    danger: 'bg-st-danger-soft text-st-danger', warning: 'bg-st-warning-soft text-st-warning', neutral: 'bg-st-neutral-soft text-st-neutral',
  };
</script>

<span class="badge inline-flex items-center gap-1.5 whitespace-nowrap rounded-[4px] py-px pr-2 pl-1.5 text-xs leading-5 font-semibold {TONE[s.tone]}"><span class:pulse={s.tone === 'active'} class="inline-flex"><Icon name={s.icon} size={14} /></span>{text ?? s.label}</span>
