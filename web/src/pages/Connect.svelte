<script lang="ts">
  import { getContext } from 'svelte';
  import { toast } from 'svelte-sonner';
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { route, href, go } from '../lib/router.svelte';
  import PageHead from '../lib/PageHead.svelte';
  import SectionHead from '../lib/SectionHead.svelte';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Btn from '../lib/Btn.svelte';
  import View from '../lib/View.svelte';
  import StepUpDialog from '../lib/StepUpDialog.svelte';

  const inArea = getContext('area') === true; // inside a project the page head and the way back belong to the project
  const a = app();
  const pid = $derived(route.params.id!);
  const proj = resource(() => a.api.project(route.params.id!));
  const guard = resource(() => a.api.guard(route.params.id!), ['harness.changed', 'guard.denial']);
  const plan = resource(() => a.api.connectPlan(route.params.id!));
  let confirming = $state(false);
</script>

{#if inArea}
  <SectionHead title="Guard" sub="The Claude Code hook of this project. Reconnecting shows the exact edit first and asks for your passphrase." />
{:else}
  <PageHead title="Connect Claude Code to {proj.data?.name ?? '…'}" sub="Review the exact edit first. Nothing changes until you confirm with your passphrase." crumbs={[{ label: 'Harnesses', href: href('/harnesses') }, { label: 'Connect' }]} />
{/if}
<View res={guard} label="the guard status">
  {#snippet children(g)}
    {#if g.drift}
      <Note kind="dng" role="alert"><b>The guard is not active here.</b> {g.drift}. The hook is not what Waves installed. Set the policy again on the <a class="tlink" href={href('/harnesses')}>Harnesses</a> page to write the hook back.</Note>
    {/if}
    {#if g.installed && !g.waiting_hooks}
      <Note kind="neu" icon="prompt"><b>Waiting for instruction is not active here.</b> This project was connected before Waves could tell when Claude Code waits for you. Connect again below to add it.</Note>
    {/if}
    <Panel title="Guard status">
      <div class="pb stack">
        <div>{#if g.installed}<b>Policy: {g.policy}.</b> {g.would_deny_24h} would-deny in the last 24 hours.{:else}<b>Not installed.</b> Nothing is guarding this project yet.{/if}</div>
        <Note kind="neu" icon="warn"><b>What this does not defend.</b> {g.not_defended}</Note>
        <div><a class="tlink" href={href('/harnesses')}>Harnesses</a></div>
      </div>
    </Panel>
  {/snippet}
</View>
<View res={plan} label="the connection plan">
  {#snippet children(p)}
    <Panel title="Edit to {p.file}">
      {#snippet right()}<span class="tag">dry run</span>{/snippet}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex: a scrollable region must be reachable by keyboard -->
      <div class="diff mono wrap" tabindex="0" aria-label="Edit to {p.file}">{#each p.lines as l}<div class={l.startsWith('+') ? 'add' : l.startsWith('-') ? 'del' : ''}>{l}</div>{/each}</div>
    </Panel>
    <Note kind="neu" icon="warn">Merged into the existing file; nothing is overwritten. User-level Claude Code configuration is never touched. The edit is recorded and <b>Disconnect</b> removes exactly these lines.</Note>
    {#if guard.data?.installed}
      <Note kind="neu">Connecting again keeps the policy: <b>{guard.data.policy}</b>.</Note>
    {:else}
      <Note kind="neu">Guard policy after connecting: <b>audit</b>. It records what enforce would deny and blocks nothing. Switch to enforce on the Harnesses page.</Note>
    {/if}
    <div class="row"><Btn kind="pri" lock onclick={() => (confirming = true)}>Confirm and connect</Btn>{#if !inArea}<Btn href={href('/harnesses')}>Cancel</Btn>{/if}</div>
  {/snippet}
</View>
{#if confirming}
  <StepUpDialog
    title="Connect Claude Code"
    kicker="Changing this needs your passphrase"
    facts={[{ label: 'Project', value: proj.data?.name ?? '' }, { label: 'File', value: plan.data?.file ?? '', mono: true }]}
    confirm="Connect"
    needPassphrase={!a.elevationFor()}
    onconfirm={async (p) => {
      await a.runElevated(p, (secret) => a.api.connect(pid, plan.data!.plan_hash, secret));
      confirming = false;
      toast.success(`Claude Code connected to ${proj.data?.name ?? 'the project'}`);
      go(inArea ? '/projects/' + pid + '/guard' : '/harnesses');
    }}
    oncancel={() => (confirming = false)}
  />
{/if}
