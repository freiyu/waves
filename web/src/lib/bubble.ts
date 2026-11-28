// A highlight that follows the pointer, or the keyboard, across the items of a group and takes each item's colour
// (`data-tone` names a status token). It moves by CSS transition, so a new target midway carries on from where it is.
export function hoverBubble(host: HTMLElement) {
  const bubble = host.querySelector<HTMLElement>(':scope > .hoverbubble')!;
  const PAD = 12; // the highlight reaches a little beyond the item on both sides
  function show(e: Event) {
    const item = (e.target as Element).closest<HTMLElement>('[data-tone]');
    if (!item || (e as PointerEvent).pointerType === 'touch') return;
    const place = () => {
      const r = item.getBoundingClientRect(), h = host.getBoundingClientRect();
      Object.assign(bubble.style, { width: `${r.width + 2 * PAD}px`, height: `${r.height}px`, transform: `translate(${r.left - h.left - PAD}px, ${r.top - h.top}px)`, backgroundColor: `var(--status-${item.dataset.tone}-soft)` });
    };
    if (!bubble.hasAttribute('data-on')) {
      place();
      void bubble.offsetWidth; // it appears where it is wanted instead of flying in from its last place
    }
    place();
    bubble.setAttribute('data-on', '');
  }
  const hide = () => bubble.removeAttribute('data-on');
  const blur = (e: FocusEvent) => { if (!host.contains(e.relatedTarget as Node)) hide(); };
  host.addEventListener('pointerover', show);
  host.addEventListener('focusin', show);
  host.addEventListener('pointerleave', hide);
  host.addEventListener('focusout', blur);
  return {
    destroy() {
      host.removeEventListener('pointerover', show);
      host.removeEventListener('focusin', show);
      host.removeEventListener('pointerleave', hide);
      host.removeEventListener('focusout', blur);
    },
  };
}
