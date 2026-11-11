<script lang="ts">
  import { toast } from 'svelte-sonner';
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { route, href, go } from '../lib/router.svelte';
  import PageHead from '../lib/PageHead.svelte';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Btn from '../lib/Btn.svelte';
  import View from '../lib/View.svelte';
  import StepUpDialog from '../lib/StepUpDialog.svelte';

  const a = app();
  const pid = $derived(route.params.id!);
  const proj = resource(() => a.api.project(route.params.id!));
  const plan = resource(() => a.api.connectPlan(route.params.id!));
  let confirming = $state(false);
</script>

<PageHead title="Connect Claude Code to {proj.data?.name ?? '…'}" sub="Review the exact edit first. Nothing changes until you confirm with your passphrase." crumbs={[{ label: 'Harnesses', href: href('/harnesses') }, { label: 'Connect' }]} />
<View res={plan} label="the connection plan">
  {#snippet children(p)}
    <Panel title="Edit to {p.file}">
      {#snippet right()}<span class="tag">dry run</span>{/snippet}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex: a scrollable region must be reachable by keyboard -->
      <div class="diff mono wrap" tabindex="0" aria-label="Edit to {p.file}">{#each p.lines as l}<div class={l.startsWith('+') ? 'add' : l.startsWith('-') ? 'del' : ''}>{l}</div>{/each}</div>
    </Panel>
    <Note kind="neu" icon="warn">Merged into the existing file; nothing is overwritten. User-level Claude Code configuration is never touched. The edit is recorded and <b>Disconnect</b> removes exactly these lines. The hook lowers the chance of unleased edits; it is not a sandbox.</Note>
    <Note kind="neu">Guard policy after connecting: <b>audit</b>. It records what enforce would deny and blocks nothing. Switch to enforce on the Harnesses page.</Note>
    <div class="row"><Btn kind="pri" lock onclick={() => (confirming = true)}>Confirm and connect</Btn><Btn href={href('/harnesses')}>Cancel</Btn></div>
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
      go('/harnesses');
    }}
    oncancel={() => (confirming = false)}
  />
{/if}
