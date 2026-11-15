<script lang="ts">
  import { app } from './lib/app.svelte';
  import { route } from './lib/router.svelte';
  import Shell from './lib/Shell.svelte';
  import SignIn from './pages/SignIn.svelte';
  import Overview from './pages/Overview.svelte';
  import Projects from './pages/Projects.svelte';
  import ProjectArea from './pages/ProjectArea.svelte';
  import PhaseRedirect from './pages/PhaseRedirect.svelte';
  import Graph from './pages/Graph.svelte';
  import Redirect from './lib/Redirect.svelte';
  import Executions from './pages/Executions.svelte';
  import Execution from './pages/Execution.svelte';
  import Recovery from './pages/Recovery.svelte';
  import Harnesses from './pages/Harnesses.svelte';
  import Connect from './pages/Connect.svelte';
  import Activity from './pages/Activity.svelte';
  import Settings from './pages/Settings.svelte';
  import NotFound from './pages/NotFound.svelte';
  import type { Component } from 'svelte';
  import { Toaster } from 'svelte-sonner';
  import { theme } from './lib/theme.svelte';

  const a = app();
  const PAGES: Record<string, Component> = {
    overview: Overview, graph: Graph, projects: Projects, project: ProjectArea, requirements: ProjectArea, plan: ProjectArea, review: ProjectArea, guard: ProjectArea,
    'phase-review': PhaseRedirect, executions: Executions, execution: Execution, recovery: Recovery,
    harnesses: Harnesses, connect: Connect, activity: Activity, settings: Settings,
  };
  const bare = $derived(a.state.signedOut || route.name === 'signin' || route.name === 'welcome' || route.name === 'signed-out');
  const Page = $derived(PAGES[route.name] ?? NotFound);
  // Addresses without a page of their own: the approval inbox and the workflow page are gone (decisions live in Projects and the Graph, the workflow is in the Plan pane).
  const moved = $derived(['approvals', 'approval', 'workflow-version'].includes(route.name) ? '/' : route.name === 'workflow' ? `/projects/${route.params.id}/plan` : '');
</script>

{#if !a.state.loaded}
  <div class="empty" role="status">Loading Waves…</div>
{:else if bare}
  <SignIn />
{:else}
  <Shell>{#if moved}<Redirect to={moved} />{:else}<Page />{/if}</Shell>
{/if}

<Toaster position="bottom-right" duration={4000} theme={theme.value} />
