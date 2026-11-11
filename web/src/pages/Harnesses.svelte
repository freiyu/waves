<script lang="ts">
  import { toast } from 'svelte-sonner';
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { shellData } from '../lib/shell';
  import { href } from '../lib/router.svelte';
  import { shortClock } from '../lib/format';
  import type { GuardDecision, Harness } from '../api/models';
  import PageHead from '../lib/PageHead.svelte';
  import Tabs from '../lib/Tabs.svelte';
  import { reasonText } from '../lib/guardReasons';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Btn from '../lib/Btn.svelte';
  import Badge from '../lib/Badge.svelte';
  import Table from '../lib/Table.svelte';
  import View from '../lib/View.svelte';
  import StepUpDialog from '../lib/StepUpDialog.svelte';
  import type { Column } from '../lib/table';

  const a = app();
  const { projects } = shellData();
  let chosen = $state<string | undefined>();
  const proj = $derived(projects.data?.items.find((p) => p.id === chosen) ?? projects.data?.items.find((p) => p.active) ?? projects.data?.items[0]);
  const pid = $derived(proj?.id);
  const harnesses = resource(() => a.api.harnesses(), ['harness.changed']);
  const guard = resource(async () => (pid ? a.api.guard(pid) : null), ['harness.changed', 'guard.denial']);
  const decisions = resource(async () => (pid ? a.api.guardDecisions(pid) : null), ['guard.denial', 'harness.changed']);

  let tab = $state('guard');
  type Dlg = null | 'policy' | 'unmanaged' | 'disconnect';
  let dlg = $state<Dlg>(null);
  let policy = $state<'off' | 'audit' | 'enforce'>('audit');
  let unmanaged = $state<'allow' | 'deny'>('allow');

  const POLICIES: Array<{ id: 'off' | 'audit' | 'enforce'; text: string }> = [
    { id: 'off', text: 'No hook installed. Nothing is checked or recorded.' },
    { id: 'audit', text: 'Hook allows every call and records the ones it would have denied. Default.' },
    { id: 'enforce', text: "Denies file edits outside a session's lease, and file edits and Bash from a session whose lease ended. A session that never claimed a task follows the Unmanaged sessions setting. Bash from a session with an active lease is not checked. Denies on any hook error or timeout." },
  ];
  // What the hook does for the current policy, in words that match the guard: a session that never claimed a task is allowed by default.
  const current = (policy: 'off' | 'audit' | 'enforce', unmanagedSessions: 'allow' | 'deny') =>
    policy === 'off' ? 'Nothing is checked or recorded.'
    : policy === 'audit' ? 'It records what enforce would deny. It blocks nothing.'
    : unmanagedSessions === 'allow'
      ? "It denies file edits outside a session's lease, and file edits and Bash from a session whose lease ended. A claude session that never claimed a task is allowed and logged as a guard decision. Bash from a session with an active lease is not checked."
      : "It denies file edits outside a session's lease, and file edits and Bash from a session that never claimed a task. Bash from a session with an active lease is not checked.";
  const capCols: Column<Harness['capabilities'][number]>[] = [
    { key: 'name', label: 'Capability', text: (r) => r.name, mono: true },
    { key: 'val', label: 'Claude Code', cell: capv },
    { key: 'note', label: 'Note', text: (r) => r.note },
  ];
  const decCols: Column<GuardDecision>[] = [
    { key: 'at', label: 'Time', sort: (r) => r.at, text: (r) => shortClock(r.at) },
    { key: 'tool', label: 'Tool', text: (r) => r.tool },
    { key: 'target', label: 'Target', text: (r) => r.target, mono: true },
    { key: 'dec', label: 'Decision', text: (r) => (r.decision === 'allow' ? 'Allowed' : 'Denied') },
    { key: 'wd', label: 'Would deny', text: (r) => (r.would_deny ? `yes · ${reasonText(r.reason)}` : `no · ${reasonText(r.reason)}`) },
  ];
  const openPolicy = (g: { policy: typeof policy; unmanaged_sessions: typeof unmanaged }) => {
    policy = g.policy;
    unmanaged = g.unmanaged_sessions;
    dlg = 'policy';
  };
