<script lang="ts">
  import ActivityRow from '../components/ActivityRow.svelte';
  import SyncStatus from '../components/SyncStatus.svelte';
  import { fetchActivities } from '../lib/data';
  import { day, duration, time } from '../lib/format';
  import { link } from '../lib/router.svelte';
  import type { ActivityRow as Row } from '../lib/types';

  type Filter = 'all' | 'ride' | 'strength' | 'other';
  const filters: { id: Filter; label: string }[] = [
    { id: 'all', label: 'All' },
    { id: 'ride', label: 'Rides' },
    { id: 'strength', label: 'Strength' },
    { id: 'other', label: 'Other' },
  ];

  let filter = $state<Filter>(readFilter());
  let rows = $state<Row[]>([]);
  let next = $state(0);
  let loading = $state(true);
  let stale = $state<number | null>(null);
  let failed = $state(false);

  function readFilter(): Filter {
    try {
      const v = localStorage.getItem('domestique:activity-filter');
      if (v === 'ride' || v === 'strength' || v === 'other') return v;
    } catch {
      /* ignore */
    }
    return 'all';
  }

  async function load(reset: boolean) {
    loading = true;
    const q = new URLSearchParams({ limit: '40' });
    if (filter !== 'all') q.set('sport', filter);
    if (!reset && next) q.set('before', String(next));
    const res = await fetchActivities('?' + q.toString());
    if (res.data) {
      rows = reset ? res.data.activities : [...rows, ...res.data.activities];
      next = res.data.next_before;
    }
    stale = res.stale;
    failed = res.error;
    loading = false;
  }

  $effect(() => {
    void filter;
    try {
      localStorage.setItem('domestique:activity-filter', filter);
    } catch {
      /* ignore */
    }
    void load(true);
  });

  // Group by ISO week (Monday start), with totals per week.
  let weeks = $derived.by(() => {
    const out: { key: string; label: string; rows: Row[]; hours: number; tss: number; km: number }[] = [];
    for (const r of rows) {
      const d = new Date(r.start_at);
      const monday = new Date(d.getFullYear(), d.getMonth(), d.getDate() - ((d.getDay() + 6) % 7));
      const key = monday.toDateString();
      let w = out.at(-1);
      if (!w || w.key !== key) {
        const now = new Date();
        const thisMon = new Date(now.getFullYear(), now.getMonth(), now.getDate() - ((now.getDay() + 6) % 7));
        const label =
          monday.getTime() === thisMon.getTime() ? 'This week'
          : monday.getTime() === thisMon.getTime() - 7 * 86400000 ? 'Last week'
          : `Week of ${day(monday.getTime())}`;
        w = { key, label, rows: [], hours: 0, tss: 0, km: 0 };
        out.push(w);
      }
      w.rows.push(r);
      w.hours += (r.moving_s || r.elapsed_s) / 3600;
      w.tss += r.tss;
      if (r.sport === 'ride' || r.sport === 'virtual_ride') w.km += r.distance_m / 1000;
    }
    return out;
  });
</script>

<main class="page">
  <header class="top">
    <h1>Activities</h1>
    <SyncStatus />
  </header>

  <div class="chips" role="radiogroup" aria-label="Show">
    {#each filters as f (f.id)}
      <button class="chip" role="radio" aria-checked={filter === f.id} class:on={filter === f.id} onclick={() => (filter = f.id)}>
        {f.label}
      </button>
    {/each}
  </div>

  {#if stale}
    <p class="notice">No connection. Showing activities as of {time(stale)}.</p>
  {/if}

  {#if !loading && rows.length === 0}
    <section class="empty">
      {#if failed}
        <h2>No connection</h2>
        <p class="muted">Domestique can't reach the Mac Mini, and nothing is saved on this device yet.</p>
        <button class="btn" onclick={() => load(true)}>Try again</button>
      {:else if filter === 'all'}
        <h2>No activities yet</h2>
        <p class="muted">
          Connect Wahoo and Whoop, or import your Strava archive, and every ride and lift lands here on its own.
        </p>
        <a class="btn" href="/settings" use:link>Set up imports</a>
      {:else}
        <h2>Nothing in this filter</h2>
        <p class="muted">Try All to see every activity.</p>
      {/if}
    </section>
  {/if}

  {#each weeks as w (w.key)}
    <section class="week" aria-label={w.label}>
      <h2 class="week-h">
        <span>{w.label}</span>
        <span class="mono muted">{[duration(w.hours * 3600), w.km >= 1 ? `${Math.round(w.km)} km` : '', `${Math.round(w.tss)} TSS`].filter(Boolean).join(' · ')}</span>
      </h2>
      {#each w.rows as r (r.id)}
        <ActivityRow a={r} />
      {/each}
    </section>
  {/each}

  {#if next && rows.length > 0}
    <div class="more">
      <button class="btn" onclick={() => load(false)} disabled={loading}>{loading ? 'Loading…' : 'Show older'}</button>
    </div>
  {/if}
</main>

<style>
  .page {
    max-width: 40rem;
    margin: 0 auto;
    padding: calc(env(safe-area-inset-top) + var(--space-2)) var(--gutter) 0;
  }
  .top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    min-height: 2.75rem;
  }
  h1 {
    font-size: var(--size-heading);
  }
  .chips {
    display: flex;
    gap: var(--space-2);
    margin-top: var(--space-4);
    overflow-x: auto;
    scrollbar-width: none;
  }
  .chip {
    flex: none;
    min-height: 2.5rem;
    padding: 0 var(--space-4);
    border: 1px solid var(--line-strong);
    border-radius: 999px;
    background: transparent;
    font-size: var(--size-small);
    font-weight: 600;
    color: var(--text-muted);
    cursor: pointer;
  }
  .chip.on {
    background: var(--text);
    border-color: var(--text);
    color: var(--bg);
  }
  .notice {
    margin-top: var(--space-4);
    padding: var(--space-3) var(--space-4);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    font-size: var(--size-small);
  }
  .week {
    margin-top: var(--space-6);
  }
  .week-h {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    align-items: baseline;
    gap: var(--space-2);
    padding-bottom: var(--space-2);
    border-bottom: 1px solid var(--line-strong);
    font-size: var(--size-body);
  }
  .week-h .mono {
    font-size: var(--size-label);
    font-weight: 400;
  }
  .empty {
    display: grid;
    gap: var(--space-3);
    justify-items: start;
    margin-top: var(--space-7);
  }
  .empty h2 {
    font-size: var(--size-heading);
  }
  .empty p {
    max-width: 50ch;
  }
  .more {
    display: flex;
    justify-content: center;
    margin-top: var(--space-5);
  }
</style>
