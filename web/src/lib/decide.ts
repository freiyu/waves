// Sending one decision, the same way from the confirmation dialog and from the form on the phase review: step up with the
// passphrase only when no held elevation covers the approval, send, and forget a single-use confirmation afterwards.
import { toast } from 'svelte-sonner';
import type { App } from './app.svelte';
import type { ApprovalDetail } from '../api/models';

export interface Decision {
  mode: 'approve' | 'reject';
  passphrase: string;
  needPass: boolean;
  feedback: string;
  /** Sent with an approval only, when the review says it needs a written reason. */
  override?: string;
}

export async function sendDecision(a: App, approval: ApprovalDetail, d: Decision): Promise<ApprovalDetail> {
  const reject = d.mode === 'reject';
  let secret = a.elevationFor(approval);
  if (!secret && d.needPass) {
    await a.stepUp(d.passphrase, approval.id);
    secret = a.elevationFor(approval);
  }
  let res;
  try {
    res = await a.api.decide(
      approval.id,
      reject
        ? { decision: 'reject', subject_hash: approval.subject_hash, feedback: d.feedback.trim() }
        : { decision: 'approve', subject_hash: approval.subject_hash, override_reason: d.override?.trim() || undefined },
      secret,
    );
  } finally {
    // A single-use confirmation covers one attempt, whether it worked or failed (for example on a changed subject).
    if (a.state.elevation?.kind === 'single_use') a.dropElevation();
  }
  const verdict = res.state === 'approved' ? 'approved' : res.state === 'rejected' ? 'rejected' : null;
  toast.success(verdict === 'approved' ? 'Approval recorded' : verdict === 'rejected' ? 'Rejection recorded' : 'Decision recorded', { description: `${approval.category_label}: ${approval.subject}${verdict ? ` was ${verdict}` : ''}${d.needPass ? ' with your passphrase' : ''}.` });
  return res;
}
