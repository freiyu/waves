<script lang="ts">
  // One pending approval as a card: what kind, how old, three facts, and the two decisions. The buttons only open the same
  // confirmation as everywhere else (DecideFlow); the card itself decides nothing.
  import { app } from './app.svelte';
  import { resource } from './resource.svelte';
  import { href } from './router.svelte';
  import { ago, plural } from './format';
  import { visible } from './text';
  import type { ApprovalSummary } from '../api/models';
  import Badge from './Badge.svelte';
  import Btn from './Btn.svelte';
  import DiffBar from './DiffBar.svelte';

  interface Props {
    item: ApprovalSummary;
    broken: boolean;
    ondecide: (id: string, mode: 'approve' | 'reject') => void;
    /** On a project page: no project name, and a phase review is decided on its Review pane only. */
    inProject?: boolean;
  }
  let { item, broken, ondecide, inProject = false }: Props = $props();
  const a = app();
  type Tone = 'ok' | 'warn' | 'bad' | 'signal' | 'plain';
  type Line = { tone: Tone; text: string };
  const more = resource(async () => {
    const approval = await a.api.approval(item.id);
    const review = approval.category === 'phase_completion' && approval.phase_id ? await a.api.phaseReview(approval.phase_id) : null;
    let version = null;
    if (approval.category === 'workflow') {
      // The version this approval is about, found by the hash it is bound to: two versions can wait at once.
      const hash = approval.facts.find((f) => f.label === 'Content hash')?.value;
      const waiting = (await a.api.workflow(item.project_id)).versions.find((v) => v.content_hash === hash);
      if (waiting) version = await a.api.workflowVersion(waiting.id);
    }
    return { approval, review, version };
  }, ['approval.changed', 'phase.changed', 'task.changed', 'workflow.changed']);

  const lines = $derived.by((): Line[] => {
    const d = more.data;
    if (!d) return [];
    if (d.review) {
      const r = d.review;
      const done = r.tasks.filter((t) => t.state === 'done').length;
      const passed = r.validators.filter((v) => v.state === 'passed').length;
      const failed = r.validators.some((v) => v.state === 'failed');
      return [
        { tone: done === r.tasks.length ? 'ok' : 'warn', text: `${done} of ${plural(r.tasks.length, 'task')} done` },
        { tone: failed ? 'bad' : passed === r.validators.length ? 'ok' : 'warn', text: `${passed} of ${plural(r.validators.length, 'validator')} passed` },
        { tone: 'plain', text: `${plural(r.diff.files, 'file')}, +${r.diff.added} −${r.diff.removed}` },
        // Never approve from the card without knowing that something changed outside the plan.
        ...(r.findings.length ? [{ tone: 'signal' as const, text: `${plural(r.findings.length, 'change')} outside the plan` }] : []),
      ];
    }
    if (d.approval.category === 'workflow') {
      const out: Line[] = [];
      if (d.version) out.push({ tone: 'plain', text: `${plural(d.version.phases.length, 'phase')}, ${plural(d.version.phases.reduce((n, p) => n + p.tasks.length, 0), 'task')}` });
      if (d.approval.commands.length) out.push({ tone: 'signal', text: `${plural(d.approval.commands.length, 'validator command')} to read` });
      const cancels = d.approval.will_cancel?.length;
      if (cancels) out.push({ tone: 'warn', text: `Cancels ${plural(cancels, 'open task')} of the version in use` });
      return out;
    }
    // Everything else: the first facts the approval carries, in the service's own labels. The dialog shows all of them.
    return d.approval.facts.filter((f) => f.value !== item.subject).slice(0, 3).map((f) => ({ tone: 'plain' as const, text: `${f.label}: ${visible(f.value)}` }));
  });
  const DOT: Record<Tone, string> = { ok: 'bg-st-ok', warn: 'bg-st-warning', bad: 'bg-st-danger', signal: 'bg-st-attention', plain: 'bg-fg-muted' };

  const risky = $derived(item.category === 'risky_action');
  const chip = $derived(item.category === 'phase_completion' ? 'Phase review' : item.category_label);
  const target = $derived(`/projects/${item.project_id}${item.category === 'phase_completion' ? '/review' : item.category === 'workflow' ? '/plan' : ''}`);
  // On the project page a phase review is only linked, so it is read where its validator results are. The Overview offers
  // Approve and Reject for it, as the design and the earlier Overview do; the confirmation still asks every time.
  const reviewOnly = $derived(inProject && item.category === 'phase_completion');
  const allowed = (mode: 'approve' | 'reject') => !broken && (more.data ? more.data.approval.allowed_decisions.includes(mode) : true);
  const subject = $derived(visible(item.subject));
</script>

<article class="panel dcard">
  <div class="flex items-center justify-between gap-3">
    <Badge state={risky ? 'failed' : 'awaiting_approval'} icon={risky ? 'warn' : undefined} text={chip} />
    <span class="text-xs text-fg-muted">{ago(item.requested_at)}</span>
  </div>
  <div>
    <h3 class="dtitle">{#if target.endsWith(item.project_id) && inProject}{subject}{:else}<a class="tlink" href={href(target)}>{subject}</a>{/if}</h3>
    {#if !inProject}<div class="text-xs text-fg-muted">{item.project_name}</div>{/if}
  </div>
  {#if more.error && !more.data}
    <p class="text-[13px] text-st-danger" role="alert">Could not load the details: {more.error.message}</p>
  {:else}
    <div class="dlines" aria-busy={!more.data}>
      {#each lines as l}<p><span class="ddot {DOT[l.tone]}" aria-hidden="true"></span><span class="min-w-0">{l.text}</span></p>{/each}
    </div>
    {#if more.data?.review}<DiffBar added={more.data.review.diff.added} removed={more.data.review.diff.removed} />{/if}
  {/if}
  <div class="mt-auto flex flex-wrap gap-2.5 pt-1">
    {#if reviewOnly}
      <Btn href={href(target)} class="flex-1">Read the review</Btn>
    {:else}
      <Btn kind="pri" lock class="flex-1" label="Approve {subject}" disabled={!allowed('approve')} onclick={() => ondecide(item.id, 'approve')}>Approve</Btn>
      <Btn kind="dng" sm label="Reject {subject}" disabled={!allowed('reject')} onclick={() => ondecide(item.id, 'reject')}>Reject</Btn>
    {/if}
  </div>
</article>
