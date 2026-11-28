import { describe, expect, it } from 'vitest';
import { apart, dodge, facing, makeRoom, overlaps, slide, type Item } from './graphroom';

// Three nodes in a row, 100 wide and 96 apart; `grow` is the size one of them opens to.
const node = (id: string, x: number, y = 0, grow?: { w: number; h: number }, fixed = false): Item => ({ id, was: { x, y, w: 100, h: 100 }, w: grow?.w ?? 100, h: grow?.h ?? 100, fixed });
const open = { w: 260, h: 300 };
const at = (items: Item[]) => Object.fromEntries(makeRoom(items, 24, 96));

describe('makeRoom', () => {
  it('moves nothing while nothing grows, however close the items are', () => {
    expect(at([node('a', 0), node('b', 104), node('c', 50)])).toEqual({ a: { x: 0, y: 0 }, b: { x: 104, y: 0 }, c: { x: 50, y: 0 } });
  });
  it('pushes the row along and keeps the spacing it had', () => {
    const p = at([node('a', 0, 0, open), node('b', 196), node('c', 392)]);
    expect(p.b).toEqual({ x: 356, y: 0 }); // 260 + 96
    expect(p.c).toEqual({ x: 552, y: 0 }); // pushed on by b
  });
  it('pushes an item that is merely near, to a clear distance', () => {
    expect(at([node('a', 0, 0, open), node('b', 270)]).b).toEqual({ x: 356, y: 0 }); // it had 170 of room: kept up to 96
    expect(at([node('a', 0, 0, open), node('b', 110)]).b).toEqual({ x: 284, y: 0 }); // it had 10: now 24
  });
  it('pushes an item below downwards', () => {
    expect(at([node('a', 0, 0, open), node('b', 0, 140)]).b).toEqual({ x: 0, y: 340 });
  });
  it('takes the shorter way for an item that is below and to the right', () => {
    expect(at([node('a', 0, 0, open), node('b', 120, 290)]).b).toEqual({ x: 120, y: 396 });
  });
  it('leaves alone what is far, above, to the left, or already overlapped by the human', () => {
    const p = at([node('a', 300, 300, open), node('far', 900, 300), node('left', 190, 300), node('up', 300, 190), node('over', 350, 350)]);
    expect(p.far).toEqual({ x: 900, y: 300 });
    expect(p.left).toEqual({ x: 190, y: 300 });
    expect(p.up).toEqual({ x: 300, y: 190 });
    expect(p.over).toEqual({ x: 350, y: 350 });
  });
  it('never moves a fixed item', () => {
    expect(at([node('a', 0, 0, open), node('b', 196, 0, undefined, true)]).b).toEqual({ x: 196, y: 0 });
  });
});

const box = (x: number, y: number, w = 100, h = 100) => ({ x, y, w, h });

describe('facing', () => {
  const a = box(0, 0);
  it('joins the sides that look at each other', () => {
    expect(facing(a, box(196, 0))).toEqual({ from: 'right', to: 'left' });
    expect(facing(a, box(-196, 20))).toEqual({ from: 'left', to: 'right' });
    expect(facing(a, box(0, 196))).toEqual({ from: 'bottom', to: 'top' });
    expect(facing(a, box(30, -196))).toEqual({ from: 'top', to: 'bottom' });
  });
  it('follows the wider gap for a diagonal neighbour, and the longer way for an overlapping one', () => {
    expect(facing(a, box(120, 250))).toEqual({ from: 'bottom', to: 'top' });
    expect(facing(a, box(250, 120))).toEqual({ from: 'right', to: 'left' });
    expect(facing(a, box(10, 60))).toEqual({ from: 'bottom', to: 'top' });
    expect(facing(a, box(60, 10))).toEqual({ from: 'right', to: 'left' });
  });
  it('stays level beside a tall open card', () => {
    expect(facing(box(0, 0, 264, 309), box(360, 0, 144, 121))).toEqual({ from: 'right', to: 'left' });
  });
});

