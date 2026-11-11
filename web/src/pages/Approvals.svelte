<script lang="ts">
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { route, href, go } from '../lib/router.svelte';
  import { ago } from '../lib/format';
  import { label } from '../lib/status';
  import type { ApprovalSummary } from '../api/models';
  import PageHead from '../lib/PageHead.svelte';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Btn from '../lib/Btn.svelte';
  import Badge from '../lib/Badge.svelte';
  import Tabs from '../lib/Tabs.svelte';
  import Icon from '../lib/Icon.svelte';
  import View from '../lib/View.svelte';
  import DecisionBox from '../lib/DecisionBox.svelte';

  const a = app();
  const list = resource(() => a.api.approvals(), ['approval.changed']);
  let tab = $state('pending');
  const selectedId = $derived(route.params.id);
  const rows = (items: ApprovalSummary[]) => items.filter((x) => (tab === 'pending') === (x.state === 'pending'));
  const pickId = (items: ApprovalSummary[]) => selectedId ?? rows(items)[0]?.id;
  const detail = resource(async () => {
    // Reads tab and the list first, so the detail follows the tab when no approval is picked in the address.
    const pending = tab === 'pending';
    const id = selectedId ?? list.data?.items.find((x) => (x.state === 'pending') === pending)?.id;
    return id ? a.api.approval(id) : null;
  }, ['approval.changed']);

</script>

<View res={list} label="the approval inbox">
  {#snippet children(l)}
    {@const pendingItems = l.items.filter((x) => x.state === 'pending')}
    {@const pending = pendingItems.length}
    <PageHead title="Approvals" sub="{pending} waiting for you · decisions are recorded with credential type and client" />
    <div class="split">
      <div>
        <Tabs label="Approval inbox" bind:active={tab} tabs={[{ id: 'pending', label: 'Pending', count: pending }, { id: 'decided', label: 'Decided' }]}>
          {#snippet children()}
            {@const shown = rows(l.items)}
            {@const sel = pickId(l.items)}
            <ul class="items card-list" aria-label="Approval inbox">
              {#each shown as r (r.id)}
                <li><a class="li" class:sel={r.id === sel} href={href('/approvals/' + r.id)} aria-current={r.id === sel ? 'true' : undefined}>
                  <div class="t"><b>{r.subject}</b><span class="muted small">{r.project_name} · {ago(r.requested_at)}</span></div>
                  <Badge state={r.state === 'pending' ? 'awaiting_approval' : r.state} text={r.state === 'pending' ? r.category_label : `${r.category_label} · ${label(r.state)}`} />
                </a></li>
              {:else}
                <li class="empty">{tab === 'pending' ? 'Nothing is waiting for a decision.' : 'No decisions yet.'}</li>
              {/each}
            </ul>
          {/snippet}
        </Tabs>
      </div>
      <View res={detail} label="the approval">
        {#snippet children(d)}
          {#if d}
            {@const at = pendingItems.findIndex((x) => x.id === d.id)}
            <section class="panel" aria-label="{d.category_label} · {d.project_name}">
              <div class="ph">
                <Badge state={d.state === 'pending' ? 'awaiting_approval' : d.state} text={d.category_label} /><span class="muted">requested {ago(d.requested_at)}</span><span class="sp"></span>
                {#if at >= 0 && pendingItems.length > 1}
                  <span class="pager" role="group" aria-label="Pending approvals">
                    <a aria-label="Previous approval" href={href('/approvals/' + pendingItems[(at - 1 + pendingItems.length) % pendingItems.length]!.id)}><Icon name="chevl" size={16} /></a>
                    <span class="muted small">{at + 1} / {pendingItems.length}</span>
                    <a aria-label="Next approval" href={href('/approvals/' + pendingItems[(at + 1) % pendingItems.length]!.id)}><Icon name="chevr" size={16} /></a>
                  </span>
                {/if}
              </div>
              <div class="pb stack">
                <h2 class="text-xl">{d.subject}</h2>
                <dl class="kv">
                  <dt>Action</dt><dd>{d.action}</dd><dt>Project</dt><dd>{d.project_name}</dd><dt>Requested by</dt><dd>{d.requested_by}</dd>
                  {#each d.facts as f}<dt>{f.label}</dt><dd class:mono={f.mono}>{f.value}</dd>{/each}
                </dl>
              </div>
              <details class="disc" open><summary>What this approves</summary><div class="pb">{d.impact}</div></details>
              {#if d.commands.length}
                <details class="disc" open><summary>Validators and commands <span class="count">{d.commands.length}</span></summary><div class="pb"><div class="small muted mb-2">Commands this approval fixes:</div><pre class="cli">{d.commands.join('\n')}</pre></div></details>
              {/if}
              <div class="pb stack foot">
                {#if d.state !== 'pending'}
                  <Note kind={d.state === 'approved' ? 'ok' : 'dng'}>Decided: <b>{label(d.state)}</b>{d.decided_at ? ' ' + ago(d.decided_at) : ''}.</Note>
                {:else if d.category === 'phase_completion' && d.phase_id}
                  <Note kind="neu">Phase completion is decided from its review package, so the validator results are in front of you.</Note>
                  <div><Btn kind="pri" href={href(`/phases/${d.phase_id}/review`)}>Open phase review</Btn></div>
                {:else}
                  <DecisionBox approval={d} ondecided={() => { void list.reload(); void detail.reload(); }} />
                {/if}
              </div>
            </section>
          {:else}
            <Panel title="Detail"><div class="empty">Select an approval to see what it asks for.</div></Panel>
          {/if}
        {/snippet}
      </View>
    </div>
  {/snippet}
</View>
