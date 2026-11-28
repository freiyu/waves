<script lang="ts">
  // The decision of a phase review, on the page, next to the evidence. Same step-up and same API as the dialog elsewhere:
  // the service decides what needs the passphrase and what is allowed. Nothing is sent by pressing Enter.
  import { app } from './app.svelte';
  import { shellData } from './shell';
  import { passphraseAttempt } from './attempt.svelte';
  import { sendDecision } from './decide';
  import { showDone } from './done.svelte';
  import { visible } from './text';
  import { ApiError } from '../api/client';
  import type { ApprovalDetail } from '../api/models';
  import Btn from './Btn.svelte';
  import PassField from './PassField.svelte';

  /** `active` is false while the pane is hidden: a typed passphrase is then thrown away. `outdated`: the last refresh failed, so
   *  nothing is decided on what may be old. */
  let { approval, overrideRequired, active, outdated = false, ondecided, onstale }: { approval: ApprovalDetail; overrideRequired: boolean; active: boolean; outdated?: boolean; ondecided: (result: ApprovalDetail) => void; onstale: () => void } = $props();
  const a = app();
  const sd = shellData();
  const attempt = passphraseAttempt('rv-pass');
  let note = $state('');
  let override = $state('');
  let pass = $state('');
  $effect(() => {
    if (!active) { pass = ''; attempt.reset(); }
  });

  const blocked = $derived(sd.status.data?.audit.state === 'broken');
  const covered = $derived(!!a.elevationFor(approval));
  const rejectNeeds = $derived(approval.reject_needs_stepup && !covered);
  const needField = $derived(!covered && (approval.allowed_decisions.includes('approve') || rejectNeeds));
  const unset = $derived(a.state.session?.passphrase_set === false);
  const stuck = $derived(blocked || outdated || attempt.busy || attempt.wait > 0);
  const canApprove = $derived(!stuck && !unset && approval.allowed_decisions.includes('approve') && (covered || pass !== '') && (!overrideRequired || override.trim() !== ''));
  const canReject = $derived(!stuck && approval.allowed_decisions.includes('reject') && note.trim() !== '' && (!rejectNeeds || pass !== ''));

  async function decide(mode: 'approve' | 'reject') {
    const typed = pass;
    pass = ''; // the passphrase never stays in the page longer than the call
    const ok = await attempt.run(async () => {
      let result: ApprovalDetail;
      try {
        result = await sendDecision(a, approval, { mode, passphrase: typed, needPass: mode === 'approve' ? !covered : rejectNeeds, feedback: note, override: overrideRequired && mode === 'approve' ? override : undefined });
      } catch (x) {
        if (x instanceof ApiError && x.code === 'stale_subject') onstale();
        throw x;
      }
      note = override = '';
      ondecided(result);
    });
    if (ok) showDone(mode === 'approve' ? 'Approved' : 'Rejected', mode === 'reject');
  }
</script>

<form class="flex flex-col gap-4" onsubmit={(e) => e.preventDefault()}>
  <p class="text-[13px] text-fg-muted">{visible(approval.impact)}</p>
  {#if overrideRequired}
    <div><label class="lbl" for="override">Override reason (required to approve with a failed validator)</label><textarea id="override" class="field" bind:value={override}></textarea></div>
  {/if}
  <div><label class="lbl" for="rv-note">Note for the agent (required to reject)</label><textarea id="rv-note" class="field" placeholder="Say what is wrong when you reject." bind:value={note}></textarea></div>
  <PassField {attempt} bind:value={pass} needed={needField} label="Approval passphrase" placeholder={rejectNeeds ? 'Required to approve or reject' : 'Required to approve'} nopass="Your unlocked window already covers this decision." {blocked} />
  <div class="flex flex-wrap gap-2.5">
    <Btn kind="pri" lock class="flex-1" disabled={!canApprove} onclick={() => decide('approve')}>{attempt.busy ? 'Working…' : 'Approve phase'}</Btn>
    <Btn kind="dng" disabled={!canReject} onclick={() => decide('reject')}>Reject</Btn>
  </div>
  <p class="text-xs text-fg-muted">
    {#if blocked}Approvals are blocked until the audit chain break is acknowledged on the Activity page.
    {:else if outdated}The review could not be refreshed. Retry above before you decide.
    {:else}{approval.step_up === 'every_time' ? 'Approving a phase asks for your passphrase every time. ' : ''}After a rejection you pick the tasks to reopen, and the agent reads your note.{/if}
    {#if unset} The passphrase is not set yet: run waves setup.{/if}
  </p>
</form>