</script>

{#snippet capv(r: Harness['capabilities'][number])}<Badge state={r.value} />{/snippet}

<PageHead title="Harnesses" sub="Installed, connected, and available for execution are separate things.">
  {#snippet right()}
    {#if (projects.data?.items.length ?? 0) > 1}
      <label class="small muted" for="hp">Project</label>
      <select id="hp" class="field" value={pid} onchange={(e) => (chosen = e.currentTarget.value)}>{#each projects.data?.items ?? [] as p}<option value={p.id}>{p.name}</option>{/each}</select>
    {/if}
  {/snippet}
</PageHead>
<View res={harnesses} label="harnesses">
  {#snippet children(h)}
    {#each h.items as hn (hn.id)}
      {@const connected = !!proj && hn.connected_projects.includes(proj.name)}
      <Panel title={hn.name}>
        <div class="pb row wrap gap-6">
          <Badge state={hn.installed ? 'connected' : 'not_connected'} text={hn.installed ? `Installed ${hn.version ?? ''}` : 'Not installed'} />
          <Badge state={connected ? 'connected' : 'not_connected'} text={connected ? `Connected to ${proj?.name}` : `Not connected to ${proj?.name ?? 'a project'}`} />
          <Badge state={hn.available_for_execution ? 'connected' : 'not_connected'} text={hn.available_for_execution ? 'Available for execution' : 'Not available for execution'} />
          <span class="sp"></span>
          {#if proj}
            {#if connected}<Btn lock onclick={() => (dlg = 'disconnect')}>Disconnect</Btn>{:else}<Btn kind="pri" href={href('/harnesses/connect/' + proj.id)}>Connect to {proj.name}</Btn>{/if}
          {/if}
        </div>
      </Panel>
      <Tabs label="Harness details" bind:active={tab} tabs={[{ id: 'guard', label: 'Guard' }, { id: 'capabilities', label: 'Capabilities', count: hn.capabilities.length }, { id: 'decisions', label: 'Decisions', count: decisions.data?.items.length }]}>
        {#snippet children(t)}
          <div class="stack-lg pt-4">
            {#if t === 'guard'}
      <View res={guard} label="the guard policy">
        {#snippet children(g)}
          {#if g}
            {#if g.drift}
              <Note kind="dng" role="alert"><b>The guard is not active here.</b> {g.drift}. The hook is not what Waves installed. Fix the file, then set the policy again to write the hook back.</Note>
            {/if}
            <Panel title="Guard policy · {proj?.name}">
              {#snippet right()}<Btn sm lock onclick={() => openPolicy(g)}>Change policy</Btn>{/snippet}
              <div class="pb stack">
                <div><b>Current policy: {g.policy}.</b> {current(g.policy, g.unmanaged_sessions)}</div>
                <Note kind="neu" icon="warn"><b>What this does not defend.</b> {g.not_defended}</Note>
                <div class="grid3" role="list" aria-label="Guard policies">
                  {#each POLICIES as p}
                    <div role="listitem" class="panel" style="padding:12px 14px;{g.policy === p.id ? 'border-color:var(--primary);box-shadow:inset 0 0 0 1px var(--primary)' : ''}">
                      <div class="row"><b>{p.id}</b>{#if g.policy === p.id}<span class="tag">current</span>{/if}</div>
                      <div class="muted mt-1.5">{p.text}</div>
                    </div>
                  {/each}
                </div>
              </div>
            </Panel>
          {/if}
        {/snippet}
      </View>
    {#if guard.data}
      <Panel title="Unmanaged sessions">
        <div class="pb row wrap">
          {#if guard.data.policy === 'enforce'}
            <span>A <span class="mono">claude</span> session that never claimed a task is currently: <b>{guard.data.unmanaged_sessions === 'allow' ? 'allowed and logged' : 'denied'}</b>.</span>
            <span class="sp"></span><span class="muted">Changing this needs your passphrase.</span>
            <Btn sm lock onclick={() => { unmanaged = guard.data!.unmanaged_sessions; policy = guard.data!.policy; dlg = 'unmanaged'; }}>Change</Btn>
          {:else}
            <span class="muted">While the policy is {guard.data.policy} nothing is blocked, whatever this setting says.</span>
          {/if}
        </div>
      </Panel>
    {/if}
            {:else if t === 'capabilities'}
              <Panel title="Capabilities (observed)"><Table label="Capabilities" rows={hn.capabilities} columns={capCols} rowKey={(r) => r.name} /></Panel>
            {:else}
    <View res={decisions} label="guard decisions">
      {#snippet children(d)}
        <Panel title="Recent guard decisions · {guard.data?.would_deny_24h ?? 0} would-deny in 24 h">
          <Table label="Guard decisions" rows={d?.items ?? []} columns={decCols} rowKey={(r) => r.at + r.tool + r.target} initialSort={{ key: 'at', dir: 'desc' }}>
            {#snippet empty()}No decisions recorded for this project yet.{/snippet}
          </Table>
        </Panel>
      {/snippet}
    </View>
            {/if}
          </div>
        {/snippet}
      </Tabs>
    {:else}
      <Note kind="neu">No harness was found on this machine. Install Claude Code, then run waves doctor.</Note>
    {/each}
  {/snippet}
</View>

{#if dlg && pid}
  <StepUpDialog
    title={dlg === 'policy' ? 'Change the guard policy' : dlg === 'unmanaged' ? 'Unmanaged sessions' : 'Disconnect Claude Code'}
    kicker="Changing this needs your passphrase"
    facts={dlg === 'disconnect' ? [{ label: 'Project', value: proj?.name ?? '' }, { label: 'Guard policy', value: 'becomes off' }] : []}
    confirm={dlg === 'disconnect' ? 'Disconnect' : 'Save'}
    danger={dlg === 'disconnect'}
    needPassphrase={!a.elevationFor()}
    onconfirm={async (p) => {
      await a.runElevated(p, async (secret) => {
        if (dlg === 'policy') await a.api.patchProjectSettings(pid, { guard_policy: policy }, secret);
        else if (dlg === 'unmanaged') await a.api.patchProjectSettings(pid, { unmanaged_sessions: unmanaged }, secret);
        else await a.api.disconnect(pid, secret);
      });
      toast.success(dlg === 'policy' ? `Guard policy set to ${policy}` : dlg === 'unmanaged' ? `Unmanaged sessions: ${unmanaged === 'allow' ? 'allowed and logged' : 'denied'}` : 'Harness disconnected');
      dlg = null;
      await Promise.all([guard.reload(), projects.reload(), harnesses.reload()]);
    }}
    oncancel={() => (dlg = null)}
  >
    {#snippet extra()}
      {#if dlg === 'policy'}
        <fieldset class="stack border-0 p-0"><legend class="lbl">Policy</legend>
          {#each POLICIES as p}<label class="row items-start"><input type="radio" bind:group={policy} value={p.id} /><span><b>{p.id}</b><br /><span class="muted">{p.text}</span></span></label>{/each}
        </fieldset>
        {#if policy === 'enforce'}<Note kind="warn"><b>enforce</b> blocks file edits outside a session's lease. A session that never claimed a task stays allowed unless you deny it under Unmanaged sessions. It is still not a sandbox; Bash from a session with an active lease is not checked.</Note>{/if}
      {:else if dlg === 'unmanaged'}
        <fieldset class="stack border-0 p-0"><legend class="lbl">A session that never claimed a task</legend>
          <label class="row"><input type="radio" bind:group={unmanaged} value="allow" /> Allow, audited</label>
          <label class="row"><input type="radio" bind:group={unmanaged} value="deny" /> Deny</label>
        </fieldset>
      {:else}
        <div>Removes exactly the lines Waves added to this project's Claude Code settings. User-level configuration is never touched.</div>
      {/if}
    {/snippet}
  </StepUpDialog>
{/if}
