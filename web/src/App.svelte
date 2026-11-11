<script lang="ts">
  import { app } from './lib/app.svelte';
  import { route } from './lib/router.svelte';
  import Shell from './lib/Shell.svelte';
  import SignIn from './pages/SignIn.svelte';
  import Overview from './pages/Overview.svelte';
  import Projects from './pages/Projects.svelte';
  import Project from './pages/Project.svelte';
  import Requirements from './pages/Requirements.svelte';
  import Plan from './pages/Plan.svelte';
  import Workflow from './pages/Workflow.svelte';
  import PhaseReview from './pages/PhaseReview.svelte';
  import Approvals from './pages/Approvals.svelte';
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
    overview: Overview, projects: Projects, project: Project, requirements: Requirements, plan: Plan, workflow: Workflow, 'workflow-version': Workflow,
    'phase-review': PhaseReview, approvals: Approvals, approval: Approvals, executions: Executions, execution: Execution, recovery: Recovery,
    harnesses: Harnesses, connect: Connect, activity: Activity, settings: Settings,
  };
  const bare = $derived(a.state.signedOut || route.name === 'signin' || route.name === 'welcome' || route.name === 'signed-out');
  const Page = $derived(PAGES[route.name] ?? NotFound);
</script>

{#if !a.state.loaded}
  <div class="empty" role="status">Loading Waves…</div>
{:else if bare}
  <SignIn />
{:else}
  <Shell><Page /></Shell>
{/if}

<Toaster position="bottom-right" duration={4000} theme={theme.value} />
