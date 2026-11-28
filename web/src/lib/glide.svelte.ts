// Moves a set of named rectangles to their new values, each over its own time, so that everything drawn from them (cards,
// frames and the lines between them) stays together on the way. A time of 0 is at once. A rectangle whose target did not
// change keeps going as it was, so a steady stream of other changes (a drag) does not stall it.
import { untrack } from 'svelte';
import type { Rect } from './graphroom';

export interface Pace { ms: number; ease: (t: number) => number }

export function glide(target: () => Record<string, Rect>, pace: (key: string) => Pace) {
  let shown = $state.raw<Record<string, Rect>>({});
  const tracks = new Map<string, Pace & { from: Rect; to: Rect; start?: number }>();
  let frame = 0;
  const same = (a: Rect, b: Rect) => a.x === b.x && a.y === b.y && a.w === b.w && a.h === b.h;
  // Where every rectangle is at time `now`; asks for another frame while one is still on its way.
  function draw(now?: number) {
    let going = false;
    shown = Object.fromEntries([...tracks].map(([key, t]) => {
      // A new target between two frames: what is on its way stays as drawn until the next frame.
      if (t.ms && now === undefined) { going = true; return [key, shown[key] ?? t.from]; }
      if (t.ms) t.start ??= now;
      const p = !t.ms ? 1 : Math.min(1, (now! - t.start!) / t.ms);
      if (p === 1) { t.ms = 0; return [key, t.to]; }
      going = true;
      const e = t.ease(p), mix = (u: number, v: number) => u + (v - u) * e;
      return [key, { x: mix(t.from.x, t.to.x), y: mix(t.from.y, t.to.y), w: mix(t.from.w, t.to.w), h: mix(t.from.h, t.to.h) }];
    }));
    cancelAnimationFrame(frame);
    if (going) frame = requestAnimationFrame(draw);
  }
  $effect(() => {
    const to = target();
    untrack(() => {
      for (const key of tracks.keys()) if (!(key in to)) tracks.delete(key);
      for (const [key, b] of Object.entries(to)) {
        const old = tracks.get(key);
        if (old && same(old.to, b)) continue;
        const from = shown[key];
        tracks.set(key, from ? { ...pace(key), from, to: b } : { ms: 0, ease: (t) => t, from: b, to: b });
      }
      draw();
    });
  });
  $effect(() => () => cancelAnimationFrame(frame));
  return { get current() { return shown; } };
}
