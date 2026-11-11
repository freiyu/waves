<script lang="ts">
  // Sign-in, signed-out and first-run states. These render without the app shell.
  import { onDestroy } from 'svelte';
  import { app } from '../lib/app.svelte';
  import { route, go, href } from '../lib/router.svelte';
  import Logo from '../lib/Logo.svelte';
  import Note from '../lib/Note.svelte';
  import Btn from '../lib/Btn.svelte';
  import Badge from '../lib/Badge.svelte';
  import { ApiError } from '../api/client';

  const a = app();
  let left = $state<number | null>(null); // seconds left on a valid link, null while unknown
  let invalid = $state(false);
  let failed = $state('');
  let busy = $state(false);
  let timer: ReturnType<typeof setInterval> | undefined;

  const mode = $derived(route.name === 'welcome' ? 'welcome' : route.name === 'signin' ? 'link' : 'ended');

  $effect(() => {
    if (mode !== 'link') return;
    const code = route.params.code!;
    invalid = false;
    left = null;
    a.api.linkStatus(code).then(
      (s) => {
        if (s.state !== 'valid') return (invalid = true);
        left = s.expires_in_s ?? 0;
        history.replaceState(null, '', '#/signin'); // the code is single-use; do not leave it in the address bar or history
        clearInterval(timer);
        timer = setInterval(() => {
          left = Math.max(0, (left ?? 0) - 1);
          if (left === 0) {
            clearInterval(timer);
            invalid = true;
          }
        }, 1000);
      },
      () => (failed = 'The service did not answer. Check that it is running: waves status.'),
    );
  });
  onDestroy(() => clearInterval(timer));

  async function open() {
    busy = true;
    try {
      await a.signIn(route.params.code!);
      go('/');
    } catch (e) {
      invalid = true;
      failed = e instanceof ApiError ? e.message : '';
    } finally {
      busy = false;
    }
  }
</script>

<div class="min-h-screen grid place-items-center p-6">
  <main class="panel w-[min(460px,100%)]" id="main">
    <div class="pb stack gap-4">
      {#if mode === 'welcome'}
        <Logo /><h1>Welcome to Waves</h1>
        <h2>One step is left in the terminal</h2>
        <div class="muted">The approval passphrase can only be set on a terminal. It is never typed into this page.</div>
        <pre class="cli">$ waves setup</pre>
        <Note kind="info" icon="lock">Until it is set, the dashboard is read-only and every approval is unavailable.</Note>
        {#if a.state.session}<div><Btn href={href('/')}>Back to the dashboard</Btn></div>{/if}
      {:else if mode === 'link' && !invalid && failed === ''}
        <Logo /><h1 class="sr-only">Waves</h1>
        <h2>Sign in from this machine</h2>
        <div class="muted">This link works once and expires after 60 seconds.</div>
        <div class="row wrap"><span class="mono muted">{location.host}</span>{#if left !== null}<Badge state="connected" text="Link valid · {left} s left" />{:else}<span class="muted" role="status">Checking the link…</span>{/if}</div>
        <Note kind="neu" icon="lock">You will be signed in as <b>operator</b>. Approving anything asks for your approval passphrase every time.</Note>
        <div><Btn kind="pri" disabled={left === null || busy} onclick={open}>Open dashboard</Btn></div>
      {:else if mode === 'link'}
        <Logo /><h1 class="sr-only">Waves</h1>
        <Note kind="warn" role="alert"><b>{failed || 'This link was already used or has expired.'}</b>{#if !failed}<br />Links are single-use and last 60 seconds.{/if}</Note>
        <div class="muted">Create a new one in your terminal:</div>
        <pre class="cli">$ waves open</pre>
      {:else}
        <Logo /><h1 class="sr-only">Waves</h1>
        <Note kind="neu" icon="wait" role="status"><b>Your session ended.</b><br />Sessions last 12 hours or until you sign out.</Note>
        <div class="muted">Open a fresh link:</div>
        <pre class="cli">$ waves open</pre>
      {/if}
    </div>
  </main>
</div>
