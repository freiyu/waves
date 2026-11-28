<script lang="ts" module>
  // Icons are imported one by one from lucide (tree-shaken). Status icons keep the design's meaning:
  // play, lock, check-circle, x-circle, triangle, open circle.
  import Play from '@lucide/svelte/icons/play';
  import Clock from '@lucide/svelte/icons/clock-3';
  import CircleCheck from '@lucide/svelte/icons/circle-check';
  import CircleX from '@lucide/svelte/icons/circle-x';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import Lock from '@lucide/svelte/icons/lock';
  import House from '@lucide/svelte/icons/house';
  import Folder from '@lucide/svelte/icons/folder';
  import Inbox from '@lucide/svelte/icons/inbox';
  import Terminal from '@lucide/svelte/icons/terminal';
  import Plug from '@lucide/svelte/icons/plug';
  import ScrollText from '@lucide/svelte/icons/scroll-text';
  import Settings from '@lucide/svelte/icons/settings';
  import Circle from '@lucide/svelte/icons/circle';
  import Menu from '@lucide/svelte/icons/menu';
  import Search from '@lucide/svelte/icons/search';
  import Bell from '@lucide/svelte/icons/bell';
  import ChevronDown from '@lucide/svelte/icons/chevron-down';
  import ChevronUp from '@lucide/svelte/icons/chevron-up';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import Ellipsis from '@lucide/svelte/icons/ellipsis';
  import Copy from '@lucide/svelte/icons/copy';
  import Plus from '@lucide/svelte/icons/plus';
  import X from '@lucide/svelte/icons/x';
  import Check from '@lucide/svelte/icons/check';
  import Sun from '@lucide/svelte/icons/sun';
  import Moon from '@lucide/svelte/icons/moon';
  import Monitor from '@lucide/svelte/icons/monitor';
  import ArrowRight from '@lucide/svelte/icons/arrow-right';
  import User from '@lucide/svelte/icons/user';
  import LogOut from '@lucide/svelte/icons/log-out';
  import MessageSquare from '@lucide/svelte/icons/message-square';
  import GitFork from '@lucide/svelte/icons/git-fork';
  import RotateCw from '@lucide/svelte/icons/rotate-cw';
  import ChevronsUpDown from '@lucide/svelte/icons/chevrons-up-down';
  import PanelRight from '@lucide/svelte/icons/panel-right';
  import LayoutDashboard from '@lucide/svelte/icons/layout-dashboard';
  import Waypoints from '@lucide/svelte/icons/waypoints';
  import Activity from '@lucide/svelte/icons/activity';
  import TextAlign from '@lucide/svelte/icons/align-left';

  const ICONS = {
    run: Play, wait: Clock, ok: CircleCheck, fail: CircleX, warn: TriangleAlert, neu: Circle,
    lock: Lock, home: House, folder: Folder, inbox: Inbox, exec: Terminal, plug: Plug, log: ScrollText,
    gear: Settings, dot: Circle, menu: Menu, search: Search, bell: Bell,
    chev: ChevronDown, chevu: ChevronUp, chevr: ChevronRight, chevl: ChevronLeft, more: Ellipsis, copy: Copy,
    plus: Plus, x: X, check: Check, sun: Sun, moon: Moon, monitor: Monitor, arrow: ArrowRight, user: User,
    signout: LogOut, release: LogOut, prompt: MessageSquare, fork: GitFork, retry: RotateCw, graph: Waypoints, pulse: Activity, lines: TextAlign, overview: LayoutDashboard, updown: ChevronsUpDown, panelr: PanelRight,
  };
  export type IconName = keyof typeof ICONS;
</script>

<script lang="ts">
  let { name, size = 16, stroke = 1.5, class: cls = '' }: { name: IconName; size?: number; stroke?: number; class?: string } = $props();
  const C = $derived(ICONS[name]);
</script>

<C {size} strokeWidth={stroke} class={cls} fill={name === 'dot' ? 'currentColor' : 'none'} aria-hidden="true" />
