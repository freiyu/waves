<script lang="ts">
  import { setContext, type Snippet } from 'svelte';
  import { DropdownMenu } from 'bits-ui';
  import { app } from './app.svelte';
  import { resource } from './resource.svelte';
  import { route, href } from './router.svelte';
  import { SHELL } from './shell';
  import { theme, setTheme, type Theme } from './theme.svelte';
  import Icon, { type IconName } from './Icon.svelte';
  import Logo from './Logo.svelte';
  import DoneMark from './DoneMark.svelte';
  import Note from './Note.svelte';
  import Btn from './Btn.svelte';
  import StepUpDialog from './StepUpDialog.svelte';
  import Palette from './Palette.svelte';
  import Badge from './Badge.svelte';
  import ConnState from './ConnState.svelte';
  import { menuPop, menuItem, menuHeading } from './ui';

  let { children }: { children: Snippet } = $props();
  const a = app();

  const status = resource(() => a.api.status(), ['attention.changed', 'approval.changed', 'execution.changed', 'audit.alert']);
  const projects = resource(() => a.api.projects(), ['project.changed', 'execution.changed', 'approval.changed', 'phase.changed']);
  setContext(SHELL, { status, projects });

  type Item = { label: string; path: string; icon: IconName; routes: string[] };
  const WORK: Item[] = [
    { label: 'Overview', path: '/', icon: 'overview', routes: ['overview'] },
    { label: 'Graph', path: '/graph', icon: 'graph', routes: ['graph'] },
    { label: 'Projects', path: '/projects', icon: 'folder', routes: ['projects', 'project', 'requirements', 'plan', 'review', 'phase-review', 'guard'] },
    { label: 'Executions', path: '/executions', icon: 'pulse', routes: ['executions', 'execution', 'recovery'] },
  ];
  const SYSTEM: Item[] = [
    { label: 'Harnesses', path: '/harnesses', icon: 'plug', routes: ['harnesses', 'connect'] },
    { label: 'Activity', path: '/activity', icon: 'lines', routes: ['activity'] },
    { label: 'Settings', path: '/settings', icon: 'gear', routes: ['settings'] },
  ];

  // The pages of one project share a key, so moving between its panes keeps them mounted.
  const AREA = ['project', 'requirements', 'plan', 'review', 'guard'];
  const pageKey = $derived(AREA.includes(route.name) ? 'area:' + route.params.id : route.name + JSON.stringify(route.params));
  const active = $derived(projects.data?.items.find((p) => p.active));
  const attention = $derived(status.data?.needs_attention.length ?? 0);
  // The one warm dot on the rail: an approval waits, and the Overview lists it.
  const decisions = $derived(status.data?.needs_attention.some((x) => x.kind === 'approval') ?? false);
  const broken = $derived(status.data?.audit.state === 'broken');
  const el = $derived(a.state.elevation?.kind === 'window' ? a.state.elevation : null);
  // The Graph draws its own bar, so the top row gives way there; it returns whenever there is something it must say.
  const bare = $derived(route.name === 'graph' && !el && !broken && a.state.conn !== 'reconnecting' && a.state.conn !== 'stale' && a.state.session?.passphrase_set !== false);

  let menu = $state(false);
  let elevate = $state(false);
  let palette = $state(false);
  $effect(() => {
    void route.name;
    menu = false;
  });
  const until = (t: number) => new Date(t).toTimeString().slice(0, 5);

  function key(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
      e.preventDefault();
      palette = !palette;
    }
  }

  const THEMES: Array<{ v: Theme; label: string; icon: IconName }> = [
    { v: 'light', label: 'Light', icon: 'sun' }, { v: 'dark', label: 'Dark', icon: 'moon' }, { v: 'system', label: 'Auto', icon: 'monitor' },
  ];
</script>

