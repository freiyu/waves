<script lang="ts" module>
  import { tv } from 'tailwind-variants';
  // shadcn-style button variants on the Waves tokens: primary, secondary, ghost, destructive, small.
  export const button = tv({
    base: 'press inline-flex h-9 shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-lg border text-sm font-semibold no-underline disabled:cursor-not-allowed disabled:border-line disabled:bg-muted disabled:text-fg-muted',
    variants: {
      kind: {
        '': 'border-line-strong bg-surface text-fg hover:bg-muted',
        pri: 'border-primary bg-primary text-primary-fg hover:brightness-110',
        dng: 'border-st-danger bg-surface text-st-danger hover:bg-st-danger-soft',
        ghost: 'border-transparent bg-transparent text-fg hover:bg-muted',
      },
      // One size: 36 high and 104 wide for page and dialog actions; `sm` keeps the 36 height but sizes to its label (top bar, inline toggles).
      sm: { true: 'px-3', false: 'min-w-[104px] px-3' },
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
