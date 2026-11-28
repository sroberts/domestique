<script lang="ts">
  // Sleep: last night against need and the athlete's usual, then the last
  // 2, 4 or 12 weeks: time asleep, when they slept, the week's debt, and how
  // sleep lines up with recovery and training. Numbers only; nothing here
  // claims a cause.
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import SleepNights from '../components/SleepNights.svelte';
  import SleepStages from '../components/SleepStages.svelte';
  import SleepWindow from '../components/SleepWindow.svelte';
  import SyncStatus from '../components/SyncStatus.svelte';
  import { fetchSleep } from '../lib/data';
  import { day, hm, time } from '../lib/format';
  import { backTarget, link } from '../lib/router.svelte';
  import type { SleepCompare, SleepTrends } from '../lib/types';

  const back = backTarget({ href: '/', label: 'Today' });
  const ranges = [
    { weeks: 2, label: '2 weeks' },
    { weeks: 4, label: '4 weeks' },
    { weeks: 12, label: '12 weeks' },
  ];
  let weeks = $state(2);
  let data = $state<SleepTrends | null>(null);
  let stale = $state<number | null>(null);
  let failed = $state(false);

  $effect(() => {
    const w = weeks;
    fetchSleep(w).then((r) => {
      if (r.data) data = r.data;
      stale = r.stale;
      failed = !r.data;
    });
  });

  let last = $derived(data?.last ?? null);
  let need = $derived(last?.need ? last.need.baseline_s + last.need.debt_s + last.need.strain_s + last.need.nap_s : 0);
  let baseline = $derived(last?.need?.baseline_s ?? 0);
  /** Why tonight's need differs from usual: only the parts worth a mention (5 min or more). */
  let needWhy = $derived.by(() => {
    const n = last?.need;
    if (!n) return '';
    const parts = [`Your usual need is ${hm(n.baseline_s)}`];
    if (n.debt_s >= 300) parts.push(`plus ${hm(n.debt_s)} for recent short nights`);
    if (n.strain_s >= 300) parts.push(`${n.debt_s >= 300 ? '' : 'plus '}${hm(n.strain_s)} for training`);
    if (n.nap_s <= -300) parts.push(`less ${hm(-n.nap_s)} for a nap`);
    return `${parts.join(', ')}.`;
  });
  const dayOf = (d: string) => day(new Date(`${d}T12:00`).getTime());
  const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;
  /** "3 and 12 so far": what a comparison still waits for. */
  const sofar = (c: SleepCompare) => `${c.with.nights} and ${c.without.nights} so far`;
  let readyEvenings = $derived(data?.evenings.filter((c) => c.ready) ?? []);
  let waiting = $derived(data?.evenings.filter((c) => !c.ready) ?? []);
</script>

