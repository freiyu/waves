<script lang="ts">
  // Filter chips with counts (single choice) as one segmented pill. A highlight pill sits inside it, above the pill's
  // own background and under the labels, and chases the pressed chip on a spring: thin while it moves fast, full width
  // at rest. A new click mid-flight only changes the target, so position, size and speed carry on without a jump.
  // Pressed state is also aria-pressed, never colour alone.
  import { onDestroy, tick } from 'svelte';
  let { options, value = $bindable(), label }: { options: Array<{ id: string; label: string; count?: number }>; value: string; label: string } = $props();

  let box = $state<HTMLElement | null>(null);
  let bubble = $state<HTMLElement | null>(null);
  type Axis = 'x' | 'w';
  const AXES: Axis[] = ['x', 'w'];
  const cur = { x: 0, y: 0, w: 0, h: 0 };
  const vel = { x: 0, w: 0 };
  let tgt = { x: 0, y: 0, w: 0, h: 0 };
  let seen: string | undefined; // the pressed chip the bubble was last sent to
  let raf = 0;
  let last = 0;
  const K = 520; // spring stiffness; settles in about 300 ms
  const C = 2 * Math.sqrt(K) * 0.85; // slightly under critical damping

  function paint() {
    if (!bubble) return;
    const squeeze = Math.min(0.55, Math.abs(vel.x) / 1600); // thinner the faster it moves
    const w = Math.max(12, cur.w * (1 - squeeze));
    bubble.style.width = `${w}px`;
    bubble.style.height = `${cur.h}px`;
    bubble.style.transform = `translate(${cur.x + cur.w / 2 - w / 2}px, ${cur.y}px)`; // y is the row's top, set by measure()
    // A label turns light only while the highlight is behind it, so text is never light on light during the move.
    const centre = cur.x + cur.w / 2;
    for (const b of box?.querySelectorAll<HTMLElement>('button') ?? []) b.toggleAttribute('data-under', Math.abs(centre - (b.offsetLeft + b.offsetWidth / 2)) < w / 2);
  }
  function snap() {
    cancelAnimationFrame(raf);
    raf = 0;
    Object.assign(cur, tgt);
    vel.x = vel.w = 0;
    paint();
  }
  function step(t: number) {
    const dt = Math.min(0.032, Math.max(0, (t - last) / 1000));
    last = t;
    for (const k of AXES) {
      vel[k] += (K * (tgt[k] - cur[k]) - C * vel[k]) * dt;
      cur[k] += vel[k] * dt;
    }
    cur.h = tgt.h;
    if (AXES.every((k) => Math.abs(tgt[k] - cur[k]) < 0.2 && Math.abs(vel[k]) < 2)) return snap(); // at rest: the loop stops
    paint();
    raf = requestAnimationFrame(step);
  }
  function measure() {
    const el = box?.querySelector<HTMLElement>('[aria-pressed=true]');
    if (!bubble) return;
    if (!el) { bubble.style.opacity = '0'; seen = undefined; return; }
    tgt = { x: el.offsetLeft, y: el.offsetTop, w: el.offsetWidth, h: el.offsetHeight };
    const first = seen === undefined;
    const moved = seen !== value;
    seen = value;
    if (first) { snap(); bubble.style.opacity = '1'; return; }
    if (moved && !matchMedia('(prefers-reduced-motion: reduce)').matches) {
      if (!raf) { last = performance.now(); raf = requestAnimationFrame(step); } // already moving: just the new target
    } else if (!raf) snap(); // resize or a width change while idle
  }
  $effect(() => { void value; void options; void tick().then(measure); });
  // A layout change without a window resize (a toolbar line wrapping, the group appearing) moves the chips too.
  $effect(() => {
    if (!box) return;
    const ro = new ResizeObserver(() => measure());
    ro.observe(box);
    return () => ro.disconnect();
  });
  onDestroy(() => cancelAnimationFrame(raf));
</script>

<svelte:window onresize={measure} />
<div bind:this={box} class="relative inline-flex max-w-full items-center gap-0 overflow-x-auto rounded-full border border-line bg-surface p-0.5 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden" role="group" aria-label={label}>
  <span bind:this={bubble} aria-hidden="true" class="pointer-events-none absolute top-0 left-0 rounded-full bg-fg opacity-0"></span>
  {#each options as o}
    <button type="button" aria-pressed={value === o.id} onclick={() => (value = o.id)}
      class="relative z-10 inline-flex h-[26px] shrink-0 cursor-pointer items-center gap-1.5 rounded-full px-3 text-[13px] font-semibold text-fg-muted outline-offset-[-2px] not-data-[under]:hover:text-fg data-[under]:text-surface">
      {o.label}{#if o.count !== undefined}<span class="font-normal">{o.count}</span>{/if}
    </button>
  {/each}
</div>
