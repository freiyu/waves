<script lang="ts">
  // Approve and reject controls for one approval. The decision itself is made by
  // the service; this component only gathers the input and the passphrase.
  import { app } from './app.svelte';
  import type { ApprovalDetail } from '../api/models';
  import { shellData } from './shell';
  import { toast } from 'svelte-sonner';
  import Btn from './Btn.svelte';
  import StepUpDialog from './StepUpDialog.svelte';

  interface Props { approval: ApprovalDetail; overrideRequired?: boolean; ondecided: (a: ApprovalDetail) => void }
  let { approval, overrideRequired = false, ondecided }: Props = $props();
  const a = app();
  const sd = shellData();
  const blocked = $derived(sd.status.data?.audit.state === 'broken');

  let mode = $state<null | 'approve' | 'reject'>(null);
  let override = $state('');
  let feedback = $state('');

  const gate = $derived(approval.reject_needs_stepup); // the service says whether rejecting needs the passphrase
  const covered = $derived(!!a.elevationFor(approval));
  const needPass = $derived(mode === 'approve' ? !covered : gate && !covered);
  const canApprove = $derived(approval.allowed_decisions.includes('approve') && a.state.session?.passphrase_set !== false);
  const facts = $derived([
    { label: 'Action', value: approval.action },
    { label: 'Project', value: approval.project_name },
    { label: 'Subject', value: approval.subject },
    ...approval.facts.filter((f) => f.label === 'Phase'),
    { label: 'Requested by', value: approval.requested_by },
    { label: 'Impact', value: approval.impact },
    ...(overrideRequired && mode === 'approve' ? [{ label: 'Override reason', value: override.trim() }] : []),
  ]);

  async function confirm(pass: string) {
    const reject = mode === 'reject';
    const usedPassphrase = needPass; // read before the dialog state is reset
    let secret = a.elevationFor(approval);
    if (!secret && (needPass)) {
      await a.stepUp(pass, approval.id);
      secret = a.elevationFor(approval);
    }
    let res;
    try {
      res = await a.api.decide(
        approval.id,
        reject
          ? { decision: 'reject', subject_hash: approval.subject_hash, feedback: feedback.trim() }
          : { decision: 'approve', subject_hash: approval.subject_hash, override_reason: overrideRequired ? override.trim() : undefined },
        secret,
      );
    } finally {
      // A single-use confirmation covers one attempt, whether it worked or failed (for example on a changed subject).
      if (a.state.elevation?.kind === 'single_use') a.dropElevation();
    }
    const verdict = res.state === 'approved' ? 'approved' : res.state === 'rejected' ? 'rejected' : null;
    toast.success(verdict === 'approved' ? 'Approval recorded' : verdict === 'rejected' ? 'Rejection recorded' : 'Decision recorded', { description: `${approval.category_label}: ${approval.subject}${verdict ? ` was ${verdict}` : ''}${usedPassphrase ? ' with your passphrase' : ''}.` });
    mode = null;
    override = feedback = '';
    ondecided(res);
  }
</script>

{#if approval.state === 'pending'}
  <div class="stack">
    {#if overrideRequired}
      <div>
        <label class="lbl" for="override">Override reason (required to approve with a failed validator)</label>
        <textarea id="override" class="field" bind:value={override}></textarea>
      </div>
    {/if}
    <div class="row wrap">
      <Btn kind="pri" lock disabled={blocked || !canApprove || (overrideRequired && !override.trim())} onclick={() => (mode = 'approve')}>Approve</Btn>
      <Btn lock={gate && !covered} disabled={blocked || !approval.allowed_decisions.includes('reject')} onclick={() => (mode = 'reject')}>Reject with feedback</Btn>
    </div>
    <div class="muted">
      {#if blocked}Approvals are blocked until the audit chain break is acknowledged on the Activity page.
      {:else}{approval.step_up === 'every_time' ? 'Approving and rejecting ask for your passphrase, every time.' : covered ? 'Your unlocked window covers this decision.' : gate ? 'Approving and rejecting ask for your passphrase.' : 'Approving asks for your passphrase. Rejecting does not.'}{/if}
      {#if a.state.session?.passphrase_set === false} The passphrase is not set yet: run waves setup.{/if}
    </div>
  </div>
{/if}

{#if mode}
  <StepUpDialog
    title={mode === 'approve' ? `Approve ${approval.category_label}` : `Reject ${approval.category_label}`}
    kicker={needPass ? 'Approval requires your passphrase' : 'Confirm your decision'}
    {facts}
    confirm={mode === 'approve' ? 'Approve' : 'Reject'}
    danger={mode === 'reject'}
    needPassphrase={needPass}
    nopass={covered ? undefined : 'No passphrase is needed to reject this item.'}
    incomplete={mode === 'reject' && !feedback.trim()}
    onconfirm={confirm}
    oncancel={() => (mode = null)}
  >
    {#snippet extra()}
      {#if mode === 'reject'}
        <div><label class="lbl" for="feedback">Feedback (required)</label><textarea id="feedback" class="field" bind:value={feedback}></textarea></div>
      {/if}
      {#if approval.commands.length}
        <div class="small muted">Commands this approval fixes:</div>
        <pre class="cli">{approval.commands.join('\n')}</pre>
      {/if}
    {/snippet}
  </StepUpDialog>
{/if}
