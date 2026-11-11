<script lang="ts">
  import { toast } from 'svelte-sonner';
  import Icon from '../lib/Icon.svelte';
  import { copyText } from '../lib/copy';
  import { shellQuote } from '../lib/quote';
  const quote = shellQuote;
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { route, href } from '../lib/router.svelte';
  import { ago } from '../lib/format';
  import { ApiError } from '../api/client';
  import Note from '../lib/Note.svelte';
  import type { Proposal } from '../api/models';
  import ProjectHead from '../lib/ProjectHead.svelte';
  import Panel from '../lib/Panel.svelte';
  import Btn from '../lib/Btn.svelte';
  import Badge from '../lib/Badge.svelte';
  import Table from '../lib/Table.svelte';
  import View from '../lib/View.svelte';
  import type { Column } from '../lib/table';

  const a = app();
  const pid = $derived(route.params.id!);
  const res = resource(() => a.api.project(pid), ['project.changed', 'workflow.changed']);
  type Err = Proposal['errors'][number];
  const cols: Column<Err>[] = [
    { key: 'code', label: 'Code', cell: code },
    { key: 'path', label: 'Where', text: (r) => r.path, mono: true },
    { key: 'msg', label: 'Message', text: (r) => r.message },
  ];
  let busy = $state(false);
  let error = $state('');
  async function request() {
    busy = true;
    error = '';
    try {
      await a.api.requestPlanning(pid);
      toast.success('Planning requested');
      await res.reload();
    } catch (x) {
      error = x instanceof ApiError ? `${x.message} ${x.nextStep}` : String(x);
    } finally {
      busy = false;
    }
  }
</script>

{#snippet code(r: Err)}<Badge state="failed" text={r.code} />{/snippet}

<View {res} label="the planning step">
  {#snippet children(p)}
    <ProjectHead id={p.id} current="plan" project={p}>
      {#snippet actions()}<Btn kind="pri" disabled={busy || p.planning.request?.state === 'waiting'} onclick={request}>Request planning</Btn>{/snippet}
    </ProjectHead>
    {#if error}<Note kind="dng" role="alert">{error}</Note>{/if}
    <Panel title="Planning request">
      <div class="pb stack">
        {#if p.planning.request}
          <div class="row wrap"><Badge state={p.planning.request.state === 'waiting' ? 'queued' : p.planning.request.state === 'fetched' ? 'in_progress' : 'completed'} text={p.planning.request.state === 'waiting' ? 'Waiting for a harness' : p.planning.request.state === 'fetched' ? 'Fetched by a harness' : 'Proposal submitted'} /><span class="muted">Planning request <span class="mono">{p.planning.request.id}</span> created {ago(p.planning.request.created_at)}</span>{#if p.planning.request.state === 'waiting'}<span class="dots" role="img" aria-label="Waiting"><i></i><i></i><i></i></span>{/if}</div>
          {#if p.planning.request.state === 'waiting'}
            <div class="muted">Waves never calls an AI provider itself; a harness fetches the request and submits a proposal. Start Claude Code in the project and ask it to plan:</div>
            <pre class="cli">$ cd {quote(p.path)} &amp;&amp; claude
&gt; Fetch the Waves planning request and submit a workflow proposal.</pre>
            <div><Btn sm onclick={() => copyText(`cd ${quote(p.path)} && claude`, 'Copied the command')}><Icon name="copy" size={14} />Copy command</Btn></div>
          {/if}
        {:else}
          <div class="muted">No planning request is open. Request one when the requirements are ready.</div>
        {/if}
      </div>
    </Panel>
    <Panel title="Latest proposal">
      <div class="pb stack">
        {#if p.planning.latest_proposal}
          {@const pr = p.planning.latest_proposal}
          {#if pr.valid}
            <div class="row wrap"><Badge state="passed" text="Proposal valid" /><span class="muted"><span class="mono">{pr.id}</span> · version {pr.version}</span>
              {#if pr.workflow_version_id}<Btn sm href={href('/workflow/' + pr.workflow_version_id)}>Open workflow draft</Btn>{/if}</div>
          {:else}
            <div class="row wrap"><Badge state="failed" text="Proposal invalid" /><span class="muted"><span class="mono">{pr.id}</span> · {pr.errors.length} {pr.errors.length === 1 ? 'error' : 'errors'} · stored unchanged; nothing was fixed silently</span></div>
            <Table label="Proposal errors" rows={pr.errors} columns={cols} rowKey={(r) => r.code + r.path} />
            <div class="muted small">The harness can read these errors through MCP and submit a corrected proposal as a new immutable version.</div>
          {/if}
        {:else}
          <div class="muted">No proposal has been submitted yet.</div>
        {/if}
      </div>
    </Panel>
  {/snippet}
</View>
