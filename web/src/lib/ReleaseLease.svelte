<script lang="ts">
  // The one place that words and runs "Release lease": the human ends a hold that a waiting harness keeps on a task and its
  // workspace. The execution ends as Interrupted and nothing is rerun; what happens next is the human's choice.
  import { toast } from 'svelte-sonner';
  import { app } from './app.svelte';
  import Confirm from './Confirm.svelte';

  let { target, oncancel, ondone }: { target: { id: string; title: string; code: string }; oncancel: () => void; ondone: () => void } = $props();
  const a = app();
</script>

<Confirm
  title="Release lease on task {target.code}?"
  confirm="Release"
  kind="info"
  {oncancel}
  onconfirm={async () => {
    await a.api.interrupt(target.id);
    toast.success('Lease released', { description: `${target.title} is Interrupted. Waves does not rerun it.` });
    ondone();
  }}
>
  <p>The harness stops holding this task and its workspace lock, and the execution ends as Interrupted. The Claude Code session stays open but can no longer work on this task. Waves does not rerun the task; you choose what happens next.</p>
</Confirm>
