<script lang="ts">
  import { toast } from 'svelte-sonner';
  import Modal from '../lib/Modal.svelte';
  import { onMount } from 'svelte';
  import { app } from '../lib/app.svelte';
  import { shellData } from '../lib/shell';
  import { href } from '../lib/router.svelte';
  import { ago } from '../lib/format';
  import { ApiError } from '../api/client';
  import type { Project } from '../api/models';
  import PageHead from '../lib/PageHead.svelte';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Btn from '../lib/Btn.svelte';
  import Badge from '../lib/Badge.svelte';
  import Table from '../lib/Table.svelte';
  import View from '../lib/View.svelte';
  import type { Column, Filter } from '../lib/table';
  import Icon from '../lib/Icon.svelte';

  const a = app();
  const { projects } = shellData();
  let adding = $state(false);
  let path = $state('');
  let name = $state('');
  let error = $state('');

  // "#/projects?register" opens the form, from another page or while already here; the marker is then dropped from the address.
  const openIfAsked = () => {
    if (/[?&]register(&|$)/.test(location.hash)) {
      adding = true;
      history.replaceState(null, '', '#/projects');
    }
  };
  onMount(() => {
    openIfAsked();
    window.addEventListener('hashchange', openIfAsked);
    return () => window.removeEventListener('hashchange', openIfAsked);
  });

  async function register(e: Event) {
    e.preventDefault();
    error = '';
    try {
      await a.api.registerProject(path.trim(), name.trim() || undefined);
      toast.success('Project registered');
      adding = false;
      path = name = '';
      await projects.reload();
    } catch (x) {
      error = x instanceof ApiError ? `${x.message} ${x.nextStep}` : String(x);
    }
  }
  let activateError = $state('');
  async function activate(p: Project) {
    activateError = '';
    try {
      await a.api.activateProject(p.id);
      toast.success(`${p.name} is now the Active Context`);
      await projects.reload();
    } catch (x) {
      activateError = x instanceof ApiError ? `${x.message} ${x.nextStep}` : String(x);
    }
  }

  const kindOf = (r: Project) => (r.failed_executions || r.pending_approvals ? 'attention' : r.running_executions ? 'running' : 'idle');
  const filters: Filter<Project>[] = [
    { id: 'kind', label: 'Show', chips: true, pick: kindOf, options: [{ value: 'attention', label: 'Needs attention' }, { value: 'running', label: 'Running' }, { value: 'idle', label: 'Idle' }] },
  ];
  const cols: Column<Project>[] = [
    { key: 'name', label: 'Project', sort: (r) => r.name, cell: pname },
    { key: 'path', label: 'Path', text: (r) => r.path, mono: true },
    { key: 'branch', label: 'Branch', text: (r) => r.branch ?? 'none' },
    { key: 'git', label: 'Git', text: (r) => (r.is_git ? 'yes' : 'no') },
    { key: 'phase', label: 'Active phase', cell: pphase },
    { key: 'last', label: 'Last activity', sort: (r) => r.last_activity_at ?? '', text: (r) => ago(r.last_activity_at) },
    { key: 'act', label: 'Context', cell: pact },
  ];
</script>

{#snippet pname(r: Project)}<a href={href('/projects/' + r.id)}><b>{r.name}</b></a> {#if r.active}<span class="tag">Active Context</span>{/if}{/snippet}
{#snippet pphase(r: Project)}{#if r.active_phase}<Badge state={r.active_phase.state} text="{r.active_phase.code} {r.active_phase.title}" />{:else}<span class="muted">no workflow yet</span>{/if}{/snippet}
{#snippet pact(r: Project)}{#if !r.active}<Btn sm onclick={() => activate(r)}>Make active</Btn>{/if}{/snippet}

<PageHead title="Projects" sub="Registered projects. Waves never deletes a project folder.">
  {#snippet right()}<Btn kind="pri" onclick={() => (adding = true)}><Icon name="plus" size={14} />Register project</Btn>{/snippet}
</PageHead>
<View res={projects} label="projects">
  {#snippet children(p)}
    {#if activateError}<Note kind="dng" role="alert">{activateError}</Note>{/if}
    <Panel><Table label="Projects" rows={p.items} columns={cols} {filters} search={{ placeholder: 'Filter by name', text: (r) => r.name }} rowKey={(r) => r.id} initialSort={{ key: 'name', dir: 'asc' }}>
      {#snippet empty()}No project is registered yet. Register a folder to start.{/snippet}
    </Table></Panel>
    {#each p.items.filter((x) => !x.is_git) as g}
      <Note kind="neu"><b>{g.name} is not a Git repository.</b> Diffs and unattributed-change detection are limited, and only in-place mode with a workspace lock is available.</Note>
    {/each}
  {/snippet}
</View>

{#if adding}
  <Modal labelledby="reg-title" oncancel={() => (adding = false)}>
    <form onsubmit={register}>
      <div class="mh"><h2 id="reg-title">Register a project</h2></div>
      <div class="mb">
        <div><label class="lbl" for="reg-path">Folder path</label><input id="reg-path" class="field mono w-full" placeholder="~/work/my-project" bind:value={path} required /></div>
        <div><label class="lbl" for="reg-name">Name (optional)</label><input id="reg-name" class="field w-full" bind:value={name} /></div>
        <div class="small muted">The folder is referenced in place. Waves never deletes it.</div>
        {#if error}<Note kind="dng" icon="fail" role="alert">{error}</Note>{/if}
      </div>
      <div class="mf"><Btn onclick={() => (adding = false)}>Cancel</Btn><Btn type="submit" kind="pri" disabled={!path.trim()}>Register</Btn></div>
    </form>
  </Modal>
{/if}
