<script lang="ts">
  import CurveChart from '../components/CurveChart.svelte';
  import LoadChart from '../components/LoadChart.svelte';
  import SyncStatus from '../components/SyncStatus.svelte';
  import WeekBars from '../components/WeekBars.svelte';
  import { fetchLoad, fetchTrends } from '../lib/data';
  import { day, time } from '../lib/format';
  import { link } from '../lib/router.svelte';
  import type { LoadDay, Trends } from '../lib/types';

  const ranges = [
    { days: 90, label: '3 months' },
    { days: 182, label: '6 months' },
    { days: 365, label: '1 year' },
  ];
  let range = $state(90);
  let load = $state<LoadDay[]>([]);
  let trends = $state<Trends | null>(null);
  let stale = $state<number | null>(null);
  let empty = $state(false);

  $effect(() => {
    const days = range;
    fetchLoad(days).then((r) => {
      if (r.data) load = r.data.days;
      stale = r.stale ?? stale;
    });
  });
  $effect(() => {
    fetchTrends().then((r) => {
      trends = r.data;
      stale = r.stale ?? stale;
      empty = !!r.data && r.data.weeks.every((w) => w.count === 0) && r.data.lifts.length === 0;
    });
  });

  let curveDurations = $derived(trends?.curve_all_time.map((c) => c.duration) ?? []);
  let hasPower = $derived(!!trends?.curve_all_time.some((c) => c.watts > 0));
  // When every best is recent, two identical lines would be one line and a legend lie.
  let sameCurve = $derived(!!trends && trends.curve_90d.every((c, i) => c.watts === (trends!.curve_all_time[i]?.watts ?? 0)));
  let ftpMax = $derived(Math.max(1, ...(trends?.ftp_history.map((f) => f.ftp) ?? [1])));
  let ftpMin = $derived(Math.min(...(trends?.ftp_history.map((f) => f.ftp) ?? [0])));

  function spark(h: [number, number][], w = 96, ht = 28): string {
    if (h.length < 2) return '';
    const vs = h.map((p) => p[1]);
    const lo = Math.min(...vs);
    const hi = Math.max(...vs);
    return h
      .map((p, i) => `${i ? 'L' : 'M'}${((i / (h.length - 1)) * w).toFixed(1)},${(ht - 2 - ((p[1] - lo) / Math.max(1, hi - lo)) * (ht - 4)).toFixed(1)}`)
      .join('');
  }
</script>

