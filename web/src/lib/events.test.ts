import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MockBus } from '../mock/bus';
import { createEventsClient, type ConnState } from './events';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

function start(bus: MockBus, staleAfterMs = 30_000) {
  const states: ConnState[] = [];
  const events: Array<[string, unknown]> = [];
  let resyncs = 0;
  const c = createEventsClient({ url: '/e', factory: bus.factory, staleAfterMs, backoffMs: () => 100, onEvent: (n, d) => events.push([n, d]), onState: (s) => states.push(s), onResync: () => resyncs++ });
  return { c, states, events, resyncs: () => resyncs };
}

describe('events client', () => {
  it('goes live and delivers events as parsed objects', async () => {
    const bus = new MockBus();
    const t = start(bus);
    await vi.advanceTimersByTimeAsync(5);
    bus.emit('attention.changed', { count: 4 });
    expect(t.states.at(-1)).toBe('live');
    expect(t.events).toContainEqual(['attention.changed', { count: 4 }]);
    t.c.close();
  });

  it('shows Reconnecting on a drop and resyncs nothing when the replay covers the gap', async () => {
    const bus = new MockBus();
    const t = start(bus);
    await vi.advanceTimersByTimeAsync(5);
    bus.drop();
    expect(t.states.at(-1)).toBe('reconnecting');
    bus.emit('attention.changed', { count: 1 }); // missed while down
    await vi.advanceTimersByTimeAsync(100);
    expect(t.states.at(-1)).toBe('live');
    expect(t.events.filter(([n]) => n === 'attention.changed')).toHaveLength(1); // replayed by Last-Event-ID
    t.c.close();
  });

  it('opens a new stream with backoff when the connection was refused, then resyncs', async () => {
    const bus = new MockBus();
    bus.refuse = true;
    const t = start(bus);
    await vi.advanceTimersByTimeAsync(5);
    expect(t.states.at(-1)).toBe('reconnecting');
    await vi.advanceTimersByTimeAsync(350);
    expect(t.states.at(-1)).toBe('reconnecting');
    bus.refuse = false;
    await vi.advanceTimersByTimeAsync(400);
    expect(t.states.at(-1)).toBe('live');
    expect(t.resyncs()).toBe(1); // a fresh EventSource has no Last-Event-ID
    t.c.close();
  });

  it('stops reconnecting once the session has ended', async () => {
    const bus = new MockBus();
    bus.refuse = true;
    let checks = 0;
    const opened: string[] = [];
    const c = createEventsClient({ url: '/e', factory: (u) => (opened.push(u), bus.factory(u)), backoffMs: () => 100, onEvent: () => {}, onState: () => {}, onResync: () => {},
      shouldRetry: async () => (checks++, false) });
    await vi.advanceTimersByTimeAsync(1000);
    expect(checks).toBe(1);
    expect(opened).toHaveLength(1); // no new stream after the session check said no
    c.close();
  });

  it('turns Stale when the stream stays down past the deadline', async () => {
    const bus = new MockBus();
    bus.refuse = true;
    const t = start(bus, 1000);
    await vi.advanceTimersByTimeAsync(1100);
    expect(t.states).toContain('stale');
    t.c.close();
  });

  it('refetches on a reset event and ignores a malformed one', async () => {
    const bus = new MockBus();
    const t = start(bus);
    await vi.advanceTimersByTimeAsync(5);
    bus.emit('reset', {});
    expect(t.resyncs()).toBe(1);
    const src = (bus as any).sources.values().next().value;
    src.deliver({ id: 99, name: 'task.changed', data: undefined }); // JSON.stringify(undefined) is not valid JSON
    expect(t.events.filter(([n]) => n === 'task.changed')).toHaveLength(0);
    t.c.close();
  });

  it('stops for good after close', async () => {
    const bus = new MockBus();
    bus.refuse = true;
    const t = start(bus);
    await vi.advanceTimersByTimeAsync(5);
    t.c.close();
    bus.refuse = false;
    await vi.advanceTimersByTimeAsync(5000);
    expect(t.states.at(-1)).toBe('reconnecting');
  });
});
