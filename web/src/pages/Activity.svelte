<script lang="ts">
  import { toast } from 'svelte-sonner';
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { clock, ago, plural } from '../lib/format';
  import type { AuditRecord } from '../api/models';
  import PageHead from '../lib/PageHead.svelte';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Btn from '../lib/Btn.svelte';
  import Table from '../lib/Table.svelte';
  import View from '../lib/View.svelte';
  import StepUpDialog from '../lib/StepUpDialog.svelte';
  import type { Column, Filter } from '../lib/table';

  const a = app();
  const res = resource(() => a.api.audit(), ['approval.changed', 'execution.changed', 'task.changed', 'phase.changed', 'audit.alert', 'attention.changed']);
  // Security alerts (a passphrase reset, a restored database, repeated wrong passphrases, bypass attempts) stay listed until a human acknowledges them here.
  const alerts = resource(() => a.api.status(), ['attention.changed', 'audit.alert']);
  const SECURITY = ['passphrase_reset', 'database_restored', 'stepup_failures', 'bypass_attempt'];
  const pending = $derived(alerts.data?.needs_attention.filter((i) => SECURITY.includes(i.kind)) ?? []);
  let ack = $state(false);
  const cols: Column<AuditRecord>[] = [
    { key: 'seq', label: '#', sort: (r) => r.seq, text: (r) => String(r.seq), mono: true },
    { key: 'at', label: 'Time', sort: (r) => r.at, text: (r) => clock(r.at) },
    { key: 'event', label: 'Event', text: (r) => r.event, mono: true },
    { key: 'actor', label: 'Actor', text: (r) => r.actor_scope },
    { key: 'subject', label: 'Subject', text: (r) => r.subject },
    { key: 'detail', label: 'Detail', text: (r) => r.detail },
  ];
  // Event families for the chips; anything else is reachable through All.
  const family = (r: AuditRecord) =>
    /^approval\./.test(r.event) ? 'approvals' : /^guard\./.test(r.event) ? 'guard' : /^(auth|stepup|login_link|session|passphrase)\./.test(r.event) ? 'auth' : /^(execution|task|lease|validation)\./.test(r.event) ? 'executions' : 'other';
  const filters: Filter<AuditRecord>[] = [
    { id: 'family', label: 'Events', chips: true, pick: family, options: [{ value: 'approvals', label: 'Approvals' }, { value: 'guard', label: 'Guard' }, { value: 'auth', label: 'Auth' }, { value: 'executions', label: 'Executions' }] },
    { id: 'actor', label: 'Actor', pick: (r) => r.actor_scope },
  ];
</script>

<PageHead title="Activity" sub="Every change is recorded. Records form a hash chain checked at startup and before each approval." />
<View {res} label="activity">
  {#snippet children(d)}
    {#if d.status.state === 'verified'}
      <Note kind="ok" role="status"><b>Audit chain verified.</b> {plural(d.status.records, 'record')} · last check {ago(d.status.last_check_at)}{d.status.last_checkpoint_at ? ` · last checkpoint ${ago(d.status.last_checkpoint_at)}` : ''}.</Note>
    {:else}
      <Note kind="warn" role="alert"><b>The audit chain did not verify</b> at record {d.status.broken_at_record?.toLocaleString('en')}. Approvals are blocked until a human acknowledges it. The records are never rewritten.
        <div class="mt-2"><Btn lock onclick={() => (ack = true)}>Acknowledge the break</Btn></div></Note>
    {/if}
    {#if pending.length}
      <Note kind="warn" role="alert"><b>{pending.length === 1 ? 'A security alert is open' : `${pending.length} security alerts are open`}.</b>
        <ul class="mt-1 list-disc pl-5">{#each pending as i}<li><b>{i.label}.</b> {i.subject}</li>{/each}</ul>
        <div class="mt-2"><Btn lock onclick={() => (ack = true)}>Acknowledge after checking</Btn></div></Note>
    {/if}
    <Panel title="Latest events">
      <Table label="Audit records" rows={d.items} columns={cols} {filters} rowKey={(r) => String(r.seq)} search={{ placeholder: 'Filter events', text: (r) => `${r.event} ${r.subject} ${r.detail}` }} initialSort={{ key: 'seq', dir: 'desc' }} />
    </Panel>
    {#if d.status.state === 'verified'}
      <Note kind="neu" icon="lock"><b>If verification fails,</b> approvals stop, a banner appears on every page, and only a human with the passphrase can acknowledge the break. The records are never rewritten.</Note>
    {/if}
    {#if ack}
      <StepUpDialog
        title={d.status.state === 'verified' ? 'Acknowledge the security alerts' : 'Acknowledge the audit chain break'}
        kicker="Changing this needs your passphrase"
        confirm="Acknowledge"
        needPassphrase={!a.elevationFor()}
        onconfirm={async (p) => { await a.runElevated(p, (s) => a.api.acknowledgeAudit(s)); ack = false; toast.success(d.status.state === 'verified' ? 'Security alerts acknowledged' : 'Audit chain break acknowledged'); await res.reload(); await alerts.reload(); }}
        oncancel={() => (ack = false)}
      >
        {#snippet extra()}<div>Acknowledging records that you have seen it{d.status.state === 'verified' ? '' : ', so approvals can resume'}. It does not repair or undo anything, and the acknowledgement is itself recorded.</div>{/snippet}
      </StepUpDialog>
    {/if}
  {/snippet}
</View>
