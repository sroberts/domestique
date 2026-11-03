<script lang="ts">
  import { AuthError } from '../lib/api';
  import { fetchActivityBike, setActivityBike, type ActivityBike } from '../lib/gear';
  import { link } from '../lib/router.svelte';

  let { activityId }: { activityId: string } = $props();
  let ab = $state<ActivityBike | null>(null);
  let changing = $state(false);
  let saving = $state(false);
  let msg = $state('');

  $effect(() => {
    fetchActivityBike(activityId)
      .then((r) => (ab = r))
      .catch(() => (ab = null)); // offline: the row simply isn't shown
  });

  const why: Record<ActivityBike['how'], string> = {
    manual: 'chosen by you',
    strava: 'from Strava',
    default: 'default bike',
    none: '',
  };

  // The select's value: a bike id, "none" for no bike, "auto" for automatic.
  let choice = $state('');
  function open() {
    if (!ab) return;
    choice = ab.how === 'manual' ? (ab.bike?.id ?? 'none') : 'auto';
    changing = true;
    msg = '';
  }

  async function save() {
    if (!ab) return;
    saving = true;
    msg = '';
    try {
      ab = await setActivityBike(activityId, choice === 'auto' ? undefined : choice === 'none' ? null : choice);
      changing = false;
    } catch (err) {
      msg = err instanceof AuthError ? 'Sign in again to change it.' : `Couldn't save: ${(err as Error).message}`;
    } finally {
      saving = false;
    }
  }
</script>

{#if ab}
  <div class="bike">
    {#if !changing}
      <p class="cur">
        <span class="label">Bike</span>
        {#if ab.bike}
          <span class="name">{ab.bike.name}</span>{#if why[ab.how]}<span class="muted">{' · '}{why[ab.how]}</span>{/if}
        {:else if ab.bikes.length === 0}
          <span class="muted">No bikes yet. <a href="/gear" use:link>Add one in Gear</a></span>
        {:else if ab.how === 'none' && ab.strava_gear}
          <span class="muted">“{ab.strava_gear}” on Strava, which isn't one of your bikes</span>
        {:else}
          <span class="muted">None</span>
        {/if}
      </p>
      {#if ab.bikes.length}<button class="btn btn-quiet" onclick={open}>Change</button>{/if}
    {:else}
      <label class="pick">
        <span class="label">Bike for this ride</span>
        <select bind:value={choice}>
          <option value="auto">Automatic{ab.strava_gear ? ` (Strava: ${ab.strava_gear})` : ' (default bike)'}</option>
          {#each ab.bikes as b (b.id)}
            <option value={b.id}>{b.name}{b.retired ? ' (retired)' : ''}</option>
          {/each}
          <option value="none">None of my bikes</option>
        </select>
      </label>
      <div class="acts">
        <button class="btn" onclick={save} disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
        <button class="btn btn-quiet" onclick={() => (changing = false)}>Cancel</button>
      </div>
      {#if msg}<p class="msg" role="status">{msg}</p>{/if}
    {/if}
  </div>
{/if}

<style>
  .bike {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2) var(--space-3);
    margin-top: var(--space-3);
    padding: var(--space-1) 0;
    border-bottom: 1px solid var(--line);
  }
  .cur {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-2);
    min-height: 2.75rem;
    align-content: center;
  }
  .cur .muted {
    font-size: var(--size-small);
  }
  .name {
    font-weight: 600;
  }
  .pick {
    display: grid;
    gap: var(--space-1);
    flex: 1 1 14rem;
  }
  select {
    min-height: 2.75rem;
    max-width: 100%;
    padding: 0 var(--space-3);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    background: var(--surface);
    color: var(--text);
    font: inherit;
  }
  .acts {
    display: flex;
    gap: var(--space-2);
  }
  .msg {
    flex-basis: 100%;
    font-size: var(--size-small);
  }
</style>
