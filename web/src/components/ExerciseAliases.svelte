<script lang="ts">
  import { HTTPError, OfflineError, deleteJSON, getJSON } from '../lib/api';
  import type { ExerciseAlias } from '../lib/types';

  // Exercise names Outpost's System 1 model counted as names already in use
  // ("BB bench" as Bench Press), so their lifts share one line. Each can be
  // undone; it isn't asked about again.
  let { onchange }: { onchange?: () => void } = $props();

  let aliases = $state<ExerciseAlias[]>([]);
  let busy = $state('');
  let status = $state('');

  $effect(() => {
    getJSON<ExerciseAlias[]>('/v1/exercise-aliases').then(
      (a) => (aliases = a),
      () => (aliases = []),
    );
  });

  async function undo(a: ExerciseAlias) {
    if (busy) return;
    busy = a.name;
    status = '';
    try {
      await deleteJSON(`/v1/exercise-aliases/${encodeURIComponent(a.name)}`);
      aliases = aliases.filter((x) => x.name !== a.name);
      status = `${a.name} is its own lift again.`;
      onchange?.();
    } catch (err) {
      status =
        err instanceof OfflineError ? 'No connection. Try again when you can reach the Mac Mini.'
        : err instanceof HTTPError ? err.message
        : "Couldn't undo that. Try again.";
    } finally {
      busy = '';
    }
  }
</script>

{#if aliases.length}
  <div class="aliases">
    <p class="muted head">Counted together</p>
    <ul>
      {#each aliases as a (a.name)}
        <li>
          <span><span class="name">{a.name}</span> <span class="muted">counted as</span> <span class="name">{a.to}</span></span>
          <button class="btn btn-quiet" onclick={() => undo(a)} disabled={!!busy} aria-label={`Stop counting ${a.name} as ${a.to}`}>
            {busy === a.name ? 'Undoing…' : 'Undo'}
          </button>
        </li>
      {/each}
    </ul>
  </div>
{/if}
<p class="status" role="status">{status}</p>

<style>
  .aliases {
    margin-top: var(--space-4);
    font-size: var(--size-small);
  }
  .head {
    font-weight: 600;
  }
  ul {
    list-style: none;
    margin: var(--space-1) 0 0;
    padding: 0;
  }
  li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
    padding: var(--space-1) 0;
    border-bottom: 1px solid var(--line);
  }
  .name {
    font-weight: 600;
  }
  .status {
    font-size: var(--size-small);
  }
  .status:empty {
    display: none;
  }
</style>
