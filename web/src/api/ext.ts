// Small types the Graph page shares with the client. The data itself (waiting executions, fork labels, the saved arrangement)
// is part of the generated contract: executions carry waiting_since, projects carry fork_of, GET and PUT /graph-layout.
import type { GraphLayout as Wire } from './models';

/** An execution whose harness said its turn ended and waits for the human. */
export interface WaitingItem {
  execution_id: string;
  /** When it started waiting. */
  since: string;
}
export interface ForkLink {
  project_id: string;
  fork_of: string;
}
/** A side of a node where a line starts or ends. */
export type Side = 'top' | 'right' | 'bottom' | 'left';
/** The arrangement as the page uses it: no nulls (the wire form has them where Go has a nil map or an absent position). */
export interface GraphLayout {
  nodes: Record<string, { x: number; y: number }>;
  lines: Record<string, { from: Side; to: Side; fo?: number; tt?: number }>;
}
export const emptyLayout = (): GraphLayout => ({ nodes: {}, lines: {} });
/** The wire form to the page form. */
export const fromWire = (w: Wire): GraphLayout => ({
  nodes: w.nodes ?? {},
  lines: Object.fromEntries(Object.entries(w.lines ?? {}).map(([k, v]) => [k, { from: v.from, to: v.to, ...(v.fo == null ? {} : { fo: v.fo }), ...(v.tt == null ? {} : { tt: v.tt }) }])),
});
