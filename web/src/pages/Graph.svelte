<script lang="ts">
  // The Graph, full screen: every project as a group of phase nodes joined by lines. Nodes and frames are HTML and CSS, the lines one SVG
  // layer (no graph package). View and project chips sit above the canvas, zoom and frames in a capsule below it, line tools in a pane
  // on the right; a phase or task detail opens as an inspector. A line can leave and enter any side of its nodes (drag its end circle round the node, or pick the sides in the tools).
  // Dragging only moves saved positions, never workflow data.
  import { onDestroy, tick } from 'svelte';
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { shellData } from '../lib/shell';
  import { href } from '../lib/router.svelte';
  import { waitingOf, waitedFor } from '../lib/waiting.svelte';
  import { taskState } from '../lib/flow';
  import { apart, dodge, facing, makeRoom, overlaps, slide, type Rect } from '../lib/graphroom';
  import { cubicInOut, cubicOut } from 'svelte/easing';
  import { glide } from '../lib/glide.svelte';
  import { ago, plural, capital } from '../lib/format';
  import StatusDot from '../lib/StatusDot.svelte';
  import ReleaseLease from '../lib/ReleaseLease.svelte';
  import { visible } from '../lib/text';
  import { label } from '../lib/status';
  import type { PhaseRef, ProjectDetail, TaskSummary } from '../api/models';
  import { emptyLayout, type GraphLayout, type Side } from '../api/ext';
  import Badge from '../lib/Badge.svelte';
  import ConnState from '../lib/ConnState.svelte';
  import Btn from '../lib/Btn.svelte';
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
  const saved = resource(async (): Promise<GraphLayout & { read: boolean }> => {
    try {
      const layout = await a.api.x.positions();
      layoutReadFailed = false; // only now: until then a save would write over what could not be read
      return { ...layout, read: true };
    } catch {
      layoutReadFailed = true;
      return { ...emptyLayout(), read: false };
    }
  });
  const execs = resource(() => a.api.executions(), ['execution.changed']);
  const audit = resource(() => a.api.audit(), ['approval.changed', 'phase.changed']);

  // ---- geometry (px in the canvas world) ----
  const W = 144, W_OPEN = 264, GAP = 96, TAG = 56, PORT_Y = 36, BASE_H = 104;
  // A closed card is the base and the edge of its folded task list; an open one adds the list instead.
  const CLOSED_H = BASE_H + 17;
  // The room kept around an opened phase and its frame; further apart than `GAP`/`FRAME_GAP` is never pushed apart further.
  const NEAR = 24, FRAME_GAP = 80;
  // No two frames come closer than this, which leaves room for the fork line and its label.
  const CLEAR = 48;
  // Two phases come no closer than this; the one approached steps aside, over `DODGE` ms.
  const PHASE_CLEAR = 24, DODGE = 180;
  // A task row (36, or 44 on a touch screen) with its 2 px gap, and what the list adds around its rows.
  const ROW_H = typeof matchMedia !== 'undefined' && matchMedia('(pointer: coarse)').matches ? 46 : 38, LIST_H = 15;
  // The opening phase grows more slowly than its frame widens and its neighbours step aside, so it never leaves the frame or
  // runs into a neighbour; closing is the same the other way round.
  const SLOW = 460, FAST = 220;
  /** `ox`, `oy`: how far its whole frame was pushed aside. `dodged`: it stepped aside for another phase. */
  type Node = { id: string; d: ProjectDetail; phase: PhaseRef; i: number; x: number; y: number; w: number; h: number; ox: number; oy: number; dodged: boolean; tasks: TaskSummary[]; open: boolean; hasTasks: boolean };
  const SIDES: Side[] = ['top', 'right', 'bottom', 'left'];
  const SIDE_LABEL: Record<Side, string> = { top: 'Top', right: 'Right', bottom: 'Bottom', left: 'Left' };

  const narrow = typeof matchMedia !== 'undefined' && matchMedia('(max-width: 860px)').matches;
  let view = $state<'graph' | 'table'>(narrow ? 'table' : 'graph');
  let toolsOpen = $state(false);
  let showFrames = $state(true);
  let off = $state(new Set<string>()); // projects switched off with the chips
  let open = $state(new Set<string>());
  let seeded = false;
  let pos = $state<GraphLayout['nodes']>({});
  let lines = $state<GraphLayout['lines']>({});
  let loadedLayout = false;
  let selected = $state<string | null>(null);
  let selTask = $state<string | null>(null);
  let k = $state(1);
  let pan = $state({ x: 0, y: 0 });
  let settle = $state(false); // a rubber band or a reveal is moving the world: the one moment a transition is allowed on it
  let held = $state(false);
  let heights = $state<Record<string, number>>({}); // the real height of each node card, measured by the browser
  let motion = $state<null | { id: string; opening: boolean }>(null); // set while a phase opens or closes
  let motionTimer: ReturnType<typeof setTimeout> | undefined;
  // Since the last open or close: phases moved by hand, which nothing opened pushes about, and for each project a hand has
  // been in, how far its frame stays pushed (frozen then, so that what is held goes exactly where the hand puts it).
  let placed = $state(new Set<string>());
  let kept = $state.raw<Record<string, { x: number; y: number }>>({});
  // What a hand holds right now: those phases stay put, the others make way, but not into the frames in `walls`.
  // `from`: where the phases of that project were when the hand took hold.
  let hold = $state.raw<{ ids: ReadonlySet<string>; project: string; walls: Rect[]; from: Array<Rect & { id: string }> } | null>(null);
  let settleTimer: ReturnType<typeof setTimeout> | undefined;
  const settleFor = () => {
    settle = true;
    clearTimeout(settleTimer);
    settleTimer = setTimeout(() => (settle = false), 260);
  };

  const sideOk = (s: unknown): s is Side => SIDES.includes(s as Side);
  $effect(() => {
    if (!loadedLayout && saved.data?.read) {
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

  const tasksOf = (d: ProjectDetail, p: PhaseRef): TaskSummary[] => d.phase_tasks.filter((t) => t.phase_id === p.id);

  // A fork is drawn right under the project it was forked from, so its line never crosses another group.
  const shown = $derived.by(() => {
    const vis = (details.data ?? []).filter((d) => !off.has(d.id));
    const forkOf = new Map((forks.data ?? []).map((f) => [f.project_id, f.fork_of]));
    const roots = vis.filter((d) => !forkOf.has(d.id) || !vis.some((x) => x.id === forkOf.get(d.id)));
    return roots.flatMap((r) => [r, ...vis.filter((d) => forkOf.get(d.id) === r.id)]);
  });
  // Where each phase is with nothing pushed: the saved place, or the next free one in its project's row.
  const rows = $derived.by(() => {
    if (!saved.data) return []; // nothing is drawn before the saved arrangement is known, so the first fit never moves a drawn phase
    let gy = 64; // room above the first frame for the title of the page
    const drawnRows = shown.map((d) => {
      const base = gy + TAG + 16;
      let nx = 48; // the next free place in the row, for a phase the human has not moved
      const list = d.phases.map((p, i) => {
        const tasks = tasksOf(d, p);
        const isOpen = open.has(p.id) && tasks.length > 0;
        const at = pos[p.id];
        const x = at?.x ?? nx, y = at?.y ?? base;
        nx += W + GAP;
        const est = isOpen ? BASE_H + tasks.length * ROW_H + LIST_H : CLOSED_H;
        // The estimate, so nothing moves while a list unfolds; the measured height only where a card at rest is taller than it.
        const h = motion ? est : Math.max(est, heights[p.id] ?? 0);
        return { p, i, d, tasks, x, y, w: isOpen ? W_OPEN : W, h, open: open.has(p.id) };
      });
      gy = base + CLOSED_H + 96;
      return list;
    }).filter((r) => r.length);
    // No two frames lie on each other, whatever the saved places say.
    const down = apart(drawnRows.map((r) => ({ id: r[0]!.d.id, ...frameOf(r.map(rest)) })), CLEAR);
    return drawnRows.map((r) => r.map((n) => ({ ...n, y: n.y + down.get(n.d.id)!, down: down.get(n.d.id)! })));
  });
  const frameOf = (ns: Array<{ x: number; y: number; w: number; h: number }>) => {
    const x = Math.min(...ns.map((n) => n.x)) - 24, y = Math.min(...ns.map((n) => n.y)) - TAG - 8;
    return { x, y, w: Math.max(...ns.map((n) => n.x + n.w)) - x + 24, h: Math.max(...ns.map((n) => n.y + n.h)) - y + 24 };
  };
  // A phase's room with its list folded away: what it is compared with to tell what opening takes.
  const rest = (n: { x: number; y: number; h: number; open: boolean; tasks: TaskSummary[] }) => ({ x: n.x, y: n.y, w: W, h: n.open && n.tasks.length ? CLOSED_H : n.h });
  // Opened phases push what is in their way, inside their project; a project that grew pushes the projects around it.
  const nodes = $derived.by(() => {
    const projects = rows.map((r) => {
      const at = makeRoom(r.map((n) => ({ id: n.p.id, was: rest(n), w: n.w, h: n.h, fixed: placed.has(n.p.id) })), NEAR, GAP);
      const roomed = r.map((n) => ({ ...n, ...at.get(n.p.id)! }));
      // No two phases lie on each other, whatever was saved or is being dragged.
      const held = hold?.project === r[0]!.d.id ? hold : null;
      const clearOfWalls = (c: Rect) => !held?.walls.some((w) => overlaps(frameOf([c]), w, CLEAR));
      const free = dodge(roomed.map((n) => ({ id: n.p.id, x: n.x, y: n.y, w: n.w, h: n.h })), held?.ids ?? new Set<string>(), PHASE_CLEAR, clearOfWalls);
      const list = roomed.map((n) => { const to = free.get(n.p.id)!; return { ...n, ...to, dodged: to.x !== n.x || to.y !== n.y }; });
      // The frame as it would be with nothing opened: what the other frames compare with.
      const closed = r.map((n, i) => { const c = rest(n); return { ...c, x: c.x + list[i]!.x - roomed[i]!.x, y: c.y + list[i]!.y - roomed[i]!.y }; });
      return { id: r[0]!.d.id, rest: frameOf(closed), list };
    });
    const frames = makeRoom(projects.map((g) => {
      const at = kept[g.id];
      return { id: g.id, was: at ? { ...g.rest, x: g.rest.x + at.x, y: g.rest.y + at.y } : g.rest, w: frameOf(g.list).w, h: frameOf(g.list).h, fixed: !!at };
    }), CLEAR, FRAME_GAP);
    const moved = projects.map((g) => {
      const ox = frames.get(g.id)!.x - g.rest.x, oy = frames.get(g.id)!.y - g.rest.y; // for every phase of the frame alike
      return { id: g.id, list: g.list.map((n): Node => ({ id: n.p.id, d: n.d, phase: n.p, i: n.i, x: n.x + ox, y: n.y + oy, w: n.w, h: n.h, ox, oy, dodged: n.dodged, tasks: n.tasks, open: n.open, hasTasks: n.tasks.length > 0 })) };
    });
    const down = apart(moved.map((g) => ({ id: g.id, ...frameOf(g.list) })), CLEAR); // whatever the pushing left
    return moved.flatMap((g) => g.list.map((n) => ({ ...n, y: n.y + down.get(g.id)!, oy: n.oy + down.get(g.id)! })));
  });
  const byId = $derived(new Map(nodes.map((n) => [n.id, n])));
  const groups = $derived(
    shown.flatMap((d) => {
      const ns = nodes.filter((n) => n.d.id === d.id);
      if (!ns.length) return []; // before the saved arrangement is known there is nothing to frame
      return [{ d, ids: ns.map((n) => n.id), ...frameOf(ns) }];
    }),
  );
  // What is drawn on the way from one arrangement to the next.
  const still = typeof matchMedia === 'undefined' ? null : matchMedia('(prefers-reduced-motion: reduce)');
  const gl = glide(
    () => Object.fromEntries([...nodes.map((n) => ['n:' + n.id, { x: n.x, y: n.y, w: n.w, h: n.h }]), ...groups.map((g) => ['f:' + g.d.id, { x: g.x, y: g.y, w: g.w, h: g.h }])]),
    (key) => {
      if (still?.matches) return { ms: 0, ease: cubicInOut };
      if (motion) return { ms: (key === 'n:' + motion.id) === motion.opening ? SLOW : FAST, ease: cubicInOut };
      // Under a hand: what is held and its own frame follow at once, what makes way for it glides.
      const mine = !hold || (key.startsWith('n:') ? hold.ids.has(key.slice(2)) : key.slice(2) === hold.project);
      return { ms: mine ? 0 : DODGE, ease: cubicOut };
    },
  );
  const drawn = $derived(nodes.map((n) => ({ ...n, ...gl.current['n:' + n.id] })));
  const drawnById = $derived(new Map(drawn.map((n) => [n.id, n])));
  const frames = $derived(groups.map((g) => ({ ...g, ...gl.current['f:' + g.d.id] })));
  const gById = $derived(new Map(frames.map((g) => [g.d.id, g])));

  // A point on the middle of a side of a node, and the direction a line leaves it in.
  // t (0 to 1) is where along the side the line meets it; without it the default point is used.
  function port(n: { x: number; y: number; w: number; h: number }, s: Side, t?: number) {
    const y = t === undefined ? n.y + PORT_Y : n.y + t * n.h, x = n.x + (t === undefined ? n.w / 2 : t * n.w);
    return s === 'right' ? { x: n.x + n.w, y, vx: 1, vy: 0 }
      : s === 'left' ? { x: n.x, y, vx: -1, vy: 0 }
      : s === 'top' ? { x, y: n.y, vx: 0, vy: -1 }
      : { x, y: n.y + n.h, vx: 0, vy: 1 };
  }
  // The colour of a line says how far its target is; a phase that has not started is reached by a dashed one.
  const edgeTone = (state: string) => (state === 'completed' ? 'ok' : state === 'planned' ? 'ahead' : state === 'awaiting_phase_review' ? 'review' : state === 'revision_required' ? 'warn' : 'active');
  type Edge = { key: string; path: string; mx: number; my: number; text: string; tone: string; fork: boolean; x1: number; y1: number; x2: number; y2: number; from?: Node; to?: Node; fs: Side; ts: Side };
  function curve(p1: ReturnType<typeof port>, p2: ReturnType<typeof port>, min = 36) {
    // Each end leaves its side for half of the distance along its own axis, and at least `min`, so a line that has to go
    // round can. Where the other end lies straight ahead and near, it takes less, or the line would curl back on itself.
    const reach = (a: typeof p1, b: typeof p1) => {
      const ahead = (b.x - a.x) * a.vx + (b.y - a.y) * a.vy;
      return Math.max(ahead > 0 ? Math.min(min, Math.max(4, ahead / 2)) : min, Math.abs(a.vx ? b.x - a.x : b.y - a.y) * 0.5);
    };
    const d1 = reach(p1, p2), d2 = reach(p2, p1);
    const c1 = { x: p1.x + p1.vx * d1, y: p1.y + p1.vy * d1 }, c2 = { x: p2.x + p2.vx * d2, y: p2.y + p2.vy * d2 };
    return { path: `M${p1.x} ${p1.y} C${c1.x} ${c1.y} ${c2.x} ${c2.y} ${p2.x} ${p2.y}`, mx: (p1.x + 3 * c1.x + 3 * c2.x + p2.x) / 8, my: (p1.y + 3 * c1.y + 3 * c2.y + p2.y) / 8 };
  }
  const edges = $derived.by(() => {
    const out: Edge[] = [];
    for (const g of frames) {
      const ns = g.ids.map((id) => drawnById.get(id)!).sort((p, q) => p.i - q.i);
      for (let j = 1; j < ns.length; j++) {
        const p = ns[j - 1]!, q = ns[j]!;
        const key = `${p.id}>${q.id}`;
        // Without a side picked by hand a line joins the sides that face each other where the phases are going.
        const auto = facing(byId.get(p.id)!, byId.get(q.id)!);
        const fs = lines[key]?.from ?? auto.from, ts = lines[key]?.to ?? auto.to;
        const a1 = port(p, fs, lines[key]?.fo), a2 = port(q, ts, lines[key]?.tt);
        out.push({ key, ...curve(a1, a2), text: '', tone: edgeTone(q.phase.state), fork: false, x1: a1.x, y1: a1.y, x2: a2.x, y2: a2.y, from: p, to: q, fs, ts });
      }
    }
    for (const f of forks.data ?? []) {
      const from = gById.get(f.fork_of), to = gById.get(f.project_id);
      if (!from || !to) continue;
      const side = facing(groups.find((g) => g.d.id === f.fork_of)!, groups.find((g) => g.d.id === f.project_id)!);
      const a1 = port(from, side.from, 0.5), a2 = port(to, side.to, 0.5);
      const across = side.from === 'left' || side.from === 'right';
      // Side by side and close, the long label would lie on the phases of both projects.
      const text = across && Math.abs(a2.x - a1.x) < 180 ? 'fork' : `fork of ${from.d.name}`;
      out.push({ key: 'fork' + f.project_id, ...curve(a1, a2, across ? 24 : 60), text, tone: 'fork', fork: true, x1: a1.x, y1: a1.y, x2: a2.x, y2: a2.y, fs: side.from, ts: side.to });
    }
    return out;
  });
  const world = $derived({ w: Math.max(960, ...nodes.map((n) => n.x + n.w + 80)), h: Math.max(700, ...nodes.map((n) => n.y + n.h + 80)) });

  // ---- canvas: pan, zoom, drag ----
  let canvas = $state<HTMLElement | null>(null);
  type Drag = { kind: 'pan' | 'nodes' | 'port'; sx: number; sy: number; ids: string[]; orig: Record<string, { x: number; y: number }>; px: number; py: number; moved: boolean; focusId?: string; edge?: string; end?: 'from' | 'to'; hand?: Hand };
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
  /** `focusId`: one phase is dragged by its card; without it a whole frame is. */
  function nodeDown(e: PointerEvent, ids: string[], focusId?: string) {
    if (e.button !== 0 || (e.target as Element).closest('button,a')) return;
    e.stopPropagation();
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    heldIds = ids;
    drag = { kind: 'nodes', sx: e.clientX, sy: e.clientY, ids, orig: {}, px: 0, py: 0, moved: false, focusId, hand: take(ids, !focusId) };
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
      const len = horiz ? n.w : n.h;
      const t = Math.min(0.9, Math.max(0.1, ((horiz ? wx - n.x : wy - n.y)) / len));
      const p = port(n, s, t);
      const d = Math.hypot(p.x - wx, p.y - wy);
      if (d < bd) { bd = d; best = { side: s, t }; }
    }
    return best;
  }
  // Picking a side in the tools resets the point along it to the default.
  const setSide = (key: string, end: 'from' | 'to', s: Side, t?: number) => {
    const e = edges.find((x) => x.key === key);
    const cur = lines[key] ?? { from: e?.fs ?? 'right', to: e?.ts ?? 'left' };
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
      shift(drag.hand!, { x: dx / k, y: dy / k });
    }
  }
  // Phases picked up by the pointer or the keyboard: where each starts, the frames around them, and how far they have come.
  type Hand = { saved?: boolean; ids: string[]; whole: boolean; from: Array<Rect & { id: string }>; mine: Array<Rect & { id: string }>; walls: Rect[]; at: { x: number; y: number }; offset: { x: number; y: number }; project: string };
  function take(ids: string[], whole: boolean): Hand {
    const project = byId.get(ids[0]!)!.d.id;
    // A single phase starts from where it is drawn. A whole frame starts without the places its phases were pushed to inside
    // it, but with the place the frame itself was pushed to, so it does not jump when picked up.
    const under = new Map(rows.flat().map((n) => [n.p.id, n]));
    const start = (id: string) => { const n = byId.get(id)!, u = under.get(id)!; return whole ? { id, x: u.x + n.ox, y: u.y + n.oy, w: n.w, h: n.h } : { id, x: n.x, y: n.y, w: n.w, h: n.h }; };
    // A whole frame stops at every other frame. A single phase stretches its frame downward, and the frames below step down;
    // it stops at the frames beside and above.
    const own = groups.find((g) => g.d.id === project)!;
    const walls = groups.filter((g) => g.d.id !== project && (whole || g.y < own.y + own.h));
    return { ids, whole, project, from: ids.map(start), mine: nodes.filter((n) => n.d.id === project).map((n) => ({ id: n.id, x: n.x, y: n.y, w: n.w, h: n.h })), walls, at: { x: 0, y: 0 }, offset: whole ? { x: 0, y: 0 } : { x: byId.get(ids[0]!)!.ox, y: byId.get(ids[0]!)!.oy } };
  }
  // Moves what is held as far toward `to` (from where it was picked up) as the other frames allow.
  function shift(h: Hand, to: { x: number; y: number }) {
    motion = null; // a hand moves things at once
    const place = (n: Rect, by: { x: number; y: number }) => ({ x: Math.max(0, n.x + by.x), y: Math.max(0, n.y + by.y) });
    const clear = (by: { x: number; y: number }) => {
      const moved = h.mine.map((n) => { const f = h.from.find((x) => x.id === n.id); return f ? { ...n, ...place(f, by) } : n; });
      const frame = frameOf(moved);
      return !h.walls.some((w) => overlaps(frame, w, CLEAR));
    };
    h.at = clear(h.at) ? slide(h.at, to, clear) : to;
    // A project drawn lower than saved, to stay clear of the one above, is first saved where it is seen: otherwise the
    // hand would move it against a push that changes with every move.
    if (!h.saved) {
      h.saved = true;
      for (const r of rows.flat().filter((r) => r.d.id === h.project && r.down)) pos[r.p.id] = { x: r.x, y: r.y };
    }
    kept = { ...kept, [h.project]: h.offset };
    if (!h.whole) placed = new Set([...placed, ...h.ids]);
    for (const f of h.from) {
      const now = place(f, h.at);
      // Saved without the push on the frame, which the whole frame gets again when it is drawn.
      pos[f.id] = { x: Math.max(0, now.x - h.offset.x), y: Math.max(0, now.y - h.offset.y) };
    }
    // The walls in the coordinates the phases are laid out in, before their frame is pushed.
    hold = { ids: new Set(h.ids), project: h.project, from: h.mine, walls: h.walls.map((w) => ({ ...w, x: w.x - h.offset.x, y: w.y - h.offset.y })) };
  }
  // Letting go: whoever stepped aside stays there.
  function settleHand() {
    if (!hold) return;
    const { project, from } = hold;
    const aside = nodes.filter((n) => n.dodged && n.d.id === project);
    for (const n of aside) pos[n.id] = { x: n.x - n.ox, y: n.y - n.oy };
    if (aside.length) placed = new Set([...placed, ...aside.map((n) => n.id)]);
    // A side picked by hand belongs to the way two phases faced each other. Where they now face another way, from where the
    // hand took hold to where it let go, their line chooses for itself again.
    const start = new Map(from.map((r) => [r.id, r]));
    for (const e of edges) {
      if (!e.from || !e.to || !lines[e.key] || e.from.d.id !== project) continue;
      const a0 = start.get(e.from.id), b0 = start.get(e.to.id), a1 = byId.get(e.from.id), b1 = byId.get(e.to.id);
      if (!a0 || !b0 || !a1 || !b1) continue;
      const was = facing(a0, b0), now = facing(a1, b1);
      if (was.from !== now.from || was.to !== now.to) delete lines[e.key];
    }
    hold = null;
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
    } else if (d.moved) { settleHand(); queueSave(); }
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
    clearTimeout(motionTimer);
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
  /** `above` and `top`: room kept over the node, in world and in screen px (a frame's title under the floating bar). */
  function reveal(id: string, above = 0, top = 16) {
    const n = byId.get(id);
    if (!n) return;
    const { cw, ch } = bounds();
    const sx = pan.x + n.x * k, sy = pan.y + n.y * k, w = n.w * k, h = n.h * k, m = 16;
    let nx = pan.x, ny = pan.y;
    if (sx < m) nx += m - sx;
    else if (sx + w > cw - m) nx -= sx + w - (cw - m);
    if (sy - above * k < top) ny += top - (sy - above * k);
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
  const select = (id: string | null) => { selected = id; selTask = null; if (id) { if (crowded()) toolsOpen = false; revealSoon(id); } };
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
    motion = { id, opening: s.has(id) };
    clearTimeout(motionTimer);
    motionTimer = setTimeout(() => (motion = null), SLOW + 60);
    placed = new Set();
    kept = {};
    open = s;
  };
  function nodeKey(e: KeyboardEvent, n: Node) {
    if (e.target !== e.currentTarget) return; // keys of the inner buttons are their own
    const step = e.shiftKey ? 32 : 8;
    const mv = { ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step] }[e.key];
    if (mv) {
      e.preventDefault();
      shift(take([n.id], false), { x: mv[0]!, y: mv[1]! });
      settleHand();
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
  const waits = $derived(waitingOf(execs.data?.items ?? []));
  const isWaiting = (t: TaskSummary) => !!t.execution_id && waits.has(t.execution_id);
  const taskWord = (t: TaskSummary) => (isWaiting(t) ? 'Waiting' : t.state === 'in_progress' ? 'Running' : t.state === 'done' ? 'Done' : label(t.state));
  const run = $derived(task?.execution_id ? execs.data?.items.find((x) => x.id === task.execution_id) : undefined);
  const doneOf = (n: Node) => (n.tasks.length ? `${n.tasks.filter((x) => x.state === 'done').length} of ${plural(n.tasks.length, 'task')}` : 'no tasks');
  // Two state words are too long for the compact node; the node's name keeps the full one.
  const SHORT: Record<string, string> = { awaiting_phase_review: 'In review', revision_required: 'Revision' };
  const ICON: Record<string, 'check' | 'run' | 'lock' | 'warn' | 'wait'> = { completed: 'check', in_progress: 'run', awaiting_phase_review: 'lock', revision_required: 'warn' };
  const decisions = $derived(status.data?.needs_attention.filter((x) => x.kind === 'approval').length ?? 0);
  // What waits on a project, for the chip beside its name.
  const asks = (d: ProjectDetail) => {
    const has = (c: string) => d.pending_approval_items.some((x) => x.category === c);
    return [
      ...(has('workflow') ? [{ text: 'Proposal ready', to: 'plan', open: 'Plan' }] : []),
      ...(has('phase_completion') ? [{ text: 'Review ready', to: 'review', open: 'Review' }] : []),
      ...(has('risky_action') ? [{ text: 'Risky action', to: '', open: 'page' }] : []),
    ];
  };
  const summaryOf = (n: Node) => {
    const t = n.tasks;
    const done = t.filter((x) => x.state === 'done').length;
    if (n.phase.state === 'completed') return 'Completed and approved.';
    if (n.phase.state === 'awaiting_phase_review') return t.length ? `${done} of ${t.length} tasks done. Waiting for your review.` : 'Waiting for your review.';
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
        <div class="gbar">
          <div class="seg" role="group" aria-label="View">
            <button type="button" aria-pressed={view === 'graph'} onclick={() => (view = 'graph')}>Graph</button>
            <button type="button" aria-pressed={view === 'table'} onclick={() => (view = 'table')}>Table</button>
          </div>
          <div class="flex flex-wrap gap-2" role="group" aria-label="Projects drawn">
            {#each details.data ?? [] as d (d.id)}
              <button type="button" class="gchip press" aria-pressed={!off.has(d.id)} onclick={() => chipToggle(d.id)}>
                <Icon name={off.has(d.id) ? 'neu' : forks.data?.some((f) => f.project_id === d.id) ? 'fork' : 'check'} size={14} />{d.name}
              </button>
            {/each}
          </div>
          <div class="ml-auto flex items-center gap-4">
            {#if decisions}<a class="inline-flex min-h-11 items-center no-underline" href={href('/')}><Badge state="awaiting_approval" text="{plural(decisions, 'decision')} waiting" /></a>{/if}
            <span class="gconn">
              {#if status.data?.needs_attention.some((x) => x.kind !== 'approval')}<a class="inline-flex min-h-11 items-center no-underline" href={href('/')}><Badge state="blocked" text="{status.data.needs_attention.length} {status.data.needs_attention.length === 1 ? 'needs' : 'need'} attention" /></a>{/if}
              <ConnState />
            </span>
          </div>
          {#if forksFailed}<p class="gnote">Fork lines could not be read, so they are not drawn.</p>{/if}
          {#if execs.error}<p class="gnote">Executions could not be read, so waiting harnesses are not marked.</p>{/if}
          {#if layoutReadFailed}<p class="gnote">The saved arrangement could not be read. Changes are not saved until you reload this page.</p>{/if}
        </div>
        {#if view === 'graph'}
          <div class="canvas" class:held bind:this={canvas} role="application" aria-label="Project graph. Drag the background to pan. With a phase focused, the arrow keys move it, Space opens its detail and Enter expands it. Line sides are set in the tools."
            onpointerdown={panDown} onwheel={wheel}>
            <div class="world" class:settle style="width:{world.w}px;height:{world.h}px;transform:translate({pan.x}px,{pan.y}px) scale({k})">
              {#if showFrames}
                {#each frames as g (g.d.id)}
                  <div class="group" style="left:{g.x}px;top:{g.y}px;width:{g.w}px;height:{g.h}px">
                    <div class="gtag" data-gtag role="presentation" onpointerdown={(e) => nodeDown(e, g.ids)}><b>{g.d.name}</b>{#each asks(g.d) as q (q.text)}<a href={href(`/projects/${g.d.id}${q.to ? '/' + q.to : ''}`)} aria-label="{q.text}, open the {q.open} of {g.d.name}" onpointerdown={(e) => e.stopPropagation()} onfocus={() => reveal(g.ids.reduce((a, b) => (byId.get(b)!.y < byId.get(a)!.y ? b : a)), TAG + 24, 72)}><Badge state="awaiting_approval" text={q.text} /></a>{/each}</div>
                  </div>
                {/each}
              {/if}
              <svg class="edges" width={world.w} height={world.h} aria-hidden="true">
                {#each edges as e (e.key)}
                  <path d={e.path} data-tone={e.tone} class:fork={e.fork} fill="none" />
                  <circle class="port" data-tone={e.tone} cx={e.x1} cy={e.y1} r="5" class:fork={e.fork} />
                  <circle class="port" data-tone={e.tone} cx={e.x2} cy={e.y2} r="5" class:fork={e.fork} />
                  <!-- The end circles are a pointer shortcut; the same choice is available by keyboard in the tools pane (Lines). -->
                  {#if !e.fork}
                    <circle class="hit" role="presentation" data-port cx={e.x1} cy={e.y1} r="12" onpointerdown={(ev) => portDown(ev, e, 'from')} />
                    <circle class="hit" role="presentation" data-port cx={e.x2} cy={e.y2} r="12" onpointerdown={(ev) => portDown(ev, e, 'to')} />
                  {/if}
                {/each}
              </svg>
              {#each edges.filter((e) => e.fork) as e (e.key + 'l')}
                <span class="elabel" style="left:{e.mx}px;top:{e.my}px"><Badge state="fork" text={e.text} /></span>
              {/each}
              {#each drawn as n (n.id)}
                <!-- A phase node is a composite widget (a draggable card with its own buttons), so it is focusable with arrow, Enter, Space and Esc keys. -->
                <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
                <div class="node" data-id={n.id} bind:offsetHeight={heights[n.id]} class:sel={selected === n.id} class:held={heldIds.includes(n.id)} class:slow={motion?.id === n.id && motion.opening} class:fast={motion?.id === n.id && !motion.opening} data-node tabindex="0" role="group" aria-current={selected === n.id ? 'true' : undefined} aria-label="{n.phase.code} {n.phase.title}, {label(n.phase.state)}"
                  style="left:{n.x}px;top:{n.y}px;width:{n.w}px;min-height:{BASE_H}px" onpointerdown={(e) => nodeDown(e, [n.id], n.id)} onfocus={() => { if (!drag) reveal(n.id); }} onkeydown={(e) => nodeKey(e, n)}>
                  <div class="nt">
                    <span class="nico" data-tone={edgeTone(n.phase.state)} aria-hidden="true"><Icon name={ICON[n.phase.state] ?? 'wait'} size={16} /></span>
                    <span class="min-w-0 flex-1"><b title="{n.phase.code} {n.phase.title}">{n.phase.code} {visible(n.phase.title)}</b><small>{doneOf(n)}</small></span>
                  </div>
                  <div class="nb">
                    <Badge plain state={n.phase.state} text={n.open ? undefined : SHORT[n.phase.state]} />
                    <button type="button" class="chev" aria-expanded={n.open} aria-label="{n.open ? 'Collapse' : 'Expand'} {n.phase.title}" disabled={!n.hasTasks} onclick={() => toggle(n.id)}>
                      <span class="swap"><span data-on={!n.open}><Icon name="chev" size={14} /></span><span data-on={n.open}><Icon name="chevu" size={14} /></span></span>
                    </button>
                  </div>
                  <div class="expand" data-open={n.open && n.hasTasks} inert={!(n.open && n.hasTasks)}>
                    <ul class="tasks">
                      {#each n.tasks as t (t.id)}
                        <li><button type="button" class="trow" class:on={selTask === t.id} class:wait={isWaiting(t)} aria-label="{t.code} {t.title}, {taskWord(t)}" onclick={() => pickTask(n, t)}><span aria-hidden="true" class="inline-flex"><StatusDot state={taskState(t, waits)} /></span><span class="mono !text-xs">{t.code}</span><span class="min-w-0 flex-1 truncate" title={visible(t.title)}>{visible(t.title)}</span></button></li>
                      {/each}
                    </ul>
                  </div>
                </div>
              {/each}
            </div>
          </div>
          <div class="gcapsule">
            <div class="zoombar" role="group" aria-label="Zoom">
              <button type="button" aria-label="Zoom out" onclick={() => zoom(-0.1)}>−</button>
              <span class="mono" aria-live="polite">{Math.round(k * 100)}%</span>
              <button type="button" aria-label="Zoom in" onclick={() => zoom(0.1)}>+</button>
            </div>
            <span class="sep" aria-hidden="true"></span>
            <button type="button" class="cap" aria-label="Fit view" onclick={() => fit(true)}>Fit</button>
            <label class="cap"><input type="checkbox" aria-label="Show project frames" bind:checked={showFrames} />Frames</label>
            <button type="button" class="cap" aria-expanded={toolsOpen} aria-controls="gtools" onclick={() => (toolsOpen = !toolsOpen)}>Tools</button>
          </div>
        {:else}
          <div class="gtable">
            <div class="panel">
              <div class="grid-head grow5" aria-hidden="true"><span>Phase</span><span>Status</span><span class="num">Tasks</span><span class="num">Done</span><span></span></div>
              <ul class="items" aria-label="Phases">
                {#each nodes as n (n.id)}
                  <li class="grow5">
                    <div class="min-w-0"><a class="tlink" href={href(reviewLink(n))}>{n.phase.code} · {visible(n.phase.title)}</a><div class="mono muted small truncate">{n.d.name}{forks.data?.some((f) => f.project_id === n.d.id) ? ' · fork' : ''}</div></div>
                    <div><Badge state={n.phase.state} /></div>
                    <span class="num">{n.tasks.length || '—'}</span>
                    <span class="num">{n.tasks.length ? `${n.tasks.filter((x) => x.state === 'done').length} of ${n.tasks.length}` : '—'}</span>
                    <div class="acts">{#if n.phase.state === 'awaiting_phase_review'}<Btn kind="pri" lock sm label="Approve {n.phase.title}" href={href(reviewLink(n))}>Approve</Btn>{/if}</div>
                  </li>
                {/each}
              </ul>
            </div>
          </div>
        {/if}
        {#if saved.data && nodes.length === 0 && view === 'graph'}
          <p class="gempty">{details.data?.length ? 'No project is drawn. Switch one on with its chip above.' : 'No project has a workflow yet. Register a project and approve a plan to see it here.'}</p>
        {/if}
      </div>

      <div class="gdetail" data-open={detailOpen} inert={!detailOpen}>
      {#if sel}
        <aside class="gpane" aria-label={task ? 'Task detail' : 'Phase detail'}>
          <div class="flex items-center gap-2">
            <span class="min-w-0 flex-1 text-xs text-fg-muted">{sel.d.name}{task ? `, ${sel.phase.code} ${sel.phase.title}` : ''}</span>
            <button type="button" class="x" aria-label="Close detail" onclick={closeDetail}><Icon name="x" size={16} /></button>
          </div>
          {#if task}
            <h2>{#if task.execution_id}<a class="tlink" href={href('/executions/' + task.execution_id)}>{visible(task.title)}</a>{:else}{visible(task.title)}{/if}</h2>
            <div><Badge state={taskState(task, waits)} /></div>
            <dl class="kv2">
              <dt>Task</dt><dd class="mono">{task.code}</dd>
              <dt>Mode</dt><dd>{task.mode === 'read_only' ? 'Read only' : 'Code change'}</dd>
              <dt>Depends on</dt><dd class="mono">{task.depends_on.length ? task.depends_on.join(', ') : 'nothing'}</dd>
              {#if task.execution_id}<dt>Execution</dt><dd class="mono">{task.execution_id}</dd>{/if}
              {#if run}<dt>Harness</dt><dd>{run.harness}</dd>{#if run.started_at}<dt>Started</dt><dd>{ago(run.started_at)}</dd>{/if}{/if}
            </dl>
            {#if isWaiting(task)}
              <div class="callout waiting"><p>This task has waited {waitedFor(waits.get(task.execution_id!)!)}. Claude Code asks for you in its own session. It keeps its task and lock until you release it.</p></div>
            {/if}
            {#if task.execution_id}
              <div class="flex flex-wrap gap-2.5">
                {#if isWaiting(task)}<Btn kind="info" class="flex-1" label="Release {task.title}" onclick={() => (releasing = { id: task.execution_id!, title: task.title, code: task.code })}>Release task</Btn>{/if}
                <Btn class="flex-1" href={href('/executions/' + task.execution_id)}>Open execution</Btn>
              </div>
            {/if}
          {:else}
            <h2><a class="tlink" href={href(reviewLink(sel))}>{sel.phase.code} {visible(sel.phase.title)}</a></h2>
            <div><Badge state={sel.phase.state} /></div>
            <p class="max-w-[65ch]">{visible(rev?.summary ?? summaryOf(sel))}</p>
            <dl class="kv2">
              <dt>Tasks</dt><dd>{doneOf(sel)} done</dd>
              {#if rev}
                <dt>Requested by</dt><dd>{aprNow?.requested_by ?? 'Waves'}</dd>
                <dt>Validators</dt><dd>{rev.validators.filter((v) => v.state === 'passed').length} of {rev.validators.length} passed</dd>
                <dt>Changes</dt><dd class="mono">{rev.diff.files} files, +{rev.diff.added} −{rev.diff.removed}</dd>
                <dt>Outside the plan</dt><dd class={rev.findings.length ? 'text-st-attention' : ''}>{rev.findings.length ? plural(rev.findings.length, 'change') : 'nothing'}</dd>
              {/if}
            </dl>
            {#if rev}
              <div class="secnote"><Icon name="lock" size={14} /><span>Approving asks for your passphrase. An agent cannot approve.</span></div>
              {#if aprNow?.state === 'pending'}
                <div class="flex gap-2.5"><Btn kind="pri" lock class="flex-1" disabled={broken} onclick={() => (deciding = { id: aprNow.id, mode: 'approve' })}>Approve</Btn><Btn kind="dng" sm disabled={broken} onclick={() => (deciding = { id: aprNow.id, mode: 'reject' })}>Reject</Btn></div>
              {/if}
            {:else}
              {#each sel.tasks.filter(isWaiting) as t (t.id)}
                <div class="callout waiting">
                  <p>Task {t.code} has waited {waitedFor(waits.get(t.execution_id!)!)}. Claude Code asks for you in its own session.</p>
                  <div><Btn sm kind="info" label="Release {t.title}" onclick={() => (releasing = { id: t.execution_id!, title: t.title, code: t.code })}>Release task</Btn></div>
                </div>
              {/each}
            {/if}
          {/if}
          <section class="flex flex-col gap-2">
            <h3>Project activity</h3>
            <ul class="plain">
              {#each recent as r (r.seq)}<li><b>{capital(r.event.replace(/[._]/g, ' '))}</b> {visible(r.subject)}<div class="text-xs text-fg-muted">{ago(r.at)}</div></li>{:else}<li class="muted">No recent activity for this project.</li>{/each}
            </ul>
          </section>
          {#if task}<button type="button" class="linkbtn self-start text-[13px] font-bold !text-link !no-underline hover:!underline" onclick={() => (selTask = null)}>Back to {sel.phase.code} {sel.phase.title}</button>{/if}
        </aside>
      {/if}
      </div>

      <div class="gside" data-open={toolsOpen && view === 'graph'}>
        <aside class="gtools" id="gtools" aria-label="Graph tools" inert={!(toolsOpen && view === 'graph')}>
          <section>
            <h2>Lines</h2>
            <p class="muted">Drag the end circle of a line round its phase, or pick the sides here. A side you pick is dropped when you move the two phases so that they face each other another way.</p>
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
          <section>
            <h2>Keys</h2>
            <p class="muted">Tab to a phase. Space opens its detail, Enter expands it, arrow keys move it by 8 px (Shift 32), Esc closes the detail.</p>
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
  <ReleaseLease target={releasing} oncancel={() => (releasing = null)} ondone={() => { releasing = null; void execs.reload(); void details.reload(); }} />
{/if}
