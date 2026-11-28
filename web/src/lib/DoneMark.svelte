<script lang="ts">
  // The screen dims and a ring turns, closes and becomes a check (a cross that shakes for a rejection), with the word for
  // what happened. It is only shown after the service answered with success, and it takes no clicks.
  import { done } from './done.svelte';
  const no = $derived(done.word === 'Rejected');
</script>

<div aria-live="polite">
  {#if done.word}
    {#key done.n}
      <div class="donemark" class:bad={done.bad} class:no>
        <div>
          <svg viewBox="0 0 64 64" fill="none" stroke="currentColor" stroke-width="5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <circle class="spin" cx="32" cy="32" r="28" pathLength="100" />
            <circle class="ring" cx="32" cy="32" r="28" pathLength="100" />
            <path class="tick" class:cross={no} d={no ? 'M23 23l18 18M41 23L23 41' : 'M20 33l8 8 16-17'} pathLength="100" />
          </svg>
          <b>{done.word}</b>
        </div>
      </div>
    {/key}
  {/if}
</div>
