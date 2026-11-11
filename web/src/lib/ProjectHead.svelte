<script lang="ts">
  // The header every project page shares: project name with its Active Context tag, path and branch, then the tab bar.
  import type { Snippet } from 'svelte';
  import { app } from './app.svelte';
  import { resource } from './resource.svelte';
  import { href } from './router.svelte';
  import type { ProjectDetail } from '../api/models';
  import PageHead from './PageHead.svelte';
  import ProjectTabs from './ProjectTabs.svelte';
  // Pages that already hold the project pass it in; Workflow and Phase review let the header load it.
  let { id, current, project, name = '', actions }: { id: string; current: 'overview' | 'requirements' | 'plan' | 'workflow' | 'review'; project?: ProjectDetail; name?: string; actions?: Snippet } = $props();
  const a = app();
  // svelte-ignore state_referenced_locally
  const own = project ? null : resource(() => a.api.project(id), ['project.changed', 'phase.changed', 'workflow.changed']);
  const data = $derived(project ?? own?.data);
  const title = $derived(data?.name ?? name);
</script>

<PageHead {title} sub={data ? `${data.path}${data.branch ? ' · ' + data.branch : ''}` : ''} crumbs={[{ label: 'Projects', href: href('/projects') }, { label: title }]}>
  {#snippet right()}{#if data?.active}<span class="tag">Active Context</span>{/if}{@render actions?.()}{/snippet}
</PageHead>
<ProjectTabs {id} {current} project={data} />
