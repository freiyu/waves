<script lang="ts">
  import { toast } from 'svelte-sonner';
  import Icon from '../lib/Icon.svelte';
  import { copyText } from '../lib/copy';
  import { shellQuote } from '../lib/quote';
  const quote = shellQuote;
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { route } from '../lib/router.svelte';
  import { ago } from '../lib/format';
  import { ApiError } from '../api/client';
  import Note from '../lib/Note.svelte';
  import type { Proposal, Validator, WorkflowTask } from '../api/models';
  import { visible } from '../lib/text';
  import SectionHead from '../lib/SectionHead.svelte';
  import ApprovalCard from '../lib/ApprovalCard.svelte';
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
    { key: 'path', label: 'Where', text: (r) => visible(r.path), mono: true },
    { key: 'msg', label: 'Message', text: (r) => visible(r.message) },
  ];
  const taskCols: Column<WorkflowTask>[] = [
    { key: 'code', label: 'Task', cell: tcode },
    { key: 'title', label: 'Title', text: (r) => visible(r.title) },
    { key: 'mode', label: 'Mode', text: (r) => (r.mode.charAt(0).toUpperCase() + r.mode.slice(1)).replace('_', ' ') },
    { key: 'acc', label: 'Acceptance', text: (r) => `${r.acceptance.length} ${r.acceptance.length === 1 ? 'criterion' : 'criteria'}` },
  ];
  const valCols: Column<Validator>[] = [
    { key: 'kind', label: 'Type', text: (r) => visible(r.kind) },
    { key: 'name', label: 'Name', text: (r) => visible(r.name) },
    { key: 'cmd', label: 'Exact command or items', cell: vcmd },
    { key: 'to', label: 'Timeout', text: (r) => (r.timeout_s ? `${r.timeout_s} s` : 'none') },
  ];
  // The version shown: the one the human picked, else a draft, else one waiting for a decision, else the approved one, else the newest.
  const wf = resource(() => a.api.workflow(pid), ['workflow.changed']);
  const short = (h: string) => (h.length > 16 ? `${h.slice(0, 7)}…${h.slice(-4)}` : h);
  let picked = $state('');
  const versionId = $derived.by(() => {
    const vs = wf.data?.versions ?? [];
    return vs.find((v) => v.id === picked)?.id ?? (vs.find((v) => v.state === 'draft') ?? vs.find((v) => v.state === 'awaiting_approval') ?? vs.find((v) => v.id === wf.data?.current_id) ?? vs[0])?.id ?? '';
  });
  const ver = resource(async () => (versionId ? a.api.workflowVersion(versionId) : null), ['workflow.changed']);
  async function submit(id: string) {
    error = '';
    try {
      await a.api.submitWorkflow(id);
      toast.success('Workflow submitted for approval');
      await Promise.all([res.reload(), wf.reload(), ver.reload()]);
    } catch (x) {
      error = x instanceof ApiError ? `${x.message} ${x.nextStep}` : String(x);
    }
  }
  async function revise(id: string) {
    error = '';
    try {
      const v = await a.api.newWorkflowVersion(id);
      toast.success('New workflow version created');
      picked = v.id;
      await Promise.all([wf.reload(), res.reload()]);
    } catch (x) {
      error = x instanceof ApiError ? `${x.message} ${x.nextStep}` : String(x);
    }
  }
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
{#snippet tcode(r: WorkflowTask)}<span class="mono">{r.code}</span>{/snippet}
{#snippet vcmd(r: Validator)}{#if r.command}<span class="mono">{visible(r.command)}</span>{:else}{visible(r.items?.join('; ') ?? '')}{/if}{/snippet}

<View {res} label="the planning step">
  {#snippet children(p)}
    <ProjectHead id={p.id} current="plan" project={p} />
    {#if error}<Note kind="dng" role="alert">{error}</Note>{/if}
    <Panel title="Planning request">
      {#snippet right()}<Btn kind="pri" sm disabled={busy || p.planning.request?.state === 'waiting'} onclick={request}>Request planning</Btn>{/snippet}
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
            <div class="row wrap"><Badge state="passed" text="Proposal valid" /><span class="muted"><span class="mono">{pr.id}</span> · version {pr.version} · it becomes a draft version under Workflow</span></div>
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
    <View res={wf} label="the workflow versions">
      {#snippet children(w)}
        {#if !w.versions.length}
          <Note kind="neu">This project has no workflow version yet. Request planning above and a harness will submit a proposal.</Note>
        {:else}
          <View res={ver} label="the workflow version">
            {#snippet children(v)}
              {#if v}
                {@const waiting = p.pending_approval_items.find((x) => x.category === 'workflow')}
                {@const open = v.state === 'draft' || v.state === 'awaiting_approval'}
                <SectionHead title="Workflow · version {v.number}" sub="hash {short(v.content_hash)}{v.proposal_id ? ' · from proposal ' + v.proposal_id : ''}">
                  {#snippet right()}
                    <Badge state={v.state} />
                    {#if v.state === 'draft'}<Btn kind="pri" onclick={() => submit(v.id)}>Submit</Btn>{/if}
                    {#if v.state === 'approved' || v.state === 'rejected'}<Btn onclick={() => revise(v.id)}>Revise</Btn>{/if}
                  {/snippet}
                </SectionHead>
                {#if v.state === 'awaiting_approval' && waiting}<ApprovalCard id={waiting.id} commands={false} />{/if}
                {#if open}<Note kind="warn"><b>Waves will run these commands</b> on your machine when the last task of a phase is Done, before you review that phase, so they also run on what the agents wrote. Approving this workflow approves exactly the strings below. Changing one creates a new version.</Note>{/if}
                {#each v.phases as ph (ph.id)}
                  <Panel title="{ph.code} {ph.title}" level={3}>
                    <div class="pb stack">
                      <div class="muted">{visible(ph.objective)}</div>
                      <h4>Tasks <span class="count">{ph.tasks.length}</span></h4>
                      <div class="boxed"><Table label="{ph.code} tasks" rows={ph.tasks} columns={taskCols} rowKey={(r) => r.code} /></div>
                      <h4>Validators <span class="count">{ph.validators.length}</span></h4>
                      <div class="boxed"><Table label="{ph.code} validators" rows={ph.validators} columns={valCols} rowKey={(r) => r.id} /></div>
                    </div>
                  </Panel>
                {:else}<Note kind="neu">This version has no phases.</Note>{/each}
                {#if w.versions.length > 1}
                  <Panel title="Versions" level={3}>
                    {#each w.versions as x (x.id)}
                      <div class="list-row"><button type="button" class="tlink" aria-current={x.id === v.id ? 'true' : undefined} onclick={() => (picked = x.id)}>Version {x.number}</button><Badge state={x.state} /><span class="mono muted small">{short(x.content_hash)}</span>{#if x.id === w.current_id}<span class="tag">in use</span>{/if}</div>
                    {/each}
                  </Panel>
                {/if}
              {/if}
            {/snippet}
          </View>
        {/if}
      {/snippet}
    </View>
  {/snippet}
</View>
