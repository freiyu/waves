/**
 * Svelte action for a modal <dialog>: when the dialog leaves the page, focus
 * goes back to the element that had it before the dialog opened (usually the
 * button that opened it) instead of falling to the page body.
 */
export function returnFocus(node: HTMLElement) {
  const a = document.activeElement;
  const opener = a instanceof HTMLElement && !node.contains(a) ? a : null;
  return {
    destroy() {
      // If the opener is gone (a decision removed its button), land on the page content, not the body.
      queueMicrotask(() => (opener?.isConnected ? opener : document.getElementById('main'))?.focus());
    },
  };
}
