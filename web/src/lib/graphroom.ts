// Making room on the Graph: when something opens and grows, what is in its way (or nearer than `near`) steps aside, to the
// right or down, keeping the spacing it had up to `far`. Nothing is saved: with nothing grown, everything is where it was.
import type { Side } from '../api/ext';

export interface Rect { x: number; y: number; w: number; h: number }
/** `was` is the place and size with nothing opened; `w` and `h` the size now. A fixed item pushes but is never pushed. */
export interface Item { id: string; was: Rect; w: number; h: number; fixed?: boolean }

export function makeRoom(items: Item[], near: number, far: number): Map<string, { x: number; y: number }> {
  const at = new Map(items.map((i) => [i.id, { x: i.was.x, y: i.was.y }]));
  const now = (i: Item): Rect => ({ ...at.get(i.id)!, w: i.w, h: i.h });
  const queue = items.filter((i) => i.w > i.was.w || i.h > i.was.h).sort((p, q) => p.was.y - q.was.y || p.was.x - q.was.x);
  // Items only ever move right or down; the cap guards against a ring of items pushing each other.
  for (let n = 0; queue.length && n < 1000; n++) {
    const p = queue.shift()!;
    for (const q of items) {
      // An overlap that was there before anything opened is the human's own arrangement and stays.
      if (q === p || q.fixed || !overlaps(now(p), now(q), near) || overlaps(p.was, q.was)) continue;
      const a = now(p), here = at.get(q.id)!;
      const gap = (had: number) => Math.min(far, Math.max(near, had));
      const ways: Array<{ x: number; y: number }> = [];
      if (q.was.x >= p.was.x + p.was.w) ways.push({ x: a.x + a.w + gap(q.was.x - p.was.x - p.was.w), y: here.y });
      if (q.was.y >= p.was.y + p.was.h) ways.push({ x: here.x, y: a.y + a.h + gap(q.was.y - p.was.y - p.was.h) });
      const cost = (w: { x: number; y: number }) => w.x - here.x + w.y - here.y;
      const best = ways.filter((w) => cost(w) > 0).sort((v, w) => cost(v) - cost(w))[0];
      if (!best) continue;
      at.set(q.id, best);
      queue.push(q);
    }
  }
  return at;
}

/** The sides a line should leave `a` and enter `b` by: the two that look at each other across the wider gap. */
export function facing(a: Rect, b: Rect): { from: Side; to: Side } {
  const dx = b.x + b.w / 2 - (a.x + a.w / 2), dy = b.y + b.h / 2 - (a.y + a.h / 2);
  const gx = Math.max(b.x - a.x - a.w, a.x - b.x - b.w), gy = Math.max(b.y - a.y - a.h, a.y - b.y - b.h);
  // Overlapping both ways: no gap to go by, so the longer way between the middles.
  const across = gx < 0 && gy < 0 ? Math.abs(dx) >= Math.abs(dy) : gx >= gy;
  if (across) return dx >= 0 ? { from: 'right', to: 'left' } : { from: 'left', to: 'right' };
  return dy >= 0 ? { from: 'bottom', to: 'top' } : { from: 'top', to: 'bottom' };
}

export const overlaps = (a: Rect, b: Rect, margin = 0) => a.x < b.x + b.w + margin && b.x < a.x + a.w + margin && a.y < b.y + b.h + margin && b.y < a.y + a.h + margin;

/** How far each box has to move down so that none is closer than `gap` to another; the upper one stays. */
export function apart(list: Array<Rect & { id: string }>, gap: number): Map<string, number> {
  const done: Rect[] = [], down = new Map<string, number>();
  for (const r of [...list].sort((p, q) => p.y - q.y || p.x - q.x)) {
    let y = r.y;
    for (let on = done.find((d) => overlaps({ ...r, y }, d, gap)); on; on = done.find((d) => overlaps({ ...r, y }, d, gap))) y = on.y + on.h + gap;
    done.push({ ...r, y });
    down.set(r.id, y - r.y);
  }
  return down;
}

type Point = { x: number; y: number };
/** The furthest point from `from` toward `to` that `clear` accepts, one axis after the other, so a drag slides along a wall. */
export function slide(from: Point, to: Point, clear: (p: Point) => boolean): Point {
  if (clear(to)) return to;
  const at = { ...from };
  for (const axis of ['x', 'y'] as const) {
    if (clear({ ...at, [axis]: to[axis] })) { at[axis] = to[axis]; continue; }
    let ok = at[axis], no = to[axis];
    for (let i = 0; i < 16; i++) {
      const mid = (ok + no) / 2;
      if (clear({ ...at, [axis]: mid })) ok = mid;
      else no = mid;
    }
    at[axis] = ok;
  }
  return at;
}

/** No two boxes closer than `gap`. The held ones stay, as do those clear of them and of each other; every other one goes to
 *  the nearest free place that `ok` accepts. */
// shortcut: cubic in the phases of one project at worst; fine for dozens, to be indexed if a project holds hundreds.
export function dodge(list: Array<Rect & { id: string }>, held: ReadonlySet<string>, gap: number, ok: (r: Rect) => boolean = () => true): Map<string, Point> {
  const placed: Rect[] = [], at = new Map<string, Point>(), waiting: typeof list = [];
  const free = (r: Rect) => !placed.some((p) => overlaps(r, p, gap));
  const put = (r: (typeof list)[number]) => { placed.push(r); at.set(r.id, { x: r.x, y: r.y }); };
  for (const r of list) if (held.has(r.id)) put(r);
  for (const r of list) {
    if (held.has(r.id)) continue;
    if (free(r)) put(r);
    else waiting.push(r);
  }
  for (const r of waiting) {
    // The nearest free place lies against a side of something that stands, or keeps one of its own coordinates.
    const xs = [r.x, ...placed.flatMap((p) => [p.x + p.w + gap, p.x - r.w - gap])];
    const ys = [r.y, ...placed.flatMap((p) => [p.y + p.h + gap, p.y - r.h - gap])];
    let best: Point | undefined, cost = Infinity;
    for (const x of xs) {
      for (const y of ys) {
        const c = (x - r.x) ** 2 + (y - r.y) ** 2, to = { ...r, x, y };
        if (c < cost && x >= 0 && y >= 0 && free(to) && ok(to)) { best = { x, y }; cost = c; }
      }
    }
    put({ ...r, ...(best ?? { x: r.x, y: Math.max(...placed.map((p) => p.y + p.h + gap)) }) }); // under everything is always free
  }
  return at;
}
