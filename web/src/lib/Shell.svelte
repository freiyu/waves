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
  import Note from './Note.svelte';
  import Btn from './Btn.svelte';
  import StepUpDialog from './StepUpDialog.svelte';
  import Palette from './Palette.svelte';
  import Badge from './Badge.svelte';
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
    { label: 'Executions', path: '/executions', icon: 'exec', routes: ['executions', 'execution', 'recovery'] },
  ];
  const SYSTEM: Item[] = [
    { label: 'Harnesses', path: '/harnesses', icon: 'plug', routes: ['harnesses', 'connect'] },
    { label: 'Activity', path: '/activity', icon: 'log', routes: ['activity'] },
    { label: 'Settings', path: '/settings', icon: 'gear', routes: ['settings'] },
  ];

  // The pages of one project share a key, so moving between its panes keeps them mounted.
  const AREA = ['project', 'requirements', 'plan', 'review', 'guard'];
  const pageKey = $derived(AREA.includes(route.name) ? 'area:' + route.params.id : route.name + JSON.stringify(route.params));
  const active = $derived(projects.data?.items.find((p) => p.active));
  // The Executions pill counts executions that hold a lease (running); interrupted ones are listed on their page.
  const activeExec = $derived(status.data?.counts.running ?? 0);
  const attention = $derived(status.data?.needs_attention.length ?? 0);
  const broken = $derived(status.data?.audit.state === 'broken');
  const conn = $derived(a.state.conn);
  const liveTone = $derived(conn === 'live' ? 'text-st-ok' : conn === 'stale' ? 'text-st-danger' : 'text-st-warning');
  const liveLabel = $derived(conn === 'live' ? 'Live' : conn === 'stale' ? 'Stale' : conn === 'reconnecting' ? 'Reconnecting' : 'Connecting');
  const el = $derived(a.state.elevation?.kind === 'window' ? a.state.elevation : null);

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
<div class="app" class:rail={route.name === 'graph'}>
  {#if menu}<div class="scrim" role="presentation" onclick={() => (menu = false)}></div>{/if}
  <aside class="side" class:open={menu} aria-label="Primary">
    <a class="brand" href={href('/')} aria-label="Waves, overview">
      <Logo />
    </a>

    <nav aria-label="Sections">
      <div class="group-label">Work</div>
      {#each WORK as n}
        <a class="nav" href={href(n.path)} aria-current={n.routes.includes(route.name) ? 'page' : undefined}>
          <Icon name={n.icon} size={20} stroke={2} /><span class="lbl">{n.label}</span>{#if n.label === 'Executions' && activeExec}<span class="cnt">{activeExec}<span class="sr-only"> active</span></span>{/if}
        </a>
      {/each}
      <div class="group-label">System</div>
      {#each SYSTEM as n}
        <a class="nav" href={href(n.path)} aria-current={n.routes.includes(route.name) ? 'page' : undefined}><Icon name={n.icon} size={20} stroke={2} /><span class="lbl">{n.label}</span></a>
      {/each}
    </nav>

    <DropdownMenu.Root>
      <DropdownMenu.Trigger class="account">
        <span aria-hidden="true" class="flex size-7 flex-none items-center justify-center rounded-full bg-primary text-xs font-bold text-primary-fg">O</span>
        <span class="acc-text min-w-0 flex-1 text-left leading-[18px]"><b class="block">Operator</b><span class="block truncate text-xs text-fg-muted">{el ? 'Passphrase unlocked' : 'Passphrase locked'}</span></span>
        <Icon name="updown" size={14} class="acc-text text-fg-muted" />
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content side="top" align="start" sideOffset={6} class="{menuPop} w-[var(--bits-dropdown-menu-anchor-width)]">
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
    <header class="top">
      <button type="button" class="menu-btn inline-flex min-h-7 cursor-pointer items-center gap-1.5 rounded-md border border-line-strong bg-surface px-2.5 text-[13px] font-semibold" aria-expanded={menu} onclick={() => (menu = !menu)}><Icon name="menu" size={14} />Menu</button>
      <span class="top-title" aria-hidden="true"></span>
      <button type="button" class="search" aria-label="Search or jump to" onclick={() => (palette = true)}>
        <Icon name="search" /><span class="flex-1 text-left">Search or jump to…</span><kbd>Ctrl</kbd><kbd>K</kbd>
      </button>
      <div class="top-right">
      {#if attention}
        <a class="no-underline" href={href('/')}><Badge state="awaiting_approval" text="{attention} {attention === 1 ? 'needs' : 'need'} attention" /></a>
      {/if}
      <span class="inline-flex min-w-[104px] items-center gap-1.5 text-xs text-fg-muted" role="status">
        <span class="inline-flex {liveTone}"><Icon name={conn === 'live' ? 'dot' : conn === 'stale' ? 'fail' : 'warn'} size={conn === 'live' ? 10 : 14} /></span>{liveLabel}
      </span>
      {#if el}
        <Btn sm lock onclick={() => a.dropElevation()} title="Lock again now">Unlocked until {until(el.expiresAt)}<span class="sr-only">. Activate to lock again now.</span></Btn>
      {:else}
        <Btn sm lock onclick={() => (elevate = true)} title="Elevate: unlock locked actions for a few minutes">Elevate</Btn>
      {/if}
      </div>
    </header>
    <main class="page outline-none" class:flush={route.name === 'graph'} id="main" tabindex="-1">
      {#if broken}
        <Note kind="warn" role="alert"><b>The audit chain did not verify</b> at record {status.data?.audit.broken_at_record?.toLocaleString('en')}. Approvals are blocked until a human acknowledges it. <a href={href('/activity')}>Open Activity</a></Note>
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

{#if elevate}
  <StepUpDialog
    title="Elevate this session"
    kicker="Elevate with your passphrase"
    confirm="Elevate"
    note="Typed into this page only; not stored, not logged."
    onconfirm={async (p) => { await a.stepUp(p); elevate = false; }}
    oncancel={() => (elevate = false)}
  >
    {#snippet extra()}
      <div>The window (length set in Settings) unlocks locked settings and the Workflow, Result and Risky action approvals. <b>Phase Start and Phase Completion always ask again.</b> It ends on its own or when you lock again.</div>
    {/snippet}
  </StepUpDialog>
{/if}
