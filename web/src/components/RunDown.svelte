<script lang="ts">
  // System 1 thinks the last few mornings look like an illness coming on:
  // resting heart rate up and HRV down against the athlete's own baseline,
  // more than training explains. A gentle question, never a change to the
  // day's call; dismissed for the day with one tap.
  import { HTTPError, postJSON } from '../lib/api';

  let { date, ondismiss }: { date: string; ondismiss: () => void } = $props();

  let busy = $state(false);
  let error = $state('');

  async function dismiss() {
    if (busy) return;
    busy = true;
    error = '';
    try {
      await postJSON(`/v1/run-down/${date}/dismiss`);
      ondismiss();
    } catch (err) {
      error = err instanceof HTTPError ? err.message : 'No connection. Try again when online.';
    } finally {
      busy = false;
    }
  }
</script>

<aside class="run-down" aria-labelledby="run-down-h">
  <h2 id="run-down-h" class="title">Feeling run down?</h2>
  <p class="text">
    Your resting heart rate has been up and your HRV down for a few mornings, more than your training explains. If you're coming
    down with something, rest beats training.
  </p>
  <button class="btn btn-quiet" onclick={dismiss} disabled={busy}>I'm fine</button>
  {#if error}<p class="small err" role="alert">{error}</p>{/if}
</aside>

<style>
  .run-down {
    margin-top: var(--space-4);
    padding: var(--space-3) var(--space-4);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    max-width: 68ch;
  }
  .title {
    margin: 0 0 var(--space-1);
    font-size: var(--size-body);
  }
  .text {
    margin: 0 0 var(--space-2);
    text-wrap: pretty;
  }
  .btn {
    margin-left: calc(-1 * var(--space-2));
  }
  .err {
    color: var(--danger);
  }
</style>
