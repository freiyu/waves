<script lang="ts">
  // Command palette (Ctrl K): jumps to pages, projects and items that need attention. It only navigates; it never decides anything.
  import { Command, Dialog } from 'bits-ui';
  import { go } from './router.svelte';
  import { shellData } from './shell';
  import Icon, { type IconName } from './Icon.svelte';
  import { returnFocus } from './returnFocus';
  import { paletteItem, menuHeading } from './ui';

  let { open = $bindable(false) }: { open: boolean } = $props();
  const { status, projects } = shellData();

  const PAGES: Array<{ label: string; path: string; icon: IconName }> = [
    { label: 'Overview', path: '/', icon: 'home' },
    { label: 'Projects', path: '/projects', icon: 'folder' },
    { label: 'Approvals', path: '/approvals', icon: 'inbox' },
    { label: 'Executions', path: '/executions', icon: 'exec' },
    { label: 'Harnesses', path: '/harnesses', icon: 'plug' },
    { label: 'Activity', path: '/activity', icon: 'log' },
    { label: 'Settings', path: '/settings', icon: 'gear' },
  ];
  const attention = $derived(
    (status.data?.needs_attention ?? []).flatMap((i) =>
      i.ref_type === 'approval' ? [{ label: `${i.label}: ${i.subject}`, path: '/approvals/' + i.ref_id, icon: 'wait' as IconName, hint: 'Opens the approval' }]
      : i.ref_type === 'execution' ? [{ label: `${i.label}: ${i.subject}`, path: '/executions/' + i.ref_id, icon: 'run' as IconName, hint: 'Opens the execution' }] : [],
    ),
  );
  const pick = (path: string) => { open = false; go(path); };
  const item = paletteItem;
  const heading = menuHeading;
</script>

{#if open}<span hidden use:returnFocus></span>{/if}
<Dialog.Root bind:open>
  <Dialog.Portal>
    <Dialog.Overlay class="overlay fixed inset-0 z-40 bg-(--overlay)" />
    <Dialog.Content aria-label="Command palette" aria-describedby={undefined} onCloseAutoFocus={(e) => e.preventDefault()} class="pop fixed top-[14vh] left-1/2 z-50 w-[min(560px,calc(100vw-32px))] -translate-x-1/2 overflow-hidden rounded-xl border border-line bg-surface text-fg shadow-2xl">
      <Command.Root loop>
        <div class="flex items-center gap-2 border-b border-line px-3">
          <Icon name="search" class="text-fg-muted" />
          <Command.Input aria-label="Search pages, projects and approvals" placeholder="Search or jump to…" class="h-11 flex-1 bg-transparent text-sm outline-none placeholder:text-fg-muted" />
          
        </div>
        <Command.List class="max-h-80 overflow-auto p-1.5">
          <Command.Viewport>
            <Command.Empty class="px-3 py-8 text-center text-sm text-fg-muted">No matches. Try a project name or an execution id.</Command.Empty>
            {#if attention.length}
              <Command.Group>
                <Command.GroupHeading class={heading}>Needs attention</Command.GroupHeading>
                <Command.GroupItems>
                  {#each attention as a}<Command.Item value={a.label} onSelect={() => pick(a.path)} class={item}><Icon name={a.icon} class="text-fg-muted" /><span class="min-w-0 flex-1 truncate">{a.label}</span><span class="text-xs text-fg-muted">{a.hint}</span></Command.Item>{/each}
                </Command.GroupItems>
              </Command.Group>
            {/if}
            <Command.Group>
              <Command.GroupHeading class={heading}>Go to</Command.GroupHeading>
              <Command.GroupItems>
                {#each PAGES as p}<Command.Item value={p.label} onSelect={() => pick(p.path)} class={item}><Icon name={p.icon} class="text-fg-muted" />{p.label}</Command.Item>{/each}
              </Command.GroupItems>
            </Command.Group>
            {#if projects.data?.items.length}
              <Command.Group>
                <Command.GroupHeading class={heading}>Projects</Command.GroupHeading>
                <Command.GroupItems>
                  {#each projects.data.items as p}<Command.Item value={'project ' + p.name} onSelect={() => pick('/projects/' + p.id)} class={item}><Icon name="folder" class="text-fg-muted" /><span class="flex-1">{p.name}</span>{#if p.active}<span class="text-xs text-fg-muted">Active Context</span>{/if}</Command.Item>{/each}
                </Command.GroupItems>
              </Command.Group>
            {/if}
          </Command.Viewport>
        </Command.List>
        <div class="flex items-center gap-3 border-t border-line px-3 py-2 text-xs text-fg-muted">
          <span class="hidden sm:inline"><kbd class="rounded border border-line-strong px-1">↑</kbd> <kbd class="rounded border border-line-strong px-1">↓</kbd> move</span>
          <span class="hidden sm:inline"><kbd class="rounded border border-line-strong px-1">Enter</kbd> open</span>
          <span><kbd class="rounded border border-line-strong px-1">Esc</kbd> close</span>
          <span class="ml-auto">The palette opens pages. It cannot approve.</span>
        </div>
      </Command.Root>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>
