<script lang="ts">
  // The first line of a page (crumb and the page's actions). On a wide screen both are shown in the shell's top row, the
  // crumb before the shell's tools and the actions after them, so the order on screen is the order of the keyboard. On a
  // narrow screen they stay in the page.
  import type { Snippet } from 'svelte';
  /** `roomy`: the actions are wide (the project tabs) and join the top row only when it has the room. */
  let { crumb, actions, roomy = false }: { crumb: Snippet; actions?: Snippet; roomy?: boolean } = $props();

  function toTop(node: HTMLElement, [slot, query]: [string, string]) {
    // The wrapper stays where Svelte put it; only the element inside it moves.
    const home = node.parentElement!;
    const wide = matchMedia(query);
    const place = () => {
      // Moving an element drops the focus inside it; give it back.
      const held = node.contains(document.activeElement) ? (document.activeElement as HTMLElement) : null;
      ((wide.matches && document.getElementById(slot)) || home).append(node);
      held?.focus();
    };
    place();
    wide.addEventListener('change', place);
    return { destroy() { wide.removeEventListener('change', place); node.remove(); } };
  }
</script>

<div class="crumbrow">
  <div class="contents"><div class="tcrumb" use:toTop={['pagecrumb', '(min-width: 861px)']}>{@render crumb()}</div></div>
  {#if actions}
    {#key roomy}
      <div class="contents"><div class="tacts" use:toTop={['pageacts', roomy ? '(min-width: 1180px)' : '(min-width: 861px)']}>{@render actions()}</div></div>
    {/key}
  {/if}
</div>
