<script lang="ts" module>
  import { tv } from 'tailwind-variants';
  // shadcn-style button variants on the Waves tokens: approve (pri), reject (dng), retry (warn), release (info), resolve (ok),
  // everything else (sec), ghost; small.
  export const button = tv({
    base: 'press inline-flex h-11 shrink-0 cursor-pointer items-center justify-center gap-2 whitespace-nowrap rounded-xl border text-sm font-semibold no-underline disabled:cursor-not-allowed disabled:border-dashed disabled:border-line-strong disabled:bg-transparent disabled:text-fg-muted',
    variants: {
      kind: {
        // Never name a variant '': tailwind-variants treats it as unset and drops its classes.
        // Solid, each action in its own colour; under the pointer the colours turn over.
        sec: 'border-transparent bg-muted text-fg hover:border-primary hover:bg-primary hover:text-primary-fg',
        pri: 'border-primary bg-primary text-primary-fg hover:border-st-ok hover:bg-st-ok hover:text-surface',
        dng: 'border-btn-dng bg-btn-dng text-surface hover:bg-st-danger-soft hover:text-btn-dng',
        warn: 'border-btn-warn bg-btn-warn text-btn-warn-fg hover:bg-st-warning-soft hover:text-st-warning',
        info: 'border-st-active bg-st-active text-surface hover:bg-st-active-soft hover:text-st-active',
        ok: 'border-st-waiting bg-st-waiting text-surface hover:bg-st-waiting-soft hover:text-st-waiting',
        ghost: 'border-transparent bg-transparent text-fg hover:bg-muted',
      },
      // One height, 44. Page and dialog actions are at least 104 wide; `sm` sizes to its label (inline toggles).
      sm: { true: 'px-3.5', false: 'min-w-[104px] px-[18px]' },
      pressed: { true: 'bg-muted border-fg' },
    },
    defaultVariants: { kind: 'sec', sm: false },
  });
</script>

<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';
  interface Props {
    kind?: '' | 'pri' | 'dng' | 'warn' | 'info' | 'ok' | 'ghost';
    lock?: boolean;
    /** A toggle: shows its state with a check mark and aria-pressed. */
    pressed?: boolean;
    sm?: boolean;
    href?: string;
    disabled?: boolean;
    type?: 'button' | 'submit';
    title?: string;
    /** Accessible name when the visible word alone does not say what it acts on. */
    label?: string;
    class?: string;
    onclick?: (e: MouseEvent) => void;
    children: Snippet;
  }
  let { kind = '', lock = false, pressed, sm = false, href, disabled = false, type = 'button', title, label, class: extra = '', onclick, children }: Props = $props();
  const cls = $derived(button({ kind: kind || 'sec', sm, pressed: pressed === true, class: extra }));
</script>

{#if href && !disabled}
  <a class={cls} {href} {title} aria-label={label}>{#if lock}<Icon name="lock" size={14} />{/if}{@render children()}</a>
{:else}
  <button class={cls} {type} {disabled} {title} aria-label={label} {onclick} aria-pressed={pressed}>{#if pressed}<Icon name="check" size={14} />{/if}{#if lock}<Icon name="lock" size={14} />{/if}{@render children()}</button>
{/if}