<main class="page">
  <header class="top">
    <h1>Trends</h1>
    <SyncStatus />
  </header>
  {#if stale}<p class="notice">No connection. Showing trends as of {time(stale)}.</p>{/if}

  {#if empty}
    <section class="empty">
      <h2>Nothing to trend yet</h2>
      <p class="muted">Fitness, fatigue and power curves build from your rides. Import your Strava archive to start with years of history.</p>
      <a class="btn" href="/settings" use:link>Set up imports</a>
    </section>
  {:else}
    <section class="block" aria-labelledby="load-h">
      <div class="block-top">
        <h2 id="load-h" class="block-h">Fitness, fatigue and form</h2>
        <div class="seg" role="radiogroup" aria-label="Range">
          {#each ranges as r (r.days)}
            <button role="radio" aria-checked={range === r.days} class:on={range === r.days} onclick={() => (range = r.days)}>{r.label}</button>
          {/each}
        </div>
      </div>
      {#if load.length > 1}
        <LoadChart days={load} />
        <p class="note">
          Fitness is a 42-day average of daily load, fatigue a 7-day one, and form is yesterday's fitness minus yesterday's fatigue (how fresh you start the day), so it can differ from today's two numbers. Rides count
          by power, lifts by Whoop strain or heart rate.
        </p>
      {/if}
    </section>

    {#if trends}
      <section class="block" aria-labelledby="wk-h">
        <h2 id="wk-h" class="block-h">Weekly load</h2>
        <WeekBars weeks={trends.weeks.slice(Math.max(0, Math.min(trends.weeks.findIndex((w) => w.count > 0), trends.weeks.length - 8)))} />
      </section>

      {#if hasPower}
        <section class="block" aria-labelledby="pc-h">
          <h2 id="pc-h" class="block-h">Power curve <span class="mono muted">FTP {trends.athlete.ftp} W ({trends.athlete.ftp_source})</span></h2>
          <CurveChart
            durations={curveDurations}
            series={sameCurve
              ? [{ label: 'All time, same as the last 90 days', values: trends.curve_all_time.map((c) => c.watts), style: 'primary' }]
              : [
                  { label: 'Last 90 days', values: trends.curve_90d.map((c) => c.watts), style: 'primary' },
                  { label: 'All time', values: trends.curve_all_time.map((c) => c.watts), style: 'compare' },
                ]}
          />
          <ul class="bests">
            {#each trends.curve_all_time.filter((c) => [5, 60, 300, 1200, 3600].includes(c.duration) && c.watts > 0) as c (c.duration)}
              <li>
                <span class="dlabel">{c.duration < 60 ? `${c.duration} s` : c.duration < 3600 ? `${c.duration / 60} min` : '1 hour'}</span>
                <span class="num">{c.watts}</span><span class="unit">W</span>
                {#if c.activity_id}<a class="mono when" href={`/activity/${c.activity_id}`} use:link>{day(c.at ?? 0)}</a>{/if}
              </li>
            {/each}
          </ul>
        </section>
      {/if}

      {#if trends.ftp_history.length > 1}
        <section class="block" aria-labelledby="ftp-h">
          <h2 id="ftp-h" class="block-h">FTP by month <span class="muted sub-h">95% of best 20 minutes, rolling 120 days</span></h2>
          <ol class="ftp">
            {#each trends.ftp_history.slice(-12) as f (f.month)}
              <li>
                <span class="bar" style:height={`${20 + ((f.ftp - ftpMin) / Math.max(1, ftpMax - ftpMin)) * 60}%`}></span>
                <span class="num v">{f.ftp}</span>
                <span class="mono m">{new Date(f.month + '-15').toLocaleString('en-GB', { month: 'short' })}</span>
              </li>
            {/each}
          </ol>
        </section>
      {/if}

      {#if trends.lifts.length > 0}
        <section class="block" aria-labelledby="lift-h">
          <h2 id="lift-h" class="block-h">Lifts <span class="muted sub-h">estimated one-rep max</span></h2>
          <ul class="lifts">
            {#each trends.lifts as l (l.exercise)}
              {@const pr = Date.now() - l.best_at < 14 * 86400000}
              <li>
                <span class="l-name">{l.exercise}<span class="mono muted sess">{l.sessions} sessions</span></span>
                <svg class="spark" width="96" height="28" aria-hidden="true"><path d={spark(l.history)} /></svg>
                <span class="l-best">
                  <span class="num">{l.best_e1rm.toFixed(0)}</span><span class="unit">kg</span>
                  {#if pr}<span class="pr">PR</span>{/if}
                  <span class="mono muted sub">{l.best_set} · {day(l.best_at)}</span>
                </span>
              </li>
            {/each}
          </ul>
        </section>
      {/if}
    {/if}
  {/if}
</main>

<style>
  .page {
    max-width: var(--page-list);
    margin: 0 auto;
    padding: calc(env(safe-area-inset-top) + var(--space-2)) var(--gutter) 0;
  }
  @media (min-width: 900px) {
    .page {
      max-width: var(--page-wide);
    }
  }
  .top {
    display: flex;
    justify-content: space-between;
    align-items: center;
    min-height: 2.75rem;
  }
  h1 {
    font-size: var(--size-heading);
  }
  .notice {
    margin-top: var(--space-3);
    padding: var(--space-3) var(--space-4);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    font-size: var(--size-small);
  }
  .empty {
    display: grid;
    gap: var(--space-3);
    justify-items: start;
    margin-top: var(--space-7);
  }
  .block {
    margin-top: var(--space-6);
  }
  .block-top {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    align-items: center;
    gap: var(--space-2);
    margin-bottom: var(--space-3);
  }
  .block-top .block-h {
    margin: 0;
  }
  .block-h {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    align-items: baseline;
    gap: var(--space-2);
    font-size: var(--size-body);
    margin-bottom: var(--space-3);
  }
  .block-h .mono,
  .block-h .sub-h {
    font-size: var(--size-label);
    font-weight: 400;
  }
  .seg {
    display: inline-flex;
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    overflow: hidden;
  }
  .seg button {
    min-height: 2.75rem;
    padding: 0 var(--space-3);
    border: 0;
    background: transparent;
    font-size: var(--size-label);
    font-weight: 600;
    color: var(--text-muted);
    cursor: pointer;
  }
  .seg button + button {
    border-left: 1px solid var(--line-strong);
  }
  .seg button.on {
    background: var(--text);
    color: var(--bg);
  }
  .note {
    margin-top: var(--space-3);
    font-size: var(--size-label);
    color: var(--text-muted);
    max-width: 62ch;
  }
  .bests {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(6rem, 1fr));
    gap: var(--space-3);
    list-style: none;
    margin: var(--space-4) 0 0;
    padding: var(--space-3) 0 0;
    border-top: 1px solid var(--line);
  }
  .bests li {
    display: grid;
    grid-template-columns: auto 1fr;
    align-items: baseline;
    column-gap: 0.25rem;
  }
  .bests .dlabel {
    grid-column: 1 / -1;
    font-size: var(--size-label);
    font-weight: 600;
    color: var(--text-muted);
  }
  .bests .num {
    font-size: var(--size-heading);
  }
  .unit {
    font-size: var(--size-label);
    color: var(--text-muted);
  }
  .bests .when {
    grid-column: 1 / -1;
    display: inline-flex;
    align-items: center;
    min-height: 2.75rem;
    margin-top: -0.5rem;
    font-size: var(--size-label);
    color: var(--text-muted);
  }
  .ftp {
    display: flex;
    align-items: flex-end;
    gap: var(--space-2);
    height: 8rem;
    list-style: none;
    margin: 0;
    padding: 0;
    border-bottom: 1px solid var(--line-strong);
  }
  .ftp li {
    flex: 1;
    max-width: 3rem;
    height: 100%;
    display: flex;
    flex-direction: column;
    justify-content: flex-end;
    align-items: center;
    gap: 0.25rem;
    position: relative;
  }
  .ftp .bar {
    width: min(100%, 24px);
    background: var(--text-muted);
    border-radius: 4px 4px 0 0;
    order: 2;
  }
  .ftp li:last-child .bar {
    background: var(--text);
  }
  .ftp .v {
    order: 1;
    font-size: var(--size-small);
  }
  .ftp .m {
    position: absolute;
    bottom: -1.4rem;
    font-size: var(--size-label);
    color: var(--text-muted);
  }
  .lifts {
    list-style: none;
    margin: var(--space-6) 0 0;
    padding: 0;
  }
  .block:has(.ftp) {
    padding-bottom: var(--space-5);
  }
  .block:has(.ftp) + .block .lifts,
  .lifts {
    margin-top: 0;
  }
  .lifts li {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 96px 7rem;
    align-items: center;
    gap: var(--space-3);
    padding: var(--space-2) 0;
    border-bottom: 1px solid var(--line);
  }
  .l-name {
    font-weight: 600;
    min-width: 0;
  }
  .l-name .sess {
    display: block;
    font-weight: 400;
    font-size: var(--size-label);
  }
  .spark path {
    fill: none;
    stroke: var(--text-muted);
    stroke-width: 1.5;
  }
  .l-best {
    display: grid;
    grid-template-columns: auto auto 1fr;
    align-items: baseline;
    column-gap: 0.25rem;
    justify-items: start;
  }
  .l-best .num {
    font-size: var(--size-heading);
  }
  .pr {
    padding: 0 0.25rem;
    background: var(--signal);
    color: var(--on-signal);
    border-radius: 2px;
    font-size: var(--size-label);
    font-weight: 700;
  }
  .sub {
    grid-column: 1 / -1;
    font-size: var(--size-label);
  }
</style>
