<script lang="ts">
  // The one place that words and runs "Retry" for a stopped execution. Waves never does this on its own.
  import { toast } from 'svelte-sonner';
  import { app } from './app.svelte';
  import Confirm from './Confirm.svelte';

  let { target, oncancel, ondone }: { target: { id: string; title: string; code: string }; oncancel: () => void; ondone: () => void } = $props();
  const a = app();
</script>

<Confirm
  title="Retry task {target.code}?"
  confirm="Retry"
  kind="warn"
  {oncancel}
  onconfirm={async () => {
    await a.api.resolve(target.id, 'retry_fresh');
    toast.success('Retry requested', { description: `${target.title} (${target.code}). Waves never reruns a task on its own.` });
    ondone();
  }}
>
  <p>The task goes back to Ready for a harness to claim. The stopped execution stays in the log. Waves never reruns it on its own.</p>
</Confirm>
