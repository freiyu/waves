<script lang="ts">
  // The Graph, full screen: every project as a group of phase nodes joined by lines. Nodes and frames are HTML and CSS, the lines one SVG
  // layer (no graph package). All tools live in a side pane on the right that opens and closes; a phase or task detail opens to the left
  // of it. A line can leave and enter any side of its nodes (drag its end circle round the node, or pick the sides in the tools).
  // Dragging only moves saved positions, never workflow data.
  import { onDestroy, tick } from 'svelte';
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { shellData } from '../lib/shell';
  import { href } from '../lib/router.svelte';
  import { waitingResource, waitedFor } from '../lib/waiting.svelte';
  import ReleaseLease from '../lib/ReleaseLease.svelte';
  import { visible } from '../lib/text';
  import { label } from '../lib/status';
  import type { PhaseRef, ProjectDetail, TaskSummary } from '../api/models';
  import { emptyLayout, type GraphLayout, type Side } from '../api/ext';
  import Badge from '../lib/Badge.svelte';
  import Btn from '../lib/Btn.svelte';
  import IconBtn from '../lib/IconBtn.svelte';
  import Icon from '../lib/Icon.svelte';
  import View from '../lib/View.svelte';
  import DecideFlow from '../lib/DecideFlow.svelte';
  import { toast } from 'svelte-sonner';

  const a = app();
  const { projects, status } = shellData();
  const broken = $derived(status.data?.audit.state === 'broken');
  const details = resource(async () => {
    const list = projects.data?.items ?? [];
    return (await Promise.all(list.map((p) => a.api.project(p.id)))).filter((d) => d.phases.length);
  }, ['project.changed', 'phase.changed', 'task.changed', 'execution.changed', 'approval.changed', 'workflow.changed']);
  let forksFailed = $state(false);
  const forks = resource(async () => {
    try {
      forksFailed = false;
      return (await a.api.x.forks()).items;
    } catch {
      forksFailed = true;
      return [];
    }
  }, ['project.changed']);
  // A layout that could not be read is drawn with the defaults, but never saved over: the stored one stays as it is.
  let layoutReadFailed = $state(false);
  const saved = resource(async (): Promise<GraphLayout> => {
    try {
      layoutReadFailed = false;
      return await a.api.x.positions();
    } catch {
      layoutReadFailed = true;
      return emptyLayout();
    }
  });
  const waiting = waitingResource();
  const audit = resource(() => a.api.audit(), ['approval.changed', 'phase.changed']);

  // ---- geometry (px in the canvas world) ----
  const W = 224, GAP = 124, TAG = 40, PORT_Y = 51, BASE_H = 118, ROW_H = 30;
  type Node = { id: string; d: ProjectDetail; phase: PhaseRef; i: number; x: number; y: number; h: number; tasks: TaskSummary[]; open: boolean; hasTasks: boolean };
  const SIDES: Side[] = ['top', 'right', 'bottom', 'left'];
  const SIDE_LABEL: Record<Side, string> = { top: 'Top', right: 'Right', bottom: 'Bottom', left: 'Left' };

  const narrow = typeof matchMedia !== 'undefined' && matchMedia('(max-width: 860px)').matches;
  let view = $state<'graph' | 'table'>(narrow ? 'table' : 'graph');
  let toolsOpen = $state(!narrow);
  let showFrames = $state(true);
  let off = $state(new Set<string>()); // projects switched off with the chips
  let open = $state(new Set<string>());
  let seeded = false;
  let pos = $state<GraphLayout['nodes']>({});
  let lines = $state<GraphLayout['lines']>({});
  let loadedLayout = false;
  let selected = $state<string | null>(null);
  let selTask = $state<string | null>(null);
  let paneTab = $state<'summary' | 'activity'>('summary');
  let k = $state(1);
  let pan = $state({ x: 0, y: 0 });
  let settle = $state(false); // a rubber band or a reveal is moving the world: the one moment a transition is allowed on it
  let held = $state(false);
  let heights = $state<Record<string, number>>({}); // the real height of each node card, measured by the browser
  let settleTimer: ReturnType<typeof setTimeout> | undefined;
  const settleFor = () => {
    settle = true;
    clearTimeout(settleTimer);
    settleTimer = setTimeout(() => (settle = false), 260);
  };

  const sideOk = (s: unknown): s is Side => SIDES.includes(s as Side);
  $effect(() => {
    if (!loadedLayout && saved.data) {
      const n = Object.fromEntries(Object.entries(saved.data.nodes ?? {}).filter(([, v]) => v && Number.isFinite(v.x) && Number.isFinite(v.y)));
      const t01 = (v: unknown) => (typeof v === 'number' && v >= 0 && v <= 1 ? v : undefined);
      const l = Object.fromEntries(Object.entries(saved.data.lines ?? {}).filter(([, v]) => v && sideOk(v.from) && sideOk(v.to)).map(([key, v]) => [key, { from: v.from, to: v.to, ...(t01(v.fo) === undefined ? {} : { fo: v.fo }), ...(t01(v.tt) === undefined ? {} : { tt: v.tt }) }]));
      pos = { ...n, ...pos };
      lines = { ...l, ...lines };
      loadedLayout = true;
      if (pendingSave) queueSave(); // the human changed something while the saved layout was loading
    }
  });
  let fitted = false;
  $effect(() => {
    // The first drawing of an arrangement nobody has changed fits the width of the graph (no further automatic zooming).
    if (!fitted && canvas && nodes.length && saved.data && Object.keys(saved.data.nodes ?? {}).length === 0) {
      fitted = true;
      queueMicrotask(() => fit(false));
    }
  });
  $effect(() => {
    // Phases that are running open on first sight; the human's later choices are never overwritten.
    // Only once there is something to open: on a direct visit the project list is still loading and the first answer is empty.
    if (!seeded && details.data?.length) {
      seeded = true;
      open = new Set(details.data.flatMap((d) => d.phases.filter((p) => p.state === 'in_progress').map((p) => p.id)));
    }
  });

  const isCurrent = (d: ProjectDetail, p: PhaseRef) => d.tasks_phase === p.id;
  const tasksOf = (d: ProjectDetail, p: PhaseRef): TaskSummary[] => (isCurrent(d, p) ? d.tasks : []);

  // A fork is drawn right under the project it was forked from, so its line never crosses another group.
  const shown = $derived.by(() => {
    const vis = (details.data ?? []).filter((d) => !off.has(d.id));
    const forkOf = new Map((forks.data ?? []).map((f) => [f.project_id, f.fork_of]));
    const roots = vis.filter((d) => !forkOf.has(d.id) || !vis.some((x) => x.id === forkOf.get(d.id)));
    return roots.flatMap((r) => [r, ...vis.filter((d) => forkOf.get(d.id) === r.id)]);
  });
  const nodes = $derived.by(() => {
    const out: Node[] = [];
    if (!saved.data) return out; // nothing is drawn before the saved arrangement is known, so the first fit never moves a drawn phase
    let gy = 64; // room above the first frame for the title of the page
    for (const d of shown) {
      const base = gy + TAG + 16;
      let tallest = BASE_H;
      d.phases.forEach((p, i) => {
        const tasks = tasksOf(d, p);
        const isOpen = open.has(p.id);
        const est = isOpen && tasks.length ? BASE_H + tasks.length * ROW_H + 12 : BASE_H;
        const h = heights[p.id] ?? est; // ports and frames use the measured card height; the row spacing below uses the estimate, so a measurement never moves a phase
        tallest = Math.max(tallest, est);
        const at = pos[p.id];
        out.push({ id: p.id, d, phase: p, i, x: at?.x ?? 48 + i * (W + GAP), y: at?.y ?? base, h, tasks, open: isOpen, hasTasks: tasks.length > 0 });
      });
      gy = base + tallest + 96;
    }
    return out;
  });
  const byId = $derived(new Map(nodes.map((n) => [n.id, n])));
  const groups = $derived(
    shown.flatMap((d) => {
      const ns = nodes.filter((n) => n.d.id === d.id);
      if (!ns.length) return []; // before the saved arrangement is known there is nothing to frame
      const x = Math.min(...ns.map((n) => n.x)) - 24, y = Math.min(...ns.map((n) => n.y)) - TAG - 8;
      return [{ d, ids: ns.map((n) => n.id), x, y, w: Math.max(...ns.map((n) => n.x + W)) - x + 24, h: Math.max(...ns.map((n) => n.y + n.h)) - y + 24 }];
    }),
  );
  const gById = $derived(new Map(groups.map((g) => [g.d.id, g])));

  // A point on the middle of a side of a node, and the direction a line leaves it in.
  // t (0 to 1) is where along the side the line meets it; without it the default point is used.
  function port(n: { x: number; y: number; h: number }, s: Side, t?: number) {
    const y = t === undefined ? n.y + PORT_Y : n.y + t * n.h, x = n.x + (t === undefined ? W / 2 : t * W);
    return s === 'right' ? { x: n.x + W, y, vx: 1, vy: 0 }
      : s === 'left' ? { x: n.x, y, vx: -1, vy: 0 }
      : s === 'top' ? { x, y: n.y, vx: 0, vy: -1 }
      : { x, y: n.y + n.h, vx: 0, vy: 1 };
  }
  type Edge = { key: string; path: string; mx: number; my: number; text: string; fork: boolean; x1: number; y1: number; x2: number; y2: number; from?: Node; to?: Node; fs: Side; ts: Side };
  function curve(p1: ReturnType<typeof port>, p2: ReturnType<typeof port>, min = 36) {
    // Each end leaves its side for half of the distance along its own axis, so a short vertical hop never loops.
    const d1 = Math.max(min, (p1.vx ? Math.abs(p2.x - p1.x) : Math.abs(p2.y - p1.y)) * 0.5);
    const d2 = Math.max(min, (p2.vx ? Math.abs(p2.x - p1.x) : Math.abs(p2.y - p1.y)) * 0.5);
    const c1 = { x: p1.x + p1.vx * d1, y: p1.y + p1.vy * d1 }, c2 = { x: p2.x + p2.vx * d2, y: p2.y + p2.vy * d2 };
    return { path: `M${p1.x} ${p1.y} C${c1.x} ${c1.y} ${c2.x} ${c2.y} ${p2.x} ${p2.y}`, mx: (p1.x + 3 * c1.x + 3 * c2.x + p2.x) / 8, my: (p1.y + 3 * c1.y + 3 * c2.y + p2.y) / 8 };
  }
  const edges = $derived.by(() => {
    const out: Edge[] = [];
    for (const g of groups) {
      const ns = g.ids.map((id) => byId.get(id)!).sort((p, q) => p.i - q.i);
      for (let j = 1; j < ns.length; j++) {
        const p = ns[j - 1]!, q = ns[j]!;
        const key = `${p.id}>${q.id}`;
        const fs = lines[key]?.from ?? 'right', ts = lines[key]?.to ?? 'left';
        const a1 = port(p, fs, lines[key]?.fo), a2 = port(q, ts, lines[key]?.tt);
        out.push({ key, ...curve(a1, a2), text: p.phase.state === 'completed' ? 'Approved' : 'Gate', fork: false, x1: a1.x, y1: a1.y, x2: a2.x, y2: a2.y, from: p, to: q, fs, ts });
      }
    }
    for (const f of forks.data ?? []) {
      const from = gById.get(f.fork_of), to = gById.get(f.project_id);
      if (!from || !to) continue;
      const a1 = port({ x: from.x + from.w / 2 - W / 2, y: from.y, h: from.h }, 'bottom'), a2 = port({ x: to.x + to.w / 2 - W / 2, y: to.y, h: to.h }, 'top');
      out.push({ key: 'fork' + f.project_id, ...curve(a1, a2, 60), text: `fork of ${from.d.name}`, fork: true, x1: a1.x, y1: a1.y, x2: a2.x, y2: a2.y, fs: 'bottom', ts: 'top' });
    }
    return out;
  });
  const world = $derived({ w: Math.max(1200, ...nodes.map((n) => n.x + W + 80)), h: Math.max(700, ...nodes.map((n) => n.y + n.h + 80)) });

  // ---- canvas: pan, zoom, drag ----
  let canvas = $state<HTMLElement | null>(null);
  type Drag = { kind: 'pan' | 'nodes' | 'port'; sx: number; sy: number; ids: string[]; orig: Record<string, { x: number; y: number }>; px: number; py: number; moved: boolean; focusId?: string; edge?: string; end?: 'from' | 'to' };
  let drag: Drag | null = null;
  let heldIds = $state<string[]>([]); // the ids being dragged, for the template (the drag record itself is not reactive)

  function bounds() {
    const r = canvas?.getBoundingClientRect();
    return { cw: r?.width ?? 800, ch: r?.height ?? 600 };
  }
  // Past the content edge the canvas follows the finger at a third of the distance, then eases back on release.
  const rubber = (v: number, lo: number, hi: number) => (v > hi ? hi + (v - hi) / 3 : v < lo ? lo + (v - lo) / 3 : v);
  function limits() {
    const { cw, ch } = bounds();
    return { hiX: 120, loX: Math.min(120, cw - world.w * k - 120), hiY: 80, loY: Math.min(80, ch - world.h * k - 80) };
  }
  function panDown(e: PointerEvent) {
    if (e.button !== 0 || (e.target as Element).closest('[data-node],[data-gtag],[data-port],button,a')) return;
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    drag = { kind: 'pan', sx: e.clientX, sy: e.clientY, ids: [], orig: {}, px: pan.x, py: pan.y, moved: false };
    held = true;
    settle = false;
    clearTimeout(settleTimer);
  }
  function nodeDown(e: PointerEvent, ids: string[], focusId?: string) {
    if (e.button !== 0 || (e.target as Element).closest('button,a')) return;
    e.stopPropagation();
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    heldIds = ids;
    drag = { kind: 'nodes', sx: e.clientX, sy: e.clientY, ids, orig: Object.fromEntries(ids.map((id) => [id, { x: byId.get(id)!.x, y: byId.get(id)!.y }])), px: 0, py: 0, moved: false, focusId };
    held = true;
  }
  function portDown(e: PointerEvent, edge: Edge, end: 'from' | 'to') {
    if (e.button !== 0 || edge.fork) return;
    e.stopPropagation();
    (e.currentTarget as Element).setPointerCapture(e.pointerId);
    drag = { kind: 'port', sx: e.clientX, sy: e.clientY, ids: [], orig: {}, px: 0, py: 0, moved: false, edge: edge.key, end };
    held = true;
  }
  // The point of the border of a node nearest to a point of the world: the side, and how far along it (kept off the corners).
  function nearestOnBorder(n: Node, wx: number, wy: number): { side: Side; t: number } {
    let best = { side: 'right' as Side, t: 0.5 }, bd = Infinity;
    for (const s of SIDES) {
      const horiz = s === 'top' || s === 'bottom';
      const len = horiz ? W : n.h;
      const t = Math.min(0.9, Math.max(0.1, ((horiz ? wx - n.x : wy - n.y)) / len));
      const p = port(n, s, t);
      const d = Math.hypot(p.x - wx, p.y - wy);
      if (d < bd) { bd = d; best = { side: s, t }; }
    }
    return best;
  }
  // Picking a side in the tools resets the point along it to the default.
  const setSide = (key: string, end: 'from' | 'to', s: Side, t?: number) => {
    const cur = lines[key] ?? { from: 'right', to: 'left' };
    const off = end === 'from' ? 'fo' : 'tt';
    const { [off]: _old, ...rest } = cur as Record<string, unknown>;
    lines[key] = { ...rest, [end]: s, ...(t === undefined ? {} : { [off]: Math.round(t * 1000) / 1000 }) } as GraphLayout['lines'][string];
  };
  function move(e: PointerEvent) {
    if (!drag) return;
    const dx = e.clientX - drag.sx, dy = e.clientY - drag.sy;
    if (Math.abs(dx) + Math.abs(dy) > 3) drag.moved = true;
    if (!drag.moved) return;
    if (drag.kind === 'pan') {
      const l = limits();
      pan = { x: rubber(drag.px + dx, l.loX, l.hiX), y: rubber(drag.py + dy, l.loY, l.hiY) };
    } else if (drag.kind === 'port') {
      const edge = edges.find((x) => x.key === drag!.edge);
      const n = drag.end === 'from' ? edge?.from : edge?.to;
      const r = canvas?.getBoundingClientRect();
      if (edge && n && r) {
        const b = nearestOnBorder(n, (e.clientX - r.left - pan.x) / k, (e.clientY - r.top - pan.y) / k);
        setSide(edge.key, drag.end!, b.side, b.t);
      }
    } else {
      for (const id of drag.ids) pos[id] = { x: Math.max(0, drag.orig[id]!.x + dx / k), y: Math.max(0, drag.orig[id]!.y + dy / k) };
    }
  }
  function up() {
    if (!drag) return;
    const d = drag;
    drag = null;
    heldIds = [];
    held = false;
    if (d.kind === 'pan') {
      const l = limits();
      const x = Math.min(l.hiX, Math.max(l.loX, pan.x)), y = Math.min(l.hiY, Math.max(l.loY, pan.y));
      if (x !== pan.x || y !== pan.y) {
        settleFor();
        pan = { x, y };
      }
    } else if (d.moved) queueSave();
    else if (d.focusId) select(d.focusId);
  }
  let saveTimer: ReturnType<typeof setTimeout> | undefined;
  const layout = (): GraphLayout => ({ nodes: pos, lines });
  let saveFailed = false;
  const save = () => a.api.x.savePositions(layout()).catch(() => {
    if (!saveFailed) toast.error('Could not save the arrangement', { description: 'It stays on screen until you leave this page.' });
    saveFailed = true;
    setTimeout(() => (saveFailed = false), 60_000); // one notice a minute, not one per drag
  });
  let pendingSave = false;
  function queueSave() {
    if (layoutReadFailed) return; // the stored arrangement could not be read: saving would replace it with a part
    if (!loadedLayout) { pendingSave = true; return; } // what was saved before has not arrived yet: saving now would overwrite it with a part
    clearTimeout(saveTimer);
    saveTimer = setTimeout(() => { saveTimer = undefined; void save(); }, 400);
  }
  // Leaving the page within the delay must not lose the last change.
  onDestroy(() => {
    clearTimeout(revealTimer);
    if (saveTimer) {
      clearTimeout(saveTimer);
      void save();
    }
  });

  const zoom = (f: number) => (k = Math.min(1.5, Math.max(0.5, Math.round((k + f) * 100) / 100)));
  // First drawing: fit the width (the height is reached by panning). Fit view fits both.
  function fit(both = false) {
    const { cw, ch } = bounds();
    const kk = Math.min(1, (cw - 48) / world.w, both ? (ch - 48) / world.h : 1);
    k = Math.max(both ? 0.5 : 0.6, Math.round(kk * 100) / 100);
    pan = { x: 24, y: 24 };
  }
  function wheel(e: WheelEvent) {
    if (!e.ctrlKey) return;
    e.preventDefault();
    zoom(e.deltaY < 0 ? 0.1 : -0.1);
  }
  const resetLines = () => { lines = {}; queueSave(); };

  // Focusing a node by keyboard brings it into view (the canvas does not scroll by itself: it pans).
  function reveal(id: string) {
    const n = byId.get(id);
    if (!n) return;
    const { cw, ch } = bounds();
    const sx = pan.x + n.x * k, sy = pan.y + n.y * k, w = W * k, h = n.h * k, m = 16;
    let nx = pan.x, ny = pan.y;
    if (sx < m) nx += m - sx;
    else if (sx + w > cw - m) nx -= sx + w - (cw - m);
    if (sy < m) ny += m - sy;
    else if (sy + h > ch - m) ny -= sy + h - (ch - m);
    if (nx !== pan.x || ny !== pan.y) {
      settleFor();
      pan = { x: nx, y: ny };
    }
  }
  // The panes take their room over 200 ms: bring the phase into view once the canvas has its final width.
  let revealTimer: ReturnType<typeof setTimeout> | undefined;
  const revealSoon = (id: string) => { clearTimeout(revealTimer); revealTimer = setTimeout(() => reveal(id), 230); };
  const crowded = () => typeof innerWidth !== 'undefined' && innerWidth < 1200;
  const select = (id: string | null) => { selected = id; selTask = null; paneTab = 'summary'; if (id) { if (crowded()) toolsOpen = false; revealSoon(id); } };
  const pickTask = (n: Node, t: TaskSummary) => { selected = n.id; selTask = t.id; if (crowded()) toolsOpen = false; revealSoon(n.id); };
  // Closing the detail returns the focus to the phase it described.
  function closeDetail() {
    const id = selected;
    select(null);
    void tick().then(() => canvas?.querySelector<HTMLElement>(`[data-id="${id}"]`)?.focus());
  }
  // Opening or closing the tools changes the room too.
  $effect(() => {
    void toolsOpen;
    const id = selected;
    if (id) revealSoon(id);
  });
  const toggle = (id: string) => {
    const s = new Set(open);
    if (s.has(id)) s.delete(id);
    else s.add(id);
    open = s;
  };
  function nodeKey(e: KeyboardEvent, n: Node) {
    if (e.target !== e.currentTarget) return; // keys of the inner buttons are their own
    const step = e.shiftKey ? 32 : 8;
    const mv = { ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step] }[e.key];
    if (mv) {
      e.preventDefault();
      pos[n.id] = { x: Math.max(0, n.x + mv[0]!), y: Math.max(0, n.y + mv[1]!) };
      queueSave();
    } else if (e.key === 'Enter') {
      e.preventDefault();
      toggle(n.id);
    } else if (e.key === ' ') {
      e.preventDefault();
      select(n.id);
    } else if (e.key === 'Escape') select(null);
  }

  // ---- detail pane ----
  // While the pane slides shut it keeps showing what it showed, so its content does not vanish before it is gone.
  let shownSel = $state<string | null>(null);
  let shownTask = $state<string | null>(null);
  $effect(() => { if (selected) { shownSel = selected; shownTask = selTask; } });
  const sel = $derived(byId.get(selected ?? shownSel ?? ''));
  const task = $derived(sel && (selected ? selTask : shownTask) ? sel.tasks.find((t) => t.id === (selected ? selTask : shownTask)) : undefined);
  const detailOpen = $derived(!!selected && !!byId.get(selected));
  // The loaders read primitives only: a drag or an expand rebuilds the node objects, and that must never ask the service again.
  const selId = $derived(sel?.phase.id);
  const selAwaiting = $derived(sel?.phase.state === 'awaiting_phase_review');
  const review = resource(async () => (selId && selAwaiting ? a.api.phaseReview(selId) : null), ['approval.changed', 'phase.changed']);
  // What is on screen belongs to the selected phase, never to the one selected before it while the new answer is on its way.
  const rev = $derived(review.data && review.data.phase.id === selId ? review.data : null);
  const apr = resource(async () => {
    const id = review.data?.approval_id;
    return id ? a.api.approval(id) : null;
  }, ['approval.changed']);
  const aprNow = $derived(apr.data && rev && apr.data.id === rev.approval_id ? apr.data : null);
  let deciding = $state<null | { id: string; mode: 'approve' | 'reject' }>(null);
  let releasing = $state<null | { id: string; title: string; code: string }>(null);
  const waits = $derived(new Map((waiting.data ?? []).map((w) => [w.execution_id, w.since])));
  const isWaiting = (t: TaskSummary) => !!t.execution_id && waits.has(t.execution_id);
  const taskTone = (t: TaskSummary) => (isWaiting(t) ? 'waiting' : t.state);
  const taskWord = (t: TaskSummary) => (isWaiting(t) ? 'Waiting' : t.state === 'in_progress' ? 'Running' : t.state === 'done' ? 'Done' : label(t.state));
  const taskIcon = (t: TaskSummary) => (isWaiting(t) ? 'prompt' : t.state === 'done' ? 'check' : t.state === 'in_progress' ? 'run' : t.state === 'blocked' ? 'warn' : 'neu');
  const summaryOf = (n: Node) => {
    const t = n.tasks;
    const done = t.filter((x) => x.state === 'done').length;
    if (n.phase.state === 'completed') return 'Completed and approved.';
    if (n.phase.state === 'awaiting_phase_review') return t.length ? `${t.length} tasks done. Waiting for your review.` : 'Waiting for your review.';
    if (n.phase.state === 'in_progress') return t.length ? `${t.length} tasks. ${done} done, ${t.filter((x) => x.state === 'in_progress').length} running, ${t.filter((x) => x.state === 'blocked').length} blocked.` : 'Running.';
    if (n.phase.state === 'planned') return 'Starts after the previous phase is approved.';
    return label(n.phase.state) + '.';
  };
  const recent = $derived((audit.data?.items ?? []).filter((r) => sel && ` ${r.subject} `.includes(` ${sel.d.name} `)).slice(0, 5));

  const chipToggle = (id: string) => {
    const s = new Set(off);
    if (s.has(id)) s.delete(id);
    else s.add(id);
    off = s;
    if (selected && !byId.has(selected)) select(null);
  };
  const reviewLink = (n: Node) => `/projects/${n.d.id}/${n.phase.state === 'awaiting_phase_review' ? 'review' : ''}`;
  const lineName = (e: Edge) => `${e.from!.phase.code} to ${e.to!.phase.code} in ${e.from!.d.name}`;