<main class="page">
  <header class="top">
    <a class="btn btn-quiet back" href={back.href} use:link><ChevronLeft size={20} aria-hidden="true" /> {back.label}</a>
    <SyncStatus />
  </header>
  <h1>Sleep</h1>
  {#if stale}<p class="notice">No connection. Showing sleep as of {time(stale)}.</p>{/if}

  {#if failed && !data}
    <section class="empty">
      <h2>Couldn't load sleep</h2>
      <p class="muted">The Mac Mini didn't answer, and there's no earlier copy on this device.</p>
    </section>
  {:else if data && !last}
    <section class="empty">
      <h2>No sleep yet</h2>
      <p class="muted">Sleep comes from Whoop. Connect it in Settings, and each night appears here the morning after.</p>
      <a class="btn" href="/settings" use:link>Connect Whoop</a>
    </section>
  {:else if data && last}
    <p class="mono meta">
      {[`Night to ${dayOf(last.date)}`, `${time(last.start_at)}–${time(last.end_at)}`, last.performance != null ? `${last.performance}% of need (Whoop)` : ''].filter(Boolean).join(' · ')}
    </p>

    <section class="block night" aria-labelledby="night-h">
      <h2 id="night-h" class="visually-hidden">Last night</h2>
      <p class="headline">
        <span class="num big">{hm(last.asleep_s)}</span>
        <span class="unit">asleep</span>
        {#if need}<span class="num of">of {hm(need)}</span><span class="unit">needed</span>{/if}
      </p>
      {#if need && baseline}
        <p class="small muted need-why">{needWhy}</p>
      {/if}
      <SleepStages night={last} norms={data.norms} />
      {#if last.efficiency != null || last.respiratory_rate != null || last.disturbances != null || last.cycles != null}
      <dl class="stats">
        {#if last.efficiency != null}
          <div><dt class="label">Efficiency</dt><dd><span class="num">{last.efficiency}</span><span class="unit">%</span>{#if data.norms.efficiency != null}<span class="mono muted usual">usual {data.norms.efficiency}%</span>{/if}</dd></div>
        {/if}
        {#if last.respiratory_rate != null}
          <div><dt class="label">Breathing</dt><dd><span class="num">{last.respiratory_rate.toFixed(1)}</span><span class="unit">/min</span>{#if data.norms.respiratory_rate != null}<span class="mono muted usual">usual {data.norms.respiratory_rate.toFixed(1)}</span>{/if}</dd></div>
        {/if}
        {#if last.disturbances != null}
          <div><dt class="label">Disturbances</dt><dd><span class="num">{last.disturbances}</span></dd></div>
        {/if}
        {#if last.cycles != null}
          <div><dt class="label">Cycles</dt><dd><span class="num">{last.cycles}</span></dd></div>
        {/if}
      </dl>
      {/if}
      {#each data.last_naps as nap (nap.id)}
        <p class="small nap"><span class="label">Nap</span> {time(nap.start_at)}–{time(nap.end_at)} · {hm(nap.asleep_s)} asleep</p>
      {/each}
    </section>

    <div class="range">
      <div class="seg" role="radiogroup" aria-label="Range">
        {#each ranges as r (r.weeks)}
          <button role="radio" aria-checked={weeks === r.weeks} class:on={weeks === r.weeks} onclick={() => (weeks = r.weeks)}>{r.label}</button>
        {/each}
      </div>
    </div>

    {#if data.nights.length > 1}
      <section class="block" aria-labelledby="asleep-h">
        <h2 id="asleep-h" class="block-h">
          Asleep against need
          <span class="mono muted">{hm(data.avg_asleep_s)} a night{#if data.avg_need_s}, {hm(data.avg_need_s)} needed{/if}</span>
        </h2>
        <SleepNights nights={data.nights} />
      </section>

      <section class="block" aria-labelledby="window-h">
        <h2 id="window-h" class="block-h">Bed and wake</h2>
        <dl class="stats two">
          <div><dt class="label">Usual bedtime</dt><dd><span class="num">{data.bed.median}</span><span class="mono muted usual">give or take {data.bed.spread_min} min</span></dd></div>
          <div><dt class="label">Usual wake</dt><dd><span class="num">{data.wake.median}</span><span class="mono muted usual">give or take {data.wake.spread_min} min</span></dd></div>
        </dl>
        <SleepWindow nights={data.nights} bed={data.bed.median} wake={data.wake.median} />
      </section>
    {/if}

    <section class="block" aria-labelledby="debt-h">
      <h2 id="debt-h" class="block-h">Sleep debt <span class="mono muted">last 7 nights</span></h2>
      {#if data.debt.nights === 0}
        <p class="small muted">Whoop hasn't scored a sleep need for these nights yet.</p>
      {:else if data.debt.s > 0}
        <p class="headline"><span class="num big">{hm(data.debt.s)}</span><span class="unit">short</span></p>
        <p class="small muted">{baseline ? `Against your usual need of ${hm(baseline)} a night` : 'Against your usual need'}, over {plural(data.debt.nights, 'night')}. A long night pays some back.</p>
      {:else}
        <p class="small">Nothing owed: the last {plural(data.debt.nights, 'night')} met your usual need.</p>
      {/if}
    </section>

    <section class="block" aria-labelledby="cmp-h">
      <h2 id="cmp-h" class="block-h">Sleep and recovery <span class="mono muted">last year</span></h2>
      <dl class="cmp">
        <div class="row">
          <dt>Recovery the morning after a night more than an hour short</dt>
          {#if data.short_nights.ready}
            <dd>
              <span><span class="num">{data.short_nights.with.median}</span><span class="unit">%</span> <span class="mono muted">{plural(data.short_nights.with.nights, 'night')}</span></span>
              <span class="vs muted">against</span>
              <span><span class="num">{data.short_nights.without.median}</span><span class="unit">%</span> <span class="mono muted">{plural(data.short_nights.without.nights, 'other night')}</span></span>
            </dd>
          {:else}
            <dd class="small muted">Shows once there are 5 nights each way ({sofar(data.short_nights)}).</dd>
          {/if}
        </div>
        <div class="row">
          <dt>Time asleep after a heavy training day</dt>
          {#if data.heavy_days.ready}
            <dd>
              <span><span class="num">{data.heavy_days.with.median.toFixed(1)}</span><span class="unit">h</span> <span class="mono muted">{plural(data.heavy_days.with.nights, 'night')}</span></span>
              <span class="vs muted">against</span>
              <span><span class="num">{data.heavy_days.without.median.toFixed(1)}</span><span class="unit">h</span> <span class="mono muted">{plural(data.heavy_days.without.nights, 'other night')}</span></span>
            </dd>
          {:else}
            <dd class="small muted">Shows once there are 5 nights each way ({sofar(data.heavy_days)}).</dd>
          {/if}
        </div>
      </dl>
      <p class="small muted foot">
        Medians. Short means more than an hour under your usual need; heavy means a day's load at least {data.heavy_day_load}× your fitness. They show what goes together, not what causes what.
      </p>
    </section>

    <section class="block" aria-labelledby="ev-h">
      <h2 id="ev-h" class="block-h">Evenings <span class="mono muted">{plural(data.evenings_logged, 'logged evening')}, last year</span></h2>
      {#if data.evenings_logged === 0}
        <p class="small muted">Log your evenings on Today, from 6 pm. Each thing shows here once it has {data.min_evening} logged evenings with it and {data.min_evening} without.</p>
      {:else}
        {#each readyEvenings as c (c.key)}
          <table class="ev">
            <caption>The night after: {c.label.toLowerCase()}</caption>
            <thead>
              <tr><th scope="col"><span class="visually-hidden">Measure</span></th><th scope="col">With <span class="mono">{c.with.nights}</span></th><th scope="col">Without <span class="mono">{c.without.nights}</span></th></tr>
            </thead>
            <tbody>
              <tr><th scope="row">Asleep</th><td><span class="num">{c.with.asleep_h.toFixed(1)}</span><span class="unit">h</span></td><td><span class="num">{c.without.asleep_h.toFixed(1)}</span><span class="unit">h</span></td></tr>
              {#if c.with.efficiency != null && c.without.efficiency != null}
                <tr><th scope="row">Efficiency</th><td><span class="num">{c.with.efficiency}</span><span class="unit">%</span></td><td><span class="num">{c.without.efficiency}</span><span class="unit">%</span></td></tr>
              {/if}
              {#if c.with.recovery != null && c.without.recovery != null}
                <tr><th scope="row">Recovery</th><td><span class="num">{c.with.recovery}</span><span class="unit">%</span></td><td><span class="num">{c.without.recovery}</span><span class="unit">%</span></td></tr>
              {/if}
            </tbody>
          </table>
        {/each}
        {#if waiting.length}
          <p class="small muted gathering">
            Still gathering ({data.min_evening} logged evenings each way): {waiting.map((c) => `${c.label.toLowerCase()} ${Math.min(c.with.nights, c.without.nights)} of ${data!.min_evening}`).join(', ')}.
          </p>
        {/if}
        <p class="small muted foot">Only evenings you logged count; one you didn't is unknown, not clear. Late training is filled in from your workouts: one ending within three hours of bed.</p>
      {/if}
    </section>
  {/if}
</main>

<style>
  .page {
    max-width: var(--page-list);
    margin: 0 auto;
    padding: calc(env(safe-area-inset-top) + var(--space-2)) var(--gutter) 0;
  }
  .top {
    display: flex;
    justify-content: space-between;
    align-items: center;
    min-height: 2.75rem;
  }
  .back {
    margin-left: calc(var(--space-2) * -1);
  }
  h1 {
    margin-top: var(--space-2);
    font-size: var(--size-heading);
  }
  .meta {
    margin-top: var(--space-1);
    font-size: var(--size-small);
    color: var(--text-muted);
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
    padding-top: var(--space-4);
    border-top: 1px solid var(--line-strong);
  }
  .night {
    margin-top: var(--space-4);
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
  .block-h .mono {
    font-size: var(--size-label);
    font-weight: 400;
  }
  .headline {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    column-gap: var(--space-2);
  }
  .big {
    font-size: var(--size-target);
    line-height: 0.9;
  }
  .of {
    margin-left: var(--space-2);
    font-size: var(--size-stat);
    color: var(--text-muted);
  }
  .unit {
    font-size: var(--size-small);
    color: var(--text-muted);
  }
  .need-why {
    margin: var(--space-2) 0 0;
    max-width: 62ch;
  }
  .small {
    font-size: var(--size-small);
  }
  .stats {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-3) var(--space-4);
    margin: var(--space-4) 0 0;
    padding: var(--space-3) 0;
    border-top: 1px solid var(--line);
    border-bottom: 1px solid var(--line);
  }
  @media (min-width: 640px) {
    .stats:not(.two) {
      grid-template-columns: repeat(4, minmax(0, 1fr));
    }
  }
  .stats.two {
    margin: 0 0 var(--space-4);
  }
  .stats dd {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    column-gap: var(--space-1);
    margin: var(--space-1) 0 0;
  }
  .stats .num {
    font-size: var(--size-stat);
    line-height: 1;
  }
  .usual {
    flex-basis: 100%;
    font-size: var(--size-label);
  }
  .nap {
    margin-top: var(--space-3);
  }
  .nap .label {
    margin-right: var(--space-2);
  }
  .range {
    margin-top: var(--space-6);
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
    font: inherit;
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
  .seg button:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: -2px;
  }
  .cmp {
    margin: 0;
  }
  .row {
    padding: var(--space-3) 0;
    border-bottom: 1px solid var(--line);
  }
  .row:first-child {
    padding-top: 0;
  }
  .row dt {
    font-size: var(--size-small);
  }
  .row dd {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-1) var(--space-3);
    margin: var(--space-2) 0 0;
  }
  .row .num {
    font-size: var(--size-stat);
    line-height: 1;
  }
  .row .mono {
    font-size: var(--size-label);
  }
  .vs {
    font-size: var(--size-small);
  }
  /* One small table per thing logged: with and without side by side. */
  .ev {
    width: 100%;
    border-collapse: collapse;
    margin-bottom: var(--space-4);
    font-variant-numeric: tabular-nums;
  }
  .ev caption {
    caption-side: top;
    text-align: left;
    font-size: var(--size-small);
    font-weight: 600;
    padding-bottom: var(--space-2);
  }
  .ev th,
  .ev td {
    padding: var(--space-1) 0;
    border-bottom: 1px solid var(--line);
    text-align: right;
    font-weight: 400;
  }
  .ev thead th {
    font-size: var(--size-label);
    color: var(--text-muted);
    width: 6.5rem;
  }
  .ev thead th:first-child {
    width: auto;
  }
  .ev tbody th {
    text-align: left;
    font-size: var(--size-small);
    color: var(--text-muted);
  }
  .ev .num {
    font-size: var(--size-lead);
  }
  .gathering {
    margin-top: var(--space-3);
    max-width: 62ch;
  }
  .foot {
    margin-top: var(--space-3);
    max-width: 62ch;
  }
</style>
