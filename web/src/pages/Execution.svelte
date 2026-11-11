<script lang="ts">
  import { toast } from 'svelte-sonner';
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { route, href } from '../lib/router.svelte';
  import { clock, diffLines, bytes, plural } from '../lib/format';
  import PageHead from '../lib/PageHead.svelte';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Btn from '../lib/Btn.svelte';
  import Badge from '../lib/Badge.svelte';
  import CopyId from '../lib/CopyId.svelte';
  import Tabs from '../lib/Tabs.svelte';
  import View from '../lib/View.svelte';
  import Confirm from '../lib/Confirm.svelte';
  import LogView from '../lib/LogView.svelte';

  const a = app();
  const id = $derived(route.params.id!);
  const res = resource(() => a.api.execution(route.params.id!), ['execution.changed']);
  let tab = $state('log');
  let interrupting = $state(false);
  $effect(() => {
    void id;
    tab = 'log'; // another execution opens on its log
  });
  const diff = resource(async () => (tab === 'diff' ? a.api.diff(route.params.id!) : null), ['execution.changed']);
  const ctx = resource(async () => (tab === 'context' ? a.api.context(route.params.id!) : null));
  const DIFF_CAP = 5000;
  const LIVE = ['running', 'queued', 'starting', 'waiting_approval', 'paused'];
  const INTERRUPTIBLE = ['running', 'starting', 'waiting_approval', 'queued', 'paused'];
</script>

<View {res} label="this execution">
  {#snippet children(e)}
    <PageHead title={e.task_title} sub="{e.project_name} · {e.task_code} · {e.harness} {e.harness_version ?? ''} · {e.mode === 'harness_driven' ? 'harness-driven' : 'Waves-driven'}" crumbs={[{ label: 'Executions', href: href('/executions') }, { label: e.id }]}>
      {#snippet above()}<Badge state={e.state} /><span class="mono muted">{e.id}</span><CopyId value={e.id} />{/snippet}
      {#snippet right()}
        {#if INTERRUPTIBLE.includes(e.state)}<Btn kind="dng" onclick={() => (interrupting = true)}>Interrupt</Btn>{/if}
        {#if e.state === 'disconnected' || e.state === 'interrupted'}<Btn kind="pri" href={href(`/executions/${e.id}/recovery`)}>Open recovery</Btn>{/if}
      {/snippet}
    </PageHead>
    {#if e.state === 'queued' && e.queued_reason}<Note kind="neu"><b>Queued.</b> {e.queued_reason}</Note>{/if}
    {#if e.state === 'failed' && e.error}<Note kind="dng" icon="fail"><b>Failed.</b> {e.error}</Note>{/if}
    {#if e.state === 'completed' && e.result}<Note kind="ok" icon="ok"><b>Completed.</b> {e.result}</Note>{/if}
    {#if e.state === 'cancelled'}<Note kind="neu">This execution was cancelled. Its log and diff stay available.</Note>{/if}
    <div class="cols">
      <div class="stack">
        <Tabs label="Execution details" bind:active={tab} tabs={[{ id: 'log', label: 'Log' }, { id: 'diff', label: `Diff (${plural(e.diff_files, 'file')})` }, { id: 'context', label: 'Delivered context' }, { id: 'events', label: 'Events' }]}>
          {#snippet children(t)}
            <div class="pt-3">
              {#if t === 'log'}
                {#key id}<LogView {id} live={LIVE.includes(e.state)} />{/key}
              {:else if t === 'diff'}
                <View res={diff} label="the diff">
                  {#snippet children(d)}
                    {#if d !== null}
                      {@const all = diffLines(d)}
                      <!-- svelte-ignore a11y_no_noninteractive_tabindex: a scrollable region must be reachable by keyboard -->
                      <div class="diff mono" tabindex="0" aria-label="Unified diff">{#each all.slice(0, DIFF_CAP) as l}<div class={l.kind}>{l.text}</div>{:else}<div class="muted">No changes were recorded.</div>{/each}</div>
                      {#if all.length > DIFF_CAP}<div class="small muted">Showing the first {DIFF_CAP.toLocaleString('en')} of {all.length.toLocaleString('en')} lines.</div>{/if}
                    {/if}
                  {/snippet}
                </View>
              {:else if t === 'context'}
                <View res={ctx} label="the delivered context">
                  {#snippet children(c)}
                    {#if c !== null}
                      <div class="stack">
                        <div class="small muted">Exactly what the agent was given · {bytes(c.size_bytes)} · {clock(c.generated_at)}</div>
                        {#each c.sections as s}<Panel title={s.title}><pre class="mono px-4 py-3 whitespace-pre-wrap">{s.content}</pre></Panel>{/each}
                      </div>
                    {/if}
                  {/snippet}
                </View>
              {:else}
                <Panel>
                  {#each e.events as ev}<div class="list-row items-baseline"><span class="mono muted">{clock(ev.at)}</span><span class="tag">{ev.kind}</span><span>{ev.message}</span></div>{:else}<div class="empty">No events yet.</div>{/each}
                </Panel>
              {/if}
            </div>
          {/snippet}
        </Tabs>
      </div>
      <div class="stack-lg">
        <Panel title="Status">
          <div class="pb"><dl class="kv grid-cols-[110px_minmax(0,1fr)]">
            <dt>State</dt><dd><Badge state={e.state} /></dd>
            {#if e.lease}<dt>Lease</dt><dd><span class="mono">{e.lease.id}</span> · expires {clock(e.lease.expires_at)}</dd><dt>Heartbeat</dt><dd>every {e.lease.heartbeat_interval_s} s</dd>{/if}
            <dt>Workspace</dt><dd><span class="mono">{e.workspace_path}</span> {e.workspace_locked ? 'locked' : 'free'}</dd>
            {#if e.start_commit}<dt>Start commit</dt><dd class="mono">{e.start_commit}</dd>{/if}
          </dl></div>
        </Panel>
        {#if e.usage}
          <Panel title="Usage"><div class="pb"><dl class="kv grid-cols-[110px_minmax(0,1fr)]">
            <dt>Tokens in</dt><dd>{e.usage.tokens_in.toLocaleString('en')}</dd><dt>Tokens out</dt><dd>{e.usage.tokens_out.toLocaleString('en')}</dd>
            {#if e.usage.cost_usd != null}<dt>Cost</dt><dd>${e.usage.cost_usd.toFixed(2)} <span class="muted small">reported by harness</span></dd>{/if}
          </dl></div></Panel>
        {/if}
        <Panel title="Status history">
          <ul class="timeline">{#each [...e.history].reverse() as h}<li><span class="dot"></span><span class="t"><Badge state={h.state} />{#if h.detail} <span class="small muted">{h.detail}</span>{/if}</span><span class="mono muted small">{clock(h.at)}</span></li>{/each}</ul>
        </Panel>
      </div>
    </div>
    {#if interrupting}
      <Confirm title="Interrupt {e.task_code}?" confirm="Interrupt" danger oncancel={() => (interrupting = false)} onconfirm={async () => { await a.api.interrupt(e.id); interrupting = false; toast.success('Interrupt sent'); await res.reload(); }}>
        <div>{e.task_title} ({e.id}).</div>
        {#if e.state === 'queued'}<div>A queued execution has not started. It is removed from the queue.</div>
        {:else}<div>Waves signals the agent's process group and then checks for processes it left behind. Work already done stays in the workspace and in the diff. The task is not rerun automatically.</div>{/if}
      </Confirm>
    {/if}
  {/snippet}
</View>
