<script lang="ts">
  import { toast } from 'svelte-sonner';
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { ago, bytes } from '../lib/format';
  import { ApiError } from '../api/client';
  import type { Settings } from '../api/models';
  import PageHead from '../lib/PageHead.svelte';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Btn from '../lib/Btn.svelte';
  import View from '../lib/View.svelte';
  import StepUpDialog from '../lib/StepUpDialog.svelte';

  const a = app();
  const res = resource(() => a.api.settings());
  type Key = 'heartbeat_interval_s' | 'log_retention_days' | 'elevation_window_min' | 'max_concurrent';
  let draft = $state<Partial<Record<Key, number>>>({});
  let error = $state('');
  let locked = $state<null | { key: 'elevation_window_min' | 'max_concurrent'; label: string; value: number }>(null);

  const val = (s: Settings, k: Key) => draft[k] ?? s[k];

  const NAME: Record<Key, string> = { heartbeat_interval_s: 'Heartbeat interval', log_retention_days: 'Log retention', elevation_window_min: 'Elevation window', max_concurrent: 'Concurrent executions' };
  async function save(k: Key) {
    error = '';
    try {
      res.set(await a.api.patchSettings({ [k]: draft[k] }));
      delete draft[k];
      toast.success(`${NAME[k]} saved`);
    } catch (x) {
      error = x instanceof ApiError ? `${x.message} ${x.nextStep}` : String(x);
    }
  }
  let backing = $state(false);
  async function doBackup() {
    backing = true;
    try {
      const b = await a.api.backup();
      toast.success(`Backup written, ${bytes(b.bytes)}`);
      await res.reload();
    } catch (x) {
      toast.error(x instanceof ApiError ? x.message : String(x));
    } finally {
      backing = false;
    }
  }
</script>

{#snippet number(s: Settings, k: Key, label: string, unit: string)}
  <label class="sr-only" for="set-{k}">{label}</label>
  <input id="set-{k}" type="number" class="field w-24" value={val(s, k)} oninput={(e) => { const v = e.currentTarget.value; if (v === '') delete draft[k]; else draft[k] = Number(v); }} />
  <span class="muted">{unit}</span>
  <Btn sm disabled={draft[k] === undefined || draft[k] === s[k]} onclick={() => save(k)}>Save</Btn>
{/snippet}

<PageHead title="Settings" sub="Locked items need your approval passphrase. They are never changed from an operator session." />
<View {res} label="settings">
  {#snippet children(s)}
    {#if error}<Note kind="dng" icon="fail" role="alert">{error}</Note>{/if}
    <Panel title="Security">
      <div class="list-row"><div class="lbl-col"><b>Approval passphrase</b><div class="small muted">Set on a terminal. {s.passphrase_set ? 'It is set.' : 'It is not set yet.'} Rotate with <span class="mono">waves auth passphrase</span>.</div></div></div>
      <div class="list-row"><div class="lbl-col"><b>Elevation window</b><div class="small muted">How long an elevation lasts. It covers locked settings and the Workflow, Result and Risky action approvals. Phase gates always ask.</div></div>
        <span class="value">{s.elevation_window_min} min</span>
        <Btn sm lock onclick={() => (locked = { key: 'elevation_window_min', label: 'Elevation window (minutes)', value: s.elevation_window_min })}>Edit with passphrase</Btn></div>
      <div class="list-row"><div class="lbl-col"><b>Network</b><div class="small muted">Listening on <span class="mono">{s.listen}</span>. LAN access is {s.lan_enabled ? 'on' : 'off'}.</div></div></div>
    </Panel>
    <Panel title="Execution">
      <div class="list-row"><div class="lbl-col"><b>Concurrent executions</b><div class="small muted">Global limit {s.max_concurrent} of hard cap {s.hard_cap}. One code-changing execution per workspace.</div></div>
        <span class="value">{s.max_concurrent}</span>
        <Btn sm lock onclick={() => (locked = { key: 'max_concurrent', label: `Concurrent executions (1 to ${s.hard_cap})`, value: s.max_concurrent })}>Edit with passphrase</Btn></div>
      <div class="list-row"><div class="lbl-col"><b>Heartbeat interval</b><div class="small muted">Agents report every {s.heartbeat_interval_s} s. Missed beats move an execution to Disconnected.</div></div>{@render number(s, 'heartbeat_interval_s', 'Heartbeat interval', 's')}</div>
    </Panel>
    <Panel title="Data">
      <div class="list-row"><div class="lbl-col"><b>Log retention</b><div class="small muted">Execution logs are kept {s.log_retention_days} days, {s.log_max_mb} MB each. Audit records are kept.</div></div>{@render number(s, 'log_retention_days', 'Log retention', 'days')}</div>
      <div class="list-row"><div class="lbl-col"><b>Backup</b><div class="small muted">{s.last_backup_at ? `Last backup ${ago(s.last_backup_at)} · ${bytes(s.last_backup_bytes ?? 0)}` : 'No backup yet.'}</div></div><Btn disabled={backing} onclick={doBackup}>{backing ? 'Backing up…' : 'Back up now'}</Btn></div>
      <div class="list-row"><div class="lbl-col"><b>Restore</b><div class="small muted">Offline only. Stop the service, then run <span class="mono">waves restore</span> in a terminal.</div></div><span class="muted small">Not available here</span></div>
    </Panel>
    {#if locked}
      {@const l = locked}
      <StepUpDialog
        title={'Change ' + l.label.split(' (')[0]!.toLowerCase()}
        kicker="Changing this needs your passphrase"
        confirm="Save"
        incomplete={!Number.isFinite(draft[l.key] ?? l.value)}
        needPassphrase={!a.elevationFor()}
        onconfirm={async (p) => {
          const key = l.key; // `l` follows `locked`, which is cleared below
          res.set(await a.runElevated(p, (secret) => a.api.patchSettings({ [key]: draft[key] ?? l.value }, secret)));
          delete draft[key];
          locked = null;
          toast.success(`${NAME[key]} saved`);
        }}
        oncancel={() => { delete draft[l.key]; locked = null; }}
      >
        {#snippet extra()}
          <div><label class="lbl" for="lk-val">{l.label}</label><input id="lk-val" class="field w-32" type="number" value={draft[l.key] ?? l.value} oninput={(e) => (draft[l.key] = e.currentTarget.value === '' ? NaN : Number(e.currentTarget.value))} /></div>
        {/snippet}
      </StepUpDialog>
    {/if}
  {/snippet}
</View>
