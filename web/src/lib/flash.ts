/**
 * Svelte action: when the signature changes after the first render (a row changed by a live event),
 * the element flashes its background for 600 ms (see motion.css). The first render never flashes.
 */
export function flashOnChange(node: HTMLElement, sig: string) {
  return {
    update(next: string) {
      if (next === sig) return;
      sig = next;
      node.classList.remove('flash', 'li-flash');
      void node.offsetWidth; // restart the animation
      node.classList.add(node.tagName === 'TR' ? 'flash' : 'li-flash');
    },
  };
}
