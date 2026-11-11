// A fake server-sent-events source for the mock adapter. It mimics what a browser
// EventSource does: events carry ids, a dropped connection reconnects with
// Last-Event-ID, and a refused connection closes for good.
import type { EventPayloads } from '../api/models';
import type { EventSourceLike } from '../lib/events';

export type EventName = keyof EventPayloads;
interface Stored { id: number; name: string; data: unknown }

export class MockBus {
  private nextId = 1;
  private ring: Stored[] = [];
  private sources = new Set<MockEventSource>();
  refuse = false;

  emit<N extends EventName>(name: N, data: EventPayloads[N]) {
    const ev = { id: this.nextId++, name, data };
    this.ring.push(ev);
    if (this.ring.length > 500) this.ring.shift();
    for (const s of this.sources) s.deliver(ev);
  }

  since(id: number) {
    return this.ring.filter((e) => e.id > id);
  }

  lastId() {
    return this.nextId - 1;
  }

  attach(s: MockEventSource) {
    this.sources.add(s);
  }

  detach(s: MockEventSource) {
    this.sources.delete(s);
  }

  /** Simulate a network drop: every source errors and tries again later. */
  drop() {
    for (const s of [...this.sources]) s.fail(false);
  }

  /** Factory handed to the events client. */
  factory = (url: string): EventSourceLike => new MockEventSource(this, url);
}

export class MockEventSource implements EventSourceLike {
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  readyState = 0;
  private handlers = new Map<string, Set<(e: { data: string; lastEventId: string }) => void>>();
  private lastId = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;

  constructor(private bus: MockBus, public url: string) {
    this.timer = setTimeout(() => this.open(), 0);
  }

  private open() {
    if (this.readyState === 2) return;
    if (this.bus.refuse) return this.fail(true);
    this.readyState = 1;
    this.bus.attach(this);
    this.onopen?.();
    // First connection gets a hello; a reconnect replays what was missed, like the server ring buffer.
    if (this.lastId === 0) this.deliver({ id: 0, name: 'hello', data: { server_version: '0.1.0', audit_ok: true } });
    for (const e of this.bus.since(this.lastId)) this.deliver(e);
  }

  deliver(e: Stored) {
    if (this.readyState !== 1) return;
    if (e.id) this.lastId = e.id;
    for (const h of this.handlers.get(e.name) ?? []) h({ data: JSON.stringify(e.data), lastEventId: String(e.id) });
  }

  fail(permanent: boolean) {
    this.bus.detach(this);
    this.readyState = permanent ? 2 : 0;
    this.onerror?.();
    if (!permanent) this.timer = setTimeout(() => this.open(), 50);
  }

  addEventListener(name: string, fn: (e: { data: string; lastEventId: string }) => void) {
    if (!this.handlers.has(name)) this.handlers.set(name, new Set());
    this.handlers.get(name)!.add(fn);
  }

  close() {
    clearTimeout(this.timer);
    this.readyState = 2;
    this.bus.detach(this);
  }
}
