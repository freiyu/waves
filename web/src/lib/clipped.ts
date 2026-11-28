// Svelte action for a box whose first child is clamped text: marks the box when text is cut off, so the whole text can be
// offered on top of it, and lets the keyboard reach it when nothing inside takes the focus.
export function clipped(node: HTMLElement, _text: string) {
  const check = () => {
    const text = node.firstElementChild as HTMLElement;
    const cut = text.scrollHeight > text.clientHeight + 1;
    node.toggleAttribute('data-cut', cut);
    if (cut && !node.querySelector('a')) node.tabIndex = 0;
    else node.removeAttribute('tabindex');
  };
  const seen = new ResizeObserver(check);
  seen.observe(node.firstElementChild!);
  void document.fonts.ready.then(check); // a font swap can add a hidden line without changing the box
  return { update: () => void requestAnimationFrame(check), destroy: () => seen.disconnect() };
}
