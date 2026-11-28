import { afterEach, describe, expect, it } from 'vitest';
import { mount, unmount, type Component } from 'svelte';
import Bar from './Bar.svelte';
import StatusDot from './StatusDot.svelte';
import PhaseChain from './PhaseChain.svelte';
import DiffBar from './DiffBar.svelte';
import ValidatorRing from './ValidatorRing.svelte';
import { label } from './status';

let made: Record<string, unknown>[] = [];
function draw<P extends Record<string, unknown>>(c: Component<P>, props: P) {
  const target = document.createElement('div');
  made.push(mount(c, { target, props }));
  return target;
}
afterEach(() => { for (const m of made) void unmount(m); made = []; });

describe('bar', () => {
  it('clamps a value and says it', () => {
    const b = draw(Bar, { label: 'p', value: 140 }).querySelector('[role=progressbar]')!;
    expect(b.getAttribute('aria-valuenow')).toBe('100');
    expect(b.querySelector('i')!.getAttribute('style')).toBe('width: 100%;');
  });
  it('a running bar claims no amount', () => {
    const b = draw(Bar, { label: 'p', running: true }).querySelector('[role=progressbar]')!;
    expect(b.classList.contains('run')).toBe(true);
    expect(b.hasAttribute('aria-valuenow')).toBe(false);
    expect(b.querySelector('i')!.getAttribute('style')).toBeNull();
  });
});

describe('status shapes', () => {
  it('every kind of state has its own shape and a name', () => {
    const states = ['done', 'running', 'waiting_for_instruction', 'awaiting_approval', 'interrupted', 'failed', 'queued'];
    const dots = states.map((state) => draw(StatusDot, { state }).firstElementChild!);
    // Take the colours away: what is left must still tell all seven apart.
    expect(new Set(dots.map((d) => d.className.replace(/(bg|border|text)-(st-[a-z]+|queued)/g, ''))).size).toBe(states.length);
    for (const d of dots) expect(d.getAttribute('aria-label')).toBeTruthy();
  });
  it('a chain names each task and its state', () => {
    const t = draw(PhaseChain, { phases: [{ id: 'p', code: 'P1', title: 'Setup', tasks: [{ id: 'a', code: 'T1.1', title: 'One', state: 'done' }, { id: 'b', code: 'T1.2', title: 'Two', state: 'blocked' }] }] });
    expect([...t.querySelectorAll('[role=img]')].map((d) => d.getAttribute('aria-label'))).toEqual(['T1.1 One: Done', 'T1.2 Two: Blocked']);
  });
  it('a diff bar draws only the parts that exist', () => {
    expect(draw(DiffBar, { added: 4, removed: 0 }).querySelectorAll('span')).toHaveLength(1);
  });
  it('the ring never overfills, and draws no arc without validators', () => {
    expect(draw(ValidatorRing, { passed: 0, total: 0 }).querySelectorAll('circle')).toHaveLength(1);
    expect(draw(ValidatorRing, { passed: 3, total: 3 }).querySelectorAll('circle')[1]!.getAttribute('stroke')).toBe('var(--status-ok)');
  });
  it('uses the agreed word for an execution that waits for approval', () => {
    expect(label('waiting_approval')).toBe('Needs decision');
    expect(label('waiting_for_instruction')).toBe('Waiting for instruction');
    expect(label('constructor')).toBe('Constructor');
  });
});
