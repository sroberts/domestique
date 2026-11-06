<script lang="ts">
  import Download from '@lucide/svelte/icons/download';
  import { localDate } from '../lib/api';
  import { rangeURL } from '../lib/export';

  const today = localDate();
  const now = new Date();
  let from = $state(`${now.getFullYear()}-01-01`);
  let to = $state(today);
  let files = $state<'generated' | 'originals'>('generated');

  function preset(kind: 'year' | '12m' | 'last') {
    const d = new Date();
    if (kind === 'year') {
      from = `${d.getFullYear()}-01-01`;
      to = today;
    } else if (kind === '12m') {
      from = localDate(new Date(d.getFullYear() - 1, d.getMonth(), d.getDate() + 1));
      to = today;
    } else {
      from = `${d.getFullYear() - 1}-01-01`;
      to = `${d.getFullYear() - 1}-12-31`;
    }
  }

  // The server takes at most five years at a time.
  let problem = $derived.by(() => {
    if (!from || !to) return 'Pick both dates.';
    if (to < from) return 'The end date is before the start.';
    const span = (new Date(to).getTime() - new Date(from).getTime()) / 86_400_000;
    if (span > 5 * 366) return 'Five years at most per download. Split it into two.';
    return '';
  });
</script>

<div class="exp">
  <div class="presets" role="group" aria-label="Quick ranges">
    <button class="btn btn-quiet" type="button" onclick={() => preset('year')}>This year</button>
    <button class="btn btn-quiet" type="button" onclick={() => preset('12m')}>Last 12 months</button>
    <button class="btn btn-quiet" type="button" onclick={() => preset('last')}>Last year</button>
  </div>
  <div class="dates">
    <label>
      <span class="label">From</span>
      <input type="date" bind:value={from} max={today} />
    </label>
    <label>
      <span class="label">To</span>
      <input type="date" bind:value={to} max={today} />
    </label>
  </div>
  <fieldset class="files">
    <legend class="label">Activity files</legend>
    <label class="opt">
      <input type="radio" name="files" value="generated" bind:group={files} />
      <span><strong>Generated FIT</strong><span class="muted small">One clean file per activity, merged from every recording.</span></span>
    </label>
    <label class="opt">
      <input type="radio" name="files" value="originals" bind:group={files} />
      <span><strong>Original files</strong><span class="muted small">Exactly as imported from Wahoo, Zwift or Strava. Activities without one get a generated FIT.</span></span>
    </label>
  </fieldset>
  {#if problem}
    <p class="small err" role="status">{problem}</p>
  {:else}
    <a class="btn dl" href={rangeURL(from, to, files)} download><Download size={18} aria-hidden="true" /> Download zip</a>
  {/if}
  <p class="muted small">
    The zip holds activities.csv (one row per activity: time, distance, power, heart rate, load, sources, bike), sets.csv
    for strength sessions, and the activity files.
  </p>
</div>

<style>
  .exp {
    display: grid;
    gap: var(--space-3);
    margin-top: var(--space-3);
  }
  .presets {
    display: flex;
    flex-wrap: wrap;
    margin-left: calc(var(--space-2) * -1);
  }
  .dates {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 11rem));
    gap: var(--space-3);
  }
  .dates label {
    display: grid;
    gap: var(--space-1);
  }
  input[type='date'] {
    min-height: 2.75rem;
    min-width: 0;
    width: 100%;
    padding: 0 var(--space-3);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    background: var(--surface);
    color: var(--text);
    font: inherit;
  }
  .files {
    display: grid;
    gap: var(--space-1);
    margin: 0;
    padding: 0;
    border: 0;
  }
  .opt {
    display: flex;
    gap: var(--space-3);
    align-items: flex-start;
    padding: var(--space-2) 0;
    min-height: 2.75rem;
    cursor: pointer;
  }
  .opt input {
    width: 1.25rem;
    height: 1.25rem;
    margin-top: 0.125rem;
    flex: none;
    accent-color: var(--signal);
  }
  .opt > span {
    display: grid;
  }
  .small {
    font-size: var(--size-small);
  }
  .err {
    color: var(--danger);
  }
  .dl {
    justify-self: start;
  }
</style>
