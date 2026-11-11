<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon, { type IconName } from './Icon.svelte';
  let { kind = 'neu', icon, role, children }: { kind?: 'warn' | 'dng' | 'info' | 'neu' | 'att' | 'ok'; icon?: IconName; role?: 'alert' | 'status'; children: Snippet } = $props();
  // The default icon follows the kind, so the warning triangle keeps its meaning (Blocked, Interrupted, Disconnected).
  const DEFAULT: Record<string, IconName> = { warn: 'warn', dng: 'fail', ok: 'ok', att: 'wait', neu: 'neu', info: 'neu' };
</script>

<div class="note {kind}" {role}><Icon name={icon ?? DEFAULT[kind]!} /><div>{@render children()}</div></div>