<svelte:window onkeydown={key} />
<a class="sr-only" href="#main" onclick={(e) => { e.preventDefault(); document.getElementById('main')?.focus(); }}>Skip to content</a>
<div class="app">
  {#if menu}<div class="scrim" role="presentation" onclick={() => (menu = false)}></div>{/if}
  <aside class="side" class:open={menu} aria-label="Primary">
    <a class="brand" href={href('/')} aria-label="Waves, overview">
      <Logo />
    </a>

    <nav aria-label="Sections">
      {#each [WORK, SYSTEM] as group, g}
        {#if g}<span class="nav-gap" aria-hidden="true"></span>{/if}
        {#each group as n}
          {@const dot = n.path === '/' && decisions}
          <a class="nav" href={href(n.path)} aria-current={n.routes.includes(route.name) ? 'page' : undefined}>
            <Icon name={n.icon} size={22} /><span class="lbl">{n.label}</span>{#if dot}<span class="sig"><span class="sr-only">, decisions waiting</span></span>{/if}
          </a>
        {/each}
      {/each}
    </nav>

    <DropdownMenu.Root>
      <DropdownMenu.Trigger class="account">
        <Icon name="user" size={20} /><span class="lbl" aria-hidden="true">Operator</span><span class="sr-only">Operator, {el ? 'passphrase unlocked' : 'passphrase locked'}</span>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content side="right" align="end" sideOffset={8} class="{menuPop} w-60">
          <div class="px-2.5 pt-1.5 pb-1 leading-[18px]"><b class="block">Operator</b><span class="text-xs text-fg-muted">{el ? 'Passphrase unlocked' : 'Passphrase locked'}</span></div>
          <DropdownMenu.RadioGroup value={theme.value} onValueChange={(v) => setTheme(v as Theme)}>
            <DropdownMenu.GroupHeading class={menuHeading}>Theme</DropdownMenu.GroupHeading>
            <div class="mx-1 mb-1 flex gap-0.5 rounded-lg bg-muted p-[3px]">
              {#each THEMES as t}
                <DropdownMenu.RadioItem value={t.v} closeOnSelect={false} class="flex h-7 flex-1 cursor-pointer items-center justify-center gap-1.5 rounded-md text-[13px] font-semibold text-fg-muted outline-none data-[highlighted]:text-fg data-[highlighted]:ring-2 data-[highlighted]:ring-inset data-[highlighted]:ring-[var(--focus)] data-[state=checked]:bg-surface data-[state=checked]:text-fg data-[state=checked]:shadow-sm">
                  <Icon name={t.icon} size={14} />{t.label}
                </DropdownMenu.RadioItem>
              {/each}
            </div>
          </DropdownMenu.RadioGroup>
          <DropdownMenu.Separator class="-mx-1.5 my-1.5 h-px bg-line" />
          {#if !el}<DropdownMenu.Item class={menuItem} onSelect={() => (elevate = true)}><Icon name="lock" class="text-fg-muted" />Elevate</DropdownMenu.Item>{/if}
          <DropdownMenu.Item class={menuItem} onSelect={() => (palette = true)}><Icon name="search" class="text-fg-muted" />Search<span class="ml-auto text-xs text-fg-muted">Ctrl K</span></DropdownMenu.Item>
          <DropdownMenu.Item class={menuItem} onSelect={() => a.signOut()}><Icon name="signout" class="text-fg-muted" />Sign out</DropdownMenu.Item>
          <div class="px-2.5 pt-1.5 pb-1 text-xs text-fg-muted">waves {status.data?.server_version ?? ''}{status.data?.claude_code_version ? ` · Claude Code ${status.data.claude_code_version}` : ''}</div>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  </aside>

  <div class="main">
    <header class="top" class:bare>
      <button type="button" class="menu-btn press mr-auto inline-flex h-11 cursor-pointer items-center gap-1.5 rounded-xl border border-line-strong bg-surface px-3 text-sm font-semibold" aria-expanded={menu} onclick={() => (menu = !menu)}><Icon name="menu" size={14} />Menu</button>
      <div id="pagecrumb" class="contents"></div>
      <div class="top-right">
      <button type="button" class="search" aria-label="Search or jump to" title="Search (Ctrl K)" onclick={() => (palette = true)}>
        <Icon name="search" />
      </button>
      {#if attention && route.name !== 'overview'}
        <a class="inline-flex min-h-11 items-center no-underline" href={href('/')}><Badge state="blocked" text="{attention} {attention === 1 ? 'needs' : 'need'} attention" /></a>
      {/if}
      <ConnState />
      {#if el}
        <Btn sm lock onclick={() => a.dropElevation()} title="Lock again now">Unlocked until {until(el.expiresAt)}<span class="sr-only">. Activate to lock again now.</span></Btn>
      {:else}
        <Btn lock class="max-[520px]:hidden" onclick={() => (elevate = true)} title="Elevate: unlock locked actions for a few minutes">Elevate</Btn>
      {/if}
      </div>
      <div id="pageacts" class="contents"></div>
    </header>
    <main class="page outline-none" class:flush={route.name === 'graph'} id="main" tabindex="-1">
      {#if broken}
        <Note kind="warn" role="alert"><b>The audit chain did not verify</b> at record {status.data?.audit.broken_at_record?.toLocaleString('en')}. Approvals are blocked until a human acknowledges it. <a href={href('/activity')}>Open Activity</a></Note>
      {/if}
      {#if a.state.conn === 'stale'}
        <Note kind="warn" role="alert"><b>The connection to Waves has been lost</b> for over 30 seconds. What you see may be out of date.</Note>
      {/if}
      {#if a.state.session && !a.state.session.passphrase_set}
        <Note kind="info" icon="lock">The approval passphrase is not set, so every approval is unavailable. <a href={href('/welcome')}>What to do</a></Note>
      {/if}
      {#key pageKey}
        <div class="page-in flex min-w-0 flex-col gap-6">{@render children()}</div>
      {/key}
    </main>
  </div>
</div>

<Palette bind:open={palette} />
<DoneMark />

{#if elevate}
  <StepUpDialog
    title="Elevate this session"
    kicker="Elevate with your passphrase"
    confirm="Elevate"
    done="Elevated"
    note="Sent only to Waves on this machine. Not stored, not logged."
    onconfirm={async (p) => { await a.stepUp(p); elevate = false; }}
    oncancel={() => (elevate = false)}
  >
    {#snippet extra()}
      <div>The window (length set in Settings) unlocks locked settings and the Workflow, Result and Risky action approvals. <b>Phase Start and Phase Completion always ask again.</b> It ends on its own or when you lock again.</div>
    {/snippet}
  </StepUpDialog>
{/if}