describe('apart', () => {
  const run = (list: Array<{ id: string } & ReturnType<typeof box>>) => Object.fromEntries(apart(list, 24));
  it('leaves boxes that stand clear', () => {
    expect(run([{ id: 'a', ...box(0, 0) }, { id: 'b', ...box(124, 0) }, { id: 'c', ...box(0, 124) }])).toEqual({ a: 0, b: 0, c: 0 });
  });
  it('gives a box that is too close the gap', () => {
    expect(run([{ id: 'a', ...box(0, 0) }, { id: 'b', ...box(0, 110) }])).toEqual({ a: 0, b: 14 });
  });
  it('moves the lower of two overlapping boxes below, and on past the next one', () => {
    expect(run([{ id: 'b', ...box(50, 50) }, { id: 'a', ...box(0, 0) }, { id: 'c', ...box(0, 150) }])).toEqual({ a: 0, b: 74, c: 98 });
  });
});

describe('slide', () => {
  const wall = (p: { x: number; y: number }) => !(p.x > 50 && p.y > -10 && p.y < 10);
  it('goes all the way when nothing is there, also past a wall', () => {
    expect(slide({ x: 0, y: 40 }, { x: 80, y: 40 }, wall)).toEqual({ x: 80, y: 40 });
  });
  it('stops at a wall and slides along it', () => {
    const p = slide({ x: 0, y: 0 }, { x: 80, y: 5 }, wall);
    expect(p.x).toBeGreaterThan(49);
    expect(p.x).toBeLessThanOrEqual(50);
    expect(p.y).toBe(5);
  });
});

describe('dodge', () => {
  const run = (list: Array<{ id: string } & ReturnType<typeof box>>, held: string[] = [], ok?: (r: { x: number; y: number }) => boolean) => Object.fromEntries(dodge(list, new Set(held), 24, ok));
  it('moves nothing that stands clear', () => {
    expect(run([{ id: 'a', ...box(0, 0) }, { id: 'b', ...box(124, 0) }], ['a'])).toEqual({ a: { x: 0, y: 0 }, b: { x: 124, y: 0 } });
  });
  it('sends the one that is approached to the nearest free place, never the held one', () => {
    expect(run([{ id: 'a', ...box(300, 300) }, { id: 'b', ...box(220, 300) }], ['b'])).toEqual({ b: { x: 220, y: 300 }, a: { x: 344, y: 300 } });
    expect(run([{ id: 'a', ...box(300, 300) }, { id: 'b', ...box(300, 230) }], ['b']).a).toEqual({ x: 300, y: 354 });
  });
  it('does not put one on another that stands clear, and leaves that one alone', () => {
    const list = [{ id: 'a', ...box(300, 300) }, { id: 'c', ...box(424, 300) }, { id: 'b', ...box(250, 300) }];
    const p = run(list, ['b']);
    expect(p.c).toEqual({ x: 424, y: 300 });
    const placed = list.map((r) => ({ ...r, ...p[r.id]! }));
    for (const [i, r] of placed.entries()) for (const q of placed.slice(i + 1)) expect(overlaps(r, q, 24)).toBe(false);
  });
  it('keeps to places that are allowed and inside the drawing', () => {
    expect(run([{ id: 'a', ...box(10, 10) }, { id: 'b', ...box(60, 10) }], ['b']).a!.x).toBeGreaterThanOrEqual(0);
    expect(run([{ id: 'a', ...box(300, 300) }, { id: 'b', ...box(220, 300) }], ['b'], (r) => r.x <= 300).a!.x).toBeLessThanOrEqual(300);
    expect(run([{ id: 'a', ...box(300, 300) }, { id: 'b', ...box(220, 300) }], ['b'], () => false).a).toEqual({ x: 300, y: 424 }); // below is the last resort
  });
});
