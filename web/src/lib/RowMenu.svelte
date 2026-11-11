<script lang="ts">
  // The "..." menu at the end of a list row. Items only navigate or copy; nothing here decides anything.
  import { DropdownMenu } from 'bits-ui';
  import Icon, { type IconName } from './Icon.svelte';
  let { label, items }: { label: string; items: Array<{ label: string; icon: IconName; run: () => void }> } = $props();
  import { menuItem as mi, menuPop } from './ui';
</script>

<DropdownMenu.Root>
  <DropdownMenu.Trigger aria-label={label} class="inline-flex size-7 cursor-pointer items-center justify-center rounded-md text-fg-muted hover:bg-muted"><Icon name="more" size={16} /></DropdownMenu.Trigger>
  <DropdownMenu.Portal>
    <DropdownMenu.Content align="end" sideOffset={4} class={menuPop}>
      {#each items as it}<DropdownMenu.Item class={mi} onSelect={it.run}><Icon name={it.icon} class="text-fg-muted" />{it.label}</DropdownMenu.Item>{/each}
    </DropdownMenu.Content>
  </DropdownMenu.Portal>
</DropdownMenu.Root>
