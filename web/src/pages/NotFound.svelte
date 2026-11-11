<script lang="ts">
  import { href } from '../lib/router.svelte';
  import PageHead from '../lib/PageHead.svelte';
  import Note from '../lib/Note.svelte';
</script>

<PageHead title="Page not found" />
<Note kind="neu">There is no page at this address. <a href={href('/')}>Go to the overview</a>.</Note>
