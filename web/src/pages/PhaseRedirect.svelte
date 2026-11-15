<script lang="ts">
  // The standalone phase review page is now the Review pane of its project.
  import { app } from '../lib/app.svelte';
  import { route } from '../lib/router.svelte';
  import { resource } from '../lib/resource.svelte';
  import View from '../lib/View.svelte';
  import Redirect from '../lib/Redirect.svelte';
  const a = app();
  const res = resource(() => a.api.phaseReview(route.params.id!));
</script>
<View {res} label="this review">
  {#snippet children(r)}<Redirect to={'/projects/' + r.project_id + '/review'} />{/snippet}
</View>
