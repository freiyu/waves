<script lang="ts">
  // Approve or Reject straight from an icon: loads the approval and, for a phase completion, its review (to know whether an
  // override reason is needed) in ONE request chain, and opens the same step-up confirmation the review page uses only when both are
  // known. If anything is missing, or the approval cannot be decided now, the flow says so and closes: it never opens a form that
  // could send a decision the service has to refuse. The service still decides everything; there is no shortcut here.
  import { toast } from 'svelte-sonner';
  import { app } from './app.svelte';
  import { resource } from './resource.svelte';
  import { shellData } from './shell';
  import DecisionBox from './DecisionBox.svelte';
  let { approvalId, mode, ondone, oncancel }: { approvalId: string; mode: 'approve' | 'reject'; ondone: () => void; oncancel: () => void } = $props();
  const a = app();
  const sd = shellData();
  const loaded = resource(async () => {
    const approval = await a.api.approval(approvalId);
    const review = approval.category === 'phase_completion' && approval.phase_id ? await a.api.phaseReview(approval.phase_id) : null;
    return { approval, review };
  });
  const why = $derived.by(() => {
    const d = loaded.data?.approval;
    if (!d) return '';
    if (sd.status.data?.audit.state === 'broken') return 'Approvals are blocked until the audit chain break is acknowledged on the Activity page.';
    if (d.state !== 'pending') return 'This approval is already decided.';
    if (!d.allowed_decisions.includes(mode)) return `This approval cannot be ${mode === 'approve' ? 'approved' : 'rejected'} now.`;
    if (mode === 'approve' && a.state.session?.passphrase_set === false) return 'The passphrase is not set yet: run waves setup.';
    return '';
  });
  let closed = false;
  $effect(() => {
    if (closed) return;
    if (loaded.error) {
      closed = true;
      toast.error('Could not load this approval', { description: `${loaded.error.message} ${loaded.error.nextStep}`.trim() });
      oncancel();
    } else if (why) {
      closed = true;
      toast.error('Nothing to decide', { description: why });
      oncancel();
    }
  });
</script>

{#if loaded.data && !why}
  <DecisionBox approval={loaded.data.approval} start={mode} overrideRequired={loaded.data.review?.requires_override_reason ?? false} {oncancel} ondecided={() => ondone()} />
{/if}
