<script lang="ts">
  // On a hold-back day, Outpost's LLM proposes an easier version of the
  // planned session. Taking it changes today's plan only; the workout itself
  // stays as written. Once taken, this shrinks to a line with a way back.
  import { deleteJSON, HTTPError, postJSON } from '../lib/api';
  import type { EaseView, Exercise } from '../lib/types';

  let {
    date,
    ease,
    eased = false,
    plan,
    onchange,
  }: { date: string; ease?: EaseView; eased?: boolean; plan: Exercise[]; onchange: () => void } = $props();

  let busy = $state(false);
  let error = $state('');

  const path = $derived(`/v1/plans/${date}/ease`);

  function sets(ex: Exercise): string {
    const reps = new Set(ex.sets.map((s) => s.reps));
    const kgs = new Set(ex.sets.map((s) => s.weight_kg));
    const r = reps.size === 1 ? `${ex.sets.length} × ${ex.sets[0]!.reps}` : `${ex.sets.length} sets`;
    if (kgs.size !== 1) return r;
    const kg = ex.sets[0]!.weight_kg;
    return kg === 0 ? `${r} · BW` : `${r} · ${kg} kg`;
  }

  // Only the lifts the easier session changes, before and after.
  let changes = $derived(
    (ease?.exercises ?? [])
      .map((ex, i) => ({ name: ex.name, from: plan[i] ? sets(plan[i]) : '', to: sets(ex) }))
      .filter((c) => c.from !== c.to),
  );

  async function act(fn: () => Promise<void>) {
    if (busy) return;
    busy = true;
    error = '';
    try {
      await fn();
      onchange();
    } catch (err) {
      error = err instanceof HTTPError ? err.message : 'No connection. Try again when online.';
    } finally {
      busy = false;
    }
  }
  const take = () => act(() => postJSON(path, { exercises: ease!.exercises }));
  const keep = () => act(() => postJSON(path, { keep: true }));
  const undo = () => act(() => deleteJSON(path));
</script>

{#if eased}
  <p class="eased small">
    <span>Eased for today.</span>
    <button class="btn btn-quiet" onclick={undo} disabled={busy}>Back to the plan</button>
  </p>
{:else if ease}
  <aside class="offer" aria-label="An easier session for today">
    <p class="label who">From the team car</p>
    <p class="reason">{ease.reason}</p>
    {#if changes.length}
      <ul class="changes small">
        {#each changes as c (c.name)}
          <li><span class="ex">{c.name}</span> <span class="mono"><s>{c.from}</s> → {c.to}</span></li>
        {/each}
      </ul>
    {/if}
    <div class="acts">
      <button class="btn" onclick={take} disabled={busy}>Use the easier version</button>
      <button class="btn btn-quiet" onclick={keep} disabled={busy}>Keep the plan</button>
    </div>
  </aside>
{/if}
{#if error}<p class="small err" role="alert">{error}</p>{/if}

<style>
  .offer {
    margin: var(--space-3) 0;
    padding: var(--space-3) var(--space-4);
    background: var(--surface-raised);
    border: 1px solid var(--line);
    border-radius: var(--radius-m);
  }
  .who {
    margin: 0 0 var(--space-1);
    color: var(--text-muted);
  }
  .reason {
    margin: 0;
    text-wrap: pretty;
  }
  .changes {
    margin: var(--space-2) 0 0;
    padding: 0;
    list-style: none;
    display: grid;
    gap: var(--space-1);
  }
  .ex {
    font-weight: 600;
  }
  s {
    color: var(--text-muted);
  }
  .acts {
    margin-top: var(--space-3);
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  .eased {
    margin: var(--space-1) 0 var(--space-2);
    display: flex;
    align-items: center;
    gap: var(--space-2);
    color: var(--text-muted);
  }
  .err {
    color: var(--danger);
  }
</style>