</script>

<svelte:window onpointermove={move} onpointerup={up} onpointercancel={up} />

<h1 class="sr-only">Graph</h1>
<div class="gfull">
  <View res={details} label="the graph">
    {#snippet children()}
      <div class="gmain">
        {#if view === 'graph'}
          <div class="canvas" class:held bind:this={canvas} role="application" aria-label="Project graph. Drag the background to pan. With a phase focused, the arrow keys move it, Space opens its detail and Enter expands it. Line sides and frames are set in the tools."
            onpointerdown={panDown} onwheel={wheel}>
            <div class="world" class:settle style="width:{world.w}px;height:{world.h}px;transform:translate({pan.x}px,{pan.y}px) scale({k})">
              {#if showFrames}
                {#each groups as g (g.d.id)}
                  <div class="group" style="left:{g.x}px;top:{g.y}px;width:{g.w}px;height:{g.h}px">
                    <div class="gtag" data-gtag role="presentation" onpointerdown={(e) => nodeDown(e, g.ids)}><b>{g.d.name}</b>{#if g.d.pending_approval_items.some((x) => x.category === 'workflow')}<a href={href(`/projects/${g.d.id}/plan`)} aria-label="Proposal ready, open the Plan of {g.d.name}" onpointerdown={(e) => e.stopPropagation()}><Badge state="awaiting_approval" text="Proposal ready" /></a>{/if}</div>
                  </div>
                {/each}
              {/if}
              <svg class="edges" width={world.w} height={world.h} aria-hidden="true">
                {#each edges as e (e.key)}
                  <path d={e.path} class:fork={e.fork} fill="none" />
                  <circle class="port" cx={e.x1} cy={e.y1} r="4.5" class:fork={e.fork} />
                  <circle class="port" cx={e.x2} cy={e.y2} r="4.5" class:fork={e.fork} />
                  <!-- The end circles are a pointer shortcut; the same choice is available by keyboard in the tools pane (Lines). -->
                  {#if !e.fork}
                    <circle class="hit" role="presentation" data-port cx={e.x1} cy={e.y1} r="12" onpointerdown={(ev) => portDown(ev, e, 'from')} />
                    <circle class="hit" role="presentation" data-port cx={e.x2} cy={e.y2} r="12" onpointerdown={(ev) => portDown(ev, e, 'to')} />
                  {/if}
                {/each}
              </svg>
              {#each edges as e (e.key + 'l')}
                <span class="elabel" style="left:{e.mx}px;top:{e.my}px"><Badge state={e.fork ? 'fork' : e.text === 'Approved' ? 'approved' : 'draft'} text={e.text} /></span>
              {/each}
              {#each nodes as n (n.id)}
                <!-- A phase node is a composite widget (a draggable card with its own buttons), so it is focusable with arrow, Enter, Space and Esc keys. -->
                <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
                <div class="node" data-id={n.id} bind:offsetHeight={heights[n.id]} class:sel={selected === n.id} class:held={heldIds.includes(n.id)} data-node tabindex="0" role="group" aria-current={selected === n.id ? 'true' : undefined} aria-label="{n.phase.code} {n.phase.title}, {label(n.phase.state)}"
                  style="left:{n.x}px;top:{n.y}px;width:{W}px;min-height:{BASE_H}px" onpointerdown={(e) => nodeDown(e, [n.id], n.id)} onfocus={() => { if (!drag) reveal(n.id); }} onkeydown={(e) => nodeKey(e, n)}>
                  <div class="nt"><b>{n.phase.code} · {n.phase.title}</b>
                    <button type="button" class="chev" aria-expanded={n.open} aria-label="{n.open ? 'Collapse' : 'Expand'} {n.phase.title}" disabled={!n.hasTasks} onclick={() => toggle(n.id)}>
                      <span class="swap"><span data-on={!n.open}><Icon name="chev" size={14} /></span><span data-on={n.open}><Icon name="chevu" size={14} /></span></span>
                    </button>
                  </div>
                  <div class="nb"><Badge state={n.phase.state} /></div>
                  <p class="ns">{summaryOf(n)}</p>
                  <div class="expand" data-open={n.open && n.hasTasks} inert={!(n.open && n.hasTasks)}>
                    <ul class="tasks">
                      {#each n.tasks as t (t.id)}
                        <li><button type="button" class="trow" class:on={selTask === t.id} aria-label="{t.code} {t.title}, {taskWord(t)}" onclick={() => pickTask(n, t)}><Icon name={taskIcon(t)} size={12} /><span class="min-w-0 flex-1 truncate" title={t.title}>{t.title}</span><span class="tw" data-tone={taskTone(t)}>{taskWord(t)}</span></button></li>
                      {/each}
                    </ul>
                  </div>
                </div>
              {/each}
            </div>
          </div>
        {:else}
          <div class="gtable">
            <div class="panel">
              <div class="grid-head grow5" aria-hidden="true"><span>Phase</span><span>Status</span><span class="num">Tasks</span><span class="num">Checks</span><span></span></div>
              <ul class="items" aria-label="Phases">
                {#each nodes as n (n.id)}
                  <li class="grow5">
                    <div class="min-w-0"><a class="tlink" href={href(reviewLink(n))}>{n.phase.code} · {n.phase.title}</a><div class="mono muted small truncate">{n.d.name}{forks.data?.some((f) => f.project_id === n.d.id) ? ' · fork' : ''}</div></div>
                    <div><Badge state={n.phase.state} /></div>
                    <span class="num">{n.tasks.length || '—'}</span>
                    <span class="num">{n.tasks.length ? `${n.tasks.filter((x) => x.state === 'done').length} of ${n.tasks.length}` : '—'}</span>
                    <div class="acts">{#if n.phase.state === 'awaiting_phase_review'}<IconBtn icon="lock" lock kind="pri" label="Approve" context={n.phase.title} href={href(reviewLink(n))} />{/if}</div>
                  </li>
                {/each}
              </ul>
            </div>
          </div>
        {/if}
        {#if saved.data && nodes.length === 0 && view === 'graph'}
          <p class="gempty">{details.data?.length ? 'No project is drawn. Switch one on under Projects in the tools.' : 'No project has a workflow yet. Register a project and approve a plan to see it here.'}</p>
        {/if}
      </div>

      <div class="gdetail" data-open={detailOpen} inert={!detailOpen}>
      {#if sel}
        <aside class="gpane" aria-label={task ? 'Task detail' : 'Phase detail'}>
          {#if task}
            <div class="flex items-start gap-2">
              <h2 class="m-0 min-w-0 flex-1 text-lg">{#if task.execution_id}<a class="tlink" href={href('/executions/' + task.execution_id)}>{task.title}</a>{:else}{task.title}{/if}</h2>
              <button type="button" class="x" aria-label="Close detail" onclick={closeDetail}><Icon name="x" size={16} /></button>
            </div>
            <div class="flex flex-wrap items-center gap-2"><Badge state={isWaiting(task) ? 'waiting_for_instruction' : task.state} /><span class="muted">{task.code} · {sel.phase.code} {sel.phase.title}</span></div>
            <dl class="kv2">
              <dt>Project</dt><dd>{sel.d.name}</dd>
              <dt>Mode</dt><dd>{task.mode === 'read_only' ? 'Read only' : 'Code change'}</dd>
              <dt>Depends on</dt><dd class="mono">{task.depends_on.length ? task.depends_on.join(', ') : 'nothing'}</dd>
              {#if task.execution_id}<dt>Execution</dt><dd class="mono">{task.execution_id}</dd>{/if}
              {#if task.state_detail}<dt>State</dt><dd>{task.state_detail}</dd>{/if}
            </dl>
            {#if isWaiting(task)}
              <div class="callout waiting">
                <b class="inline-flex items-center gap-2"><Icon name="prompt" size={14} />Waiting for instruction</b>
                <p class="m-0">This task has waited {waitedFor(waits.get(task.execution_id!)!)}. Claude Code asks for you in its own session.</p>
                <div class="flex justify-end"><IconBtn icon="release" label="Release" context={task.title} onclick={() => (releasing = { id: task.execution_id!, title: task.title, code: task.code })} /></div>
              </div>
            {/if}
            <button type="button" class="linkbtn self-start" onclick={() => (selTask = null)}>Back to {sel.phase.code} {sel.phase.title}</button>
          {:else}
            <div class="flex items-start gap-2">
              <h2 class="m-0 min-w-0 flex-1 text-lg"><a class="tlink" href={href(reviewLink(sel))}>{sel.phase.code} · {sel.phase.title}</a></h2>
              <button type="button" class="x" aria-label="Close detail" onclick={closeDetail}><Icon name="x" size={16} /></button>
            </div>
            <div class="flex flex-wrap items-center gap-2"><Badge state={sel.phase.state} /><span class="muted">{sel.d.name}</span></div>
            <div class="seg tabs" role="group" aria-label="Detail">
              <button type="button" aria-pressed={paneTab === 'summary'} onclick={() => (paneTab = 'summary')}>Summary</button>
              <button type="button" aria-pressed={paneTab === 'activity'} onclick={() => (paneTab = 'activity')}>Activity</button>
            </div>
            {#if paneTab === 'summary'}
              <p class="m-0 max-w-[65ch]">{visible(rev?.summary ?? summaryOf(sel))}</p>
              {#if rev}
                <dl class="kv2">
                  <dt>Requested by</dt><dd>{aprNow?.requested_by ?? 'Waves'}</dd>
                  <dt>Validators</dt><dd>{rev.validators.filter((v) => v.state === 'passed').length} of {rev.validators.length} passed</dd>
                  <dt>Changes</dt><dd class="mono">{rev.diff.files} files, +{rev.diff.added} −{rev.diff.removed}</dd>
                </dl>
                <div class="secnote"><Icon name="lock" size={14} /><span>Approving asks for your passphrase. An agent cannot approve.</span></div>
                {#if aprNow?.state === 'pending'}
                  <div class="flex gap-2"><Btn kind="pri" lock disabled={broken} onclick={() => (deciding = { id: aprNow.id, mode: 'approve' })}>Approve</Btn><Btn kind="dng" disabled={broken} onclick={() => (deciding = { id: aprNow.id, mode: 'reject' })}>Reject</Btn></div>
                {/if}
              {:else}
                {#each sel.tasks.filter(isWaiting) as t (t.id)}
                  <div class="callout waiting">
                    <b class="inline-flex items-center gap-2"><Icon name="prompt" size={14} />Waiting for instruction</b>
                    <p class="m-0">Task {t.code} has waited {waitedFor(waits.get(t.execution_id!)!)}. Claude Code asks for you in its own session.</p>
                    <div class="flex justify-end"><IconBtn icon="release" label="Release" context={t.title} onclick={() => (releasing = { id: t.execution_id!, title: t.title, code: t.code })} /></div>
                  </div>
                {/each}
              {/if}
            {:else}
              <ul class="plain">
                {#each recent as r (r.seq)}<li><span class="mono">{r.event}</span> {r.subject}</li>{:else}<li class="muted">No recent activity for this project.</li>{/each}
              </ul>
            {/if}
          {/if}
        </aside>
      {/if}
      </div>

      <div class="gside" data-open={toolsOpen}>
        <button type="button" class="gtoggle" aria-expanded={toolsOpen} aria-controls="gtools" aria-label="Tools" onclick={() => (toolsOpen = !toolsOpen)}><Icon name="panelr" size={18} /></button>
        <aside class="gtools" id="gtools" aria-label="Graph tools" inert={!toolsOpen}>
          <h2 class="gcap">Tools</h2>
          <section>
            <h2>View</h2>
            <div class="seg" role="group" aria-label="View">
              <button type="button" aria-pressed={view === 'graph'} onclick={() => (view = 'graph')}>Graph</button>
              <button type="button" aria-pressed={view === 'table'} onclick={() => (view = 'table')}>Table</button>
            </div>
          </section>
          <section>
            <h2>Projects</h2>
            <div class="flex flex-wrap gap-2" role="group" aria-label="Projects drawn">
              {#each details.data ?? [] as d (d.id)}
                <button type="button" class="gchip" aria-pressed={!off.has(d.id)} onclick={() => chipToggle(d.id)}>
                  <Icon name={off.has(d.id) ? 'neu' : forks.data?.some((f) => f.project_id === d.id) ? 'fork' : 'check'} size={12} />{d.name}
                </button>
              {/each}
            </div>
            <label class="switch"><input type="checkbox" bind:checked={showFrames} />Show project frames</label>
            {#if forksFailed}<p class="muted m-0">Fork lines could not be read, so they are not drawn.</p>{/if}
            {#if waiting.error}<p class="muted m-0">Waiting harnesses could not be read, so they are not marked.</p>{/if}
            {#if layoutReadFailed}<p class="muted m-0">The saved arrangement could not be read. Changes are not saved until you reload this page.</p>{/if}
          </section>
          {#if view === 'graph'}
            <section>
              <h2>Zoom</h2>
              <div class="zoombar" role="group" aria-label="Zoom">
                <button type="button" aria-label="Zoom out" onclick={() => zoom(-0.1)}>−</button>
                <span class="mono" aria-live="polite">{Math.round(k * 100)}%</span>
                <button type="button" aria-label="Zoom in" onclick={() => zoom(0.1)}>+</button>
                <button type="button" class="fit" onclick={() => fit(true)}>Fit view</button>
              </div>
            </section>
            <section>
              <h2>Lines</h2>
              <p class="muted m-0">Drag the end circle of a line round its phase, or pick the sides here.</p>
              <details class="disc2"><summary>Line sides ({edges.filter((e) => !e.fork).length})</summary>
              <ul class="plain lines">
                {#each edges.filter((e) => !e.fork) as e (e.key)}
                  <li>
                    <span class="lname">{e.from!.phase.code} → {e.to!.phase.code} <span class="muted">{e.from!.d.name}</span></span>
                    <label class="sr-only" for="ls-{e.key}">Start side, line {lineName(e)}</label>
                    <select id="ls-{e.key}" class="field" value={e.fs} onchange={(ev) => { setSide(e.key, 'from', ev.currentTarget.value as Side); queueSave(); }}>{#each SIDES as s}<option value={s}>{SIDE_LABEL[s]}</option>{/each}</select>
                    <label class="sr-only" for="le-{e.key}">End side, line {lineName(e)}</label>
                    <select id="le-{e.key}" class="field" value={e.ts} onchange={(ev) => { setSide(e.key, 'to', ev.currentTarget.value as Side); queueSave(); }}>{#each SIDES as s}<option value={s}>{SIDE_LABEL[s]}</option>{/each}</select>
                  </li>
                {/each}
              </ul>
              </details>
              <button type="button" class="linkbtn self-start" onclick={resetLines}>Reset lines</button>
            </section>
          {/if}
          <section>
            <h2>Keys</h2>
            <p class="muted m-0">Tab to a phase. Space opens its detail, Enter expands it, arrow keys move it by 8 px (Shift 32), Esc closes the detail.</p>
          </section>
        </aside>
      </div>
    {/snippet}
  </View>
</div>

{#if deciding}
  <DecideFlow approvalId={deciding.id} mode={deciding.mode} oncancel={() => (deciding = null)} ondone={() => { deciding = null; void review.reload(); void details.reload(); }} />
{/if}
{#if releasing}
  <ReleaseLease target={releasing} oncancel={() => (releasing = null)} ondone={() => { releasing = null; void waiting.reload(); void details.reload(); }} />
{/if}
