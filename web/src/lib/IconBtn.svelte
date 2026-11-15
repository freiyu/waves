<script lang="ts">
  // Row and card actions are icon-only 36 px squares. Hover or keyboard focus rolls a one-word label out to the left
  // over the neighbouring cell (no layout shift); the label is also the accessible name, so the icon never carries
  // the meaning alone. The buttons to its left are pushed aside, never covered. On touch there is no hover: the icon stays (the primary keeps its word) and the dialog that follows names the action.
  import Icon, { type IconName } from './Icon.svelte';
  interface Props {
    icon: IconName;
    /** One word: also the aria-label. */
    label: string;
    kind?: '' | 'pri' | 'dng';
    lock?: boolean;
    disabled?: boolean;
    href?: string;
    title?: string;
    onclick?: (e: MouseEvent) => void;
    /** What the action is about (a task or phase name): added to the accessible name only, so three rows of Approve are told apart. */
    context?: string;
  }
  let { icon, label, kind = '', lock = false, disabled = false, href, title, onclick, context }: Props = $props();
  const name = $derived(context ? `${label} ${context}` : label);
  // The roll-out width fits the label: icon 34 + padding 12 + about 8 px per letter.
  const roll = $derived(`${34 + 12 + 10 + label.length * 8}px`);
</script>

<span class="ibtn" style="--roll:{roll}">
  {#if href && !disabled}
    <a class="face {kind}" {href} aria-label={name} {title}><span class="ico"><Icon name={lock ? 'lock' : icon} size={16} /></span><span class="lab" aria-hidden="true">{label}</span></a>
  {:else}
    <button type="button" class="face {kind}" {disabled} {onclick} aria-label={name} {title}><span class="ico"><Icon name={lock ? 'lock' : icon} size={16} /></span><span class="lab" aria-hidden="true">{label}</span></button>
  {/if}
</span>
