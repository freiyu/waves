<script lang="ts">
  // The shared dialog shell (bits-ui Dialog): focus trap, Escape and overlay close, fade and scale in 160 ms.
  // Parents mount it with {#if}; it is open while mounted and calls `oncancel` on Escape or an outside click.
  // While `locked` (a request is in flight) both are ignored, so a dialog never looks cancelled while its action still completes.
  import { onMount, type Snippet } from 'svelte';
  import { Dialog } from 'bits-ui';
  import { returnFocus } from './returnFocus';
  import { hideDone } from './done.svelte';
  let { labelledby, oncancel, locked = false, children }: { labelledby: string; oncancel: () => void; locked?: boolean; children: Snippet } = $props();
  onMount(hideDone); // the last action's success mark must never lie over the next prompt, or look like its answer
</script>

<span hidden use:returnFocus></span>
<Dialog.Root open onOpenChange={(o) => { if (!o) oncancel(); }}>
  <Dialog.Portal>
    <Dialog.Overlay class="overlay fixed inset-0 z-40 bg-(--overlay)" />
    <Dialog.Content
      aria-labelledby={labelledby}
      aria-describedby={undefined}
      onCloseAutoFocus={(e) => e.preventDefault()}
      escapeKeydownBehavior={locked ? 'ignore' : 'close'}
      interactOutsideBehavior={locked ? 'ignore' : 'close'}
      class="pop modal fixed inset-0 z-50 m-auto h-fit max-h-[92dvh] w-[min(520px,calc(100vw-32px))] overflow-auto rounded-[20px] border border-line-strong bg-surface text-fg max-[860px]:inset-x-0 max-[860px]:top-auto max-[860px]:bottom-0 max-[860px]:m-0 max-[860px]:w-full max-[860px]:rounded-b-none"
    >
      {@render children()}
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>
