<script lang="ts">
  // Copy button with a tooltip and a toast. Ids are not secrets; nothing else is ever copied from here.
  import { Tooltip } from 'bits-ui';
  import { copyText } from './copy';
  import Icon from './Icon.svelte';
  let { value, label = 'Copy id' }: { value: string; label?: string } = $props();
  const copy = () => copyText(value);
</script>

<Tooltip.Provider delayDuration={300}>
  <Tooltip.Root>
    <Tooltip.Trigger onclick={copy} aria-label="{label} {value}" class="inline-flex size-7 cursor-pointer items-center justify-center rounded-md border border-line bg-surface text-fg-muted hover:bg-muted">
      <Icon name="copy" size={14} />
    </Tooltip.Trigger>
    <Tooltip.Portal>
      <Tooltip.Content sideOffset={6} class="tip z-50 rounded-md bg-fg px-2 py-1 text-xs font-semibold text-bg">{label}</Tooltip.Content>
    </Tooltip.Portal>
  </Tooltip.Root>
</Tooltip.Provider>
