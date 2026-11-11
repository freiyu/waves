<script lang="ts" module>
  // Icons are imported one by one from lucide (tree-shaken). Status icons keep the design's meaning:
  // play-circle, clock, check-circle, x-circle, triangle, dashed circle.
  import CirclePlay from '@lucide/svelte/icons/circle-play';
  import Clock from '@lucide/svelte/icons/clock-3';
  import CircleCheck from '@lucide/svelte/icons/circle-check';
  import CircleX from '@lucide/svelte/icons/circle-x';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import CircleDashed from '@lucide/svelte/icons/circle-dashed';
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

  const ICONS = {
    run: CirclePlay, wait: Clock, ok: CircleCheck, fail: CircleX, warn: TriangleAlert, neu: CircleDashed,
    lock: Lock, home: House, folder: Folder, inbox: Inbox, exec: Terminal, plug: Plug, log: ScrollText,
    gear: Settings, dot: Circle, menu: Menu, search: Search, bell: Bell,
    chev: ChevronDown, chevu: ChevronUp, chevr: ChevronRight, chevl: ChevronLeft, more: Ellipsis, copy: Copy,
    plus: Plus, x: X, check: Check, sun: Sun, moon: Moon, monitor: Monitor, arrow: ArrowRight, user: User,
    signout: LogOut,
  };
  export type IconName = keyof typeof ICONS;
</script>

<script lang="ts">
  let { name, size = 16, class: cls = '' }: { name: IconName; size?: number; class?: string } = $props();
  const C = $derived(ICONS[name]);
</script>

<C {size} strokeWidth={1.5} class={cls} fill={name === 'dot' ? 'currentColor' : 'none'} aria-hidden="true" />
