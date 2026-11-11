<script lang="ts" module>
  import { tv } from 'tailwind-variants';
  // shadcn-style button variants on the Waves tokens: primary, secondary, ghost, destructive, small.
  export const button = tv({
    base: 'inline-flex shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-md border text-sm font-semibold no-underline transition-colors duration-100 disabled:cursor-not-allowed disabled:border-line disabled:bg-muted disabled:text-fg-muted',
    variants: {
      kind: {
        '': 'border-line-strong bg-surface text-fg hover:bg-muted',
        pri: 'border-primary bg-primary text-primary-fg hover:brightness-110',
        dng: 'border-st-danger bg-surface text-st-danger hover:bg-st-danger-soft',
        ghost: 'border-transparent bg-transparent text-fg hover:bg-muted',
      },
      sm: { true: 'min-h-7 px-2.5 text-[13px]', false: 'min-h-8 px-3' },
      pressed: { true: 'bg-muted border-fg' },
    },
    defaultVariants: { kind: '', sm: false },
  });
</script>

<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';
  interface Props {
    kind?: '' | 'pri' | 'dng' | 'ghost';
    lock?: boolean;
    /** A toggle: shows its state with a check mark and aria-pressed. */
    pressed?: boolean;
    sm?: boolean;
    href?: string;
    disabled?: boolean;
    type?: 'button' | 'submit';
    title?: string;
    class?: string;
    onclick?: (e: MouseEvent) => void;
    children: Snippet;
  }
  let { kind = '', lock = false, pressed, sm = false, href, disabled = false, type = 'button', title, class: extra = '', onclick, children }: Props = $props();
  const cls = $derived(button({ kind, sm, pressed: pressed === true, class: extra }));
</script>

{#if href && !disabled}
  <a class={cls} {href} {title}>{#if lock}<Icon name="lock" size={14} />{/if}{@render children()}</a>
{:else}
  <button class={cls} {type} {disabled} {title} {onclick} aria-pressed={pressed}>{#if pressed}<Icon name="check" size={14} />{/if}{#if lock}<Icon name="lock" size={14} />{/if}{@render children()}</button>
{/if}
