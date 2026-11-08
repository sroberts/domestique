<script lang="ts">
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import ActionBar from '../components/ActionBar.svelte';
  import ActivityRow from '../components/ActivityRow.svelte';
  import GearReminder from '../components/GearReminder.svelte';
  import AskLink from '../components/AskLink.svelte';
  import SyncStatus from '../components/SyncStatus.svelte';
  import { AuthError, fetchToday, localDate } from '../lib/api';
  import { activeSession, cacheToday, cachedToday, sessionSets } from '../lib/db';
  import { day, duration, recoveryBand, time, weight } from '../lib/format';
  import { link, router } from '../lib/router.svelte';
  import { fetchTemplates } from '../lib/templates';
  import { beginSession, complete, estimateMinutes } from '../lib/workout.svelte';
  import type { LocalSession, Today } from '../lib/types';

  type Load = 'loading' | 'fresh' | 'stale' | 'none' | 'auth';

  const date = localDate();
  let today = $state<Today | null>(null);
  let load = $state<Load>('loading');
  let staleAt = $state<number | null>(null);
  let active = $state<LocalSession | null>(null);
  let activeDone = $state(0);
  let starting = $state(false);
  let slow = $state(false);

  // While Outpost's LLM writes today's call, look again a few times; the
  // rule-based call stands meanwhile, and for good if the model never answers.
  const RECHECK_MS = 20_000;
  const RECHECKS = 9;
  let rechecks = 0;
  let recheck: ReturnType<typeof setTimeout> | undefined;

  $effect(() => {
    const t = setTimeout(() => (slow = true), 400);
    void refresh();
    return () => {
      clearTimeout(t);
      clearTimeout(recheck);
    };
  });

  async function refresh() {
    const a = await activeSession();
    active = a ?? null;
    activeDone = a ? (await sessionSets(a.id)).length : 0;
    try {
      today = await fetchToday(date);
      load = 'fresh';
      clearTimeout(recheck);
      if (today.readiness.pending && rechecks < RECHECKS) {
        rechecks++;
        recheck = setTimeout(() => void refresh(), RECHECK_MS);
      }
      await cacheToday($state.snapshot(today) as Today);
      void fetchTemplates(); // keep the workout list on the phone for offline starts
    } catch (err) {
      const cached = await cachedToday(date);
      if (cached) {
        today = cached.value;
        staleAt = cached.at;
      }
      load = err instanceof AuthError ? 'auth' : cached ? 'stale' : 'none';
    }
  }

  let band = $derived(today?.recovery ? recoveryBand(today.recovery.score) : null);
  let plan = $derived(today?.plan ?? null);
  let totalSets = $derived(plan ? plan.exercises.reduce((n, e) => n + e.sets.length, 0) : 0);
  let ld = $derived(today?.load ?? null);
  // Today's planned session, already done.
  let doneToday = $derived(
    plan && today
      ? (today.recent.find((r) => r.template_id === plan.id && r.finished_at && complete(r.progress) && localDate(new Date(r.started_at)) === date) ?? null)
      : null,
  );
  let hasLoad = $derived(!!ld && ld.ctl > 1);

  // Monday to Sunday, today marked; days still to come drawn empty.
  let week = $derived.by(() => {
    const names = ['M', 'T', 'W', 'T', 'F', 'S', 'S'];
    const days = today?.week ?? [];
    const max = Math.max(60, ...days.map((d) => d.tss));
    return names.map((n, i) => ({ n, d: days[i], max, isToday: i === days.length - 1 }));
  });
  let weekTSS = $derived((today?.week ?? []).reduce((a, d) => a + d.tss, 0));
  let weekHours = $derived((today?.week ?? []).reduce((a, d) => a + d.hours, 0));

  async function start() {
    if (!plan || starting) return;
    starting = true;
    const s = await beginSession(plan);
    router.go(`/session/${s.id}`);
  }

  const totalOf = (s: LocalSession) => s.plan.reduce((n, e) => n + e.sets.length, 0);

  function summarizeSets(sets: { reps: number; weight_kg: number }[]): { reps: string; kg: string } {
    const reps = new Set(sets.map((s) => s.reps));
    const kgs = new Set(sets.map((s) => s.weight_kg));
    const repText = reps.size === 1 ? `${sets.length} × ${sets[0]!.reps}` : `${sets.length} sets`;
    const kgText = kgs.size === 1 ? weight(sets[0]!.weight_kg) : `${weight(Math.min(...kgs))}–${weight(Math.max(...kgs))}`;
    return { reps: repText, kg: kgText };
  }

  /** h:mm, rounding to the minute first so 7:59:40 reads 8:00, not 7:60. */
  function sleepText(seconds: number): string {
    const mins = Math.round(seconds / 60);
    return `${Math.floor(mins / 60)}:${String(mins % 60).padStart(2, '0')}`;
  }

  function formWord(tsb: number): string {
    if (tsb > 5) return 'fresh';
    if (tsb >= -10) return 'neutral';
    if (tsb >= -30) return 'building';
    return 'overreaching';
  }

  const segs = (d: Today['week'] extends (infer T)[] | null ? T : never) =>
    [
      { k: 'ride', v: d.ride_tss },
      { k: 'strength', v: d.strength_tss },
      { k: 'other', v: d.other_tss },
    ].filter((s) => s.v > 0);
</script>

<main class="today">
  <header class="top">
    <span class="wordmark">Domestique</span>
    <AskLink />
    <SyncStatus />
  </header>

  <p class="date mono">{day(new Date())}</p>

  {#if load === 'stale' && staleAt}
    <p class="notice">No connection. Showing today as of {time(staleAt)}.</p>
  {:else if load === 'auth'}
    <div class="notice">
      <p>Your Outpost sign-in has expired.</p>
      <button class="btn" onclick={() => location.reload()}>Sign in again</button>
    </div>
  {/if}

  {#if load === 'loading' && !today}
    {#if slow}<p class="muted loading">Loading today…</p>{/if}
  {:else if load === 'none'}
    <section class="empty">
      <h1>No connection</h1>
      <p class="muted">Domestique can't reach the Mac Mini and has nothing saved for today yet. Sessions you start still log on this phone and sync later.</p>
      <button class="btn" onclick={() => refresh()}>Try again</button>
    </section>
  {:else if today}
    <div class="cols">
      <div class="col-main">
    <!-- The call for the day, painted in the recovery colour like a race
         graphic: the score, then the verdict in one sentence. -->
    <section class="call band-{band ?? 'none'}" aria-labelledby="call-h">
      <p class="call-rec">
        <span class="call-k">Recovery</span>
        {#if today.recovery && band}
          <span class="num call-pct">{today.recovery.score}<span class="pct">%</span></span>
          <span class="visually-hidden"> ({band})</span>
        {:else}
          <span class="call-none">Not scored yet</span>
        {/if}
      </p>
      <h1 id="call-h" class="call-title">{today.readiness.title}</h1>
      <p class="call-detail">{today.readiness.detail}</p>
    </section>

    <section class="body" aria-label="Recovery and sleep">
      <dl class="vitals">
        {#if today.recovery}
          <div><dt class="label">HRV</dt><dd><span class="num">{today.recovery.hrv_ms}</span><span class="unit">ms</span></dd></div>
          <div><dt class="label">Resting</dt><dd><span class="num">{today.recovery.resting_hr}</span><span class="unit">bpm</span></dd></div>
        {/if}
        {#if today.sleep}
          <div><dt class="label">Sleep</dt><dd><span class="num">{sleepText(today.sleep.asleep_s)}</span>{#if today.sleep.performance != null}<span class="unit">{today.sleep.performance}%</span>{/if}</dd></div>
        {/if}
      </dl>
    </section>
    {#if plan}
      <section class="plan" aria-labelledby="plan-h">
        <div class="plan-head">
          <h2 id="plan-h" class="plan-name">{plan.name}</h2>
          <a class="more" href="/workouts" use:link aria-label="Change today's workout">Change</a>
        </div>
        <p class="mono plan-meta">{plan.exercises.length} lifts · {totalSets} sets · about {estimateMinutes(plan.exercises)} min</p>
        <ol class="sheet">
          {#each plan.exercises as ex, i (i)}
            {@const s = summarizeSets(ex.sets)}
            <li>
              <span class="ex-name">{ex.name}</span>
              <span class="num ex-reps">{s.reps}</span>
              <span class="ex-kg"><span class="num">{s.kg}</span>{#if s.kg !== 'BW'}<span class="unit">kg</span>{/if}</span>
              {#if ex.change}
                <span class="ex-change" class:up={ex.change.startsWith('Up')}>{ex.change}</span>
              {:else if ex.notes}
                <span class="ex-notes muted">{ex.notes}</span>
              {/if}
            </li>
          {/each}
        </ol>
      </section>
    {:else}
      <section class="plan empty-plan" aria-labelledby="plan-h">
        <h2 id="plan-h" class="plan-name">No lift planned today</h2>
        <p class="muted small">Rest, or pick a workout to do today.</p>
        <a class="btn pick" href="/workouts" use:link>Choose a workout</a>
      </section>
    {/if}

      </div>
      <div class="col-side">

    {#if hasLoad && ld}
      <section class="form" aria-labelledby="form-h">
        <h2 id="form-h" class="sec-h">Training load <a class="more" href="/trends" use:link>Trends <ChevronRight size={16} aria-hidden="true" /></a></h2>
        <dl class="figs">
          <div><dt class="label">Fitness</dt><dd><span class="num">{Math.round(ld.ctl)}</span>{#if today.ramp_ctl}<span class="unit">{today.ramp_ctl > 0 ? '+' : ''}{today.ramp_ctl} in 7 days</span>{/if}</dd></div>
          <div><dt class="label">Fatigue</dt><dd><span class="num">{Math.round(ld.atl)}</span></dd></div>
          <div><dt class="label">Form</dt><dd><span class="num">{ld.tsb > 0 ? '+' : ''}{Math.round(ld.tsb)}</span><span class="state st-{formWord(ld.tsb)}">{formWord(ld.tsb)}</span></dd></div>
        </dl>
        <div class="week" role="img" aria-label={`This week so far: ${Math.round(weekTSS)} TSS over ${weekHours.toFixed(1)} hours`}>
          {#each week as w, i (i)}
            <div class="wday" class:now={w.isToday} class:future={!w.d}>
              <div class="wbar">
                {#if w.d}
                  {#each segs(w.d) as s (s.k)}
                    <span class="seg seg-{s.k}" style:height={`${(s.v / w.max) * 100}%`}></span>
                  {/each}
                {/if}
              </div>
              <span class="mono wn">{w.n}</span>
            </div>
          {/each}
          <p class="mono wsum"><span class="label">Week</span><span class="num">{Math.round(weekTSS)}</span>TSS · {weekHours.toFixed(1)} h</p>
        </div>
        <p class="legend small">
          <span><span class="lk seg-ride" aria-hidden="true"></span>Rides</span>
          <span><span class="lk seg-strength" aria-hidden="true"></span>Strength</span>
          <span><span class="lk seg-other" aria-hidden="true"></span>Other</span>
        </p>
      </section>
    {/if}

    {#if today.latest.length > 0}
      <section class="recent" aria-labelledby="recent-h">
        <h2 id="recent-h" class="sec-h">Latest <a class="more" href="/activities" use:link>All activities <ChevronRight size={16} aria-hidden="true" /></a></h2>
        {#each today.latest.slice(0, 3) as a (a.id)}
          <ActivityRow {a} />
        {/each}
      </section>
    {/if}
    <GearReminder />
      </div>
    </div>
  {/if}
</main>

{#if active}
  <ActionBar column>
    <a class="btn btn-primary" href={`/session/${active.id}`} use:link>
      Resume {active.name} · set {Math.min(activeDone + 1, totalOf(active))} of {totalOf(active)}
    </a>
  </ActionBar>
{:else if plan && doneToday}
  <ActionBar column>
    <a class="btn btn-primary" href={`/session/${doneToday.id}/summary`} use:link>{plan.name} done · View summary</a>
    <button class="btn btn-quiet again" onclick={start} disabled={starting}>Start {plan.name} again</button>
  </ActionBar>
{:else if plan}
  <ActionBar column>
    <button class="btn btn-primary" onclick={start} disabled={starting}>Start {plan.name}</button>
  </ActionBar>
{/if}

<style>
  .today {
    max-width: var(--page-narrow);
    margin: 0 auto;
    padding: calc(env(safe-area-inset-top) + var(--space-2)) var(--gutter) 0;
  }
  .top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
  }
  .wordmark {
    font-family: var(--font-num);
    font-weight: 700;
    font-size: 1.25rem;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }
  .date {
    margin-top: var(--space-2);
    font-size: var(--size-small);
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .notice {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    margin-top: var(--space-4);
    padding: var(--space-3) var(--space-4);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    font-size: var(--size-small);
  }
  .loading {
    margin-top: var(--space-6);
  }
  .small {
    font-size: var(--size-small);
  }
  .unit {
    margin-left: 0.25rem;
    font-size: var(--size-label);
    color: var(--text-muted);
  }

  /* Phone: one column, plan first. iPad/Mac: verdict and plan beside the body. */
  @media (min-width: 900px) {
    .today {
      max-width: var(--page-wide);
    }
    .cols {
      display: grid;
      grid-template-columns: minmax(0, 1.1fr) minmax(0, 1fr);
      gap: var(--space-7);
      align-items: start;
    }
    .col-side > :first-child {
      margin-top: var(--space-5);
    }
  }

  /* The call: a solid slab in the recovery colour, dark ink on it, like a
     race graphic's caption. No score yet: a quiet raised slab. */
  .call {
    margin-top: var(--space-4);
    padding: var(--space-4) var(--space-4) var(--space-5);
    border-radius: var(--radius-m);
    background: var(--surface-raised);
    color: var(--text);
    transition: background-color var(--dur-state) var(--ease-out);
  }
  .band-green {
    background: var(--rec-green);
    color: var(--on-color);
  }
  .band-yellow {
    background: var(--rec-amber);
    color: var(--on-color);
  }
  .band-red {
    background: var(--rec-red);
    color: var(--on-color);
  }
  .call-rec {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-3);
  }
  .call-k {
    font-size: var(--size-label);
    font-weight: 700;
    letter-spacing: var(--tracking-label);
    text-transform: uppercase;
  }
  .call-pct {
    font-size: var(--size-live);
    font-weight: 700;
    line-height: 0.82;
  }
  .call-pct .pct {
    font-size: var(--size-target);
    font-weight: 600;
    margin-left: 0.05em;
  }
  .call-none {
    font-size: var(--size-small);
    color: var(--text-muted);
  }
  .call-title {
    margin-top: var(--space-3);
    font-size: var(--size-heading);
  }
  .call-detail {
    margin-top: var(--space-1);
    max-width: 60ch;
  }

  /* Body */
  .body {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    justify-content: space-between;
    gap: var(--space-4);
    margin-top: var(--space-4);
  }
  .again {
    min-height: 2.75rem;
  }
  .vitals {
    display: flex;
    gap: var(--space-5);
    margin: 0 0 var(--space-2);
  }
  .vitals div {
    display: grid;
    gap: var(--space-1);
  }
  .vitals dd {
    margin: 0;
    white-space: nowrap;
  }
  .vitals .num {
    font-size: var(--size-heading);
  }

  /* Load */
  .form {
    margin-top: var(--space-6);
  }
  .sec-h {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    font-size: var(--size-body);
    padding-bottom: var(--space-2);
    border-bottom: 1px solid var(--line-strong);
  }
  .more {
    display: inline-flex;
    align-items: center;
    gap: 0.125rem;
    font-size: var(--size-small);
    font-weight: 600;
    color: var(--text-muted);
    text-decoration: none;
    min-height: 2.75rem;
    margin: -0.5rem 0;
  }
  .figs {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    margin: var(--space-3) 0 0;
  }
  .figs div {
    display: grid;
    gap: var(--space-1);
    align-content: start;
  }
  .figs dd {
    margin: 0;
    display: grid;
    gap: var(--space-1);
  }
  .figs .num {
    font-size: var(--size-target);
  }
  .figs .unit {
    margin: 0;
  }
  .week {
    display: grid;
    grid-template-columns: repeat(7, minmax(0, 1fr)) auto;
    align-items: end;
    gap: var(--space-2);
    margin-top: var(--space-5);
  }
  .wday {
    display: grid;
    justify-items: center;
    gap: var(--space-1);
  }
  .wbar {
    display: flex;
    flex-direction: column-reverse;
    gap: 2px;
    width: min(100%, 24px);
    height: 4rem;
    border-bottom: 1px solid var(--line-strong);
  }
  .seg {
    display: block;
    width: 100%;
    min-height: 2px;
  }
  .seg:last-child {
    border-radius: 4px 4px 0 0;
  }
  .seg-ride {
    background: var(--sport-ride);
  }
  .seg-strength {
    background: var(--sport-strength);
  }
  .seg-other {
    background: var(--sport-other);
  }
  .wn {
    font-size: var(--size-label);
    color: var(--text-muted);
  }
  .wday.now .wn {
    color: var(--text);
    font-weight: 500;
    text-decoration: underline;
    text-underline-offset: 3px;
  }
  .wday.future .wbar {
    border-bottom-style: dashed;
  }
  .wsum {
    align-self: center;
    padding-left: var(--space-2);
    font-size: var(--size-label);
    color: var(--text-muted);
    text-align: right;
  }
  .wsum .label {
    display: block;
  }
  .wsum .num {
    display: block;
    font-size: var(--size-heading);
    color: var(--text);
  }
  .legend {
    display: flex;
    gap: var(--space-4);
    margin-top: var(--space-2);
    color: var(--text-muted);
  }
  .legend span {
    display: inline-flex;
    align-items: center;
    gap: var(--space-1);
  }
  .lk {
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 2px;
  }

  /* Plan */
  .plan {
    margin-top: var(--space-5);
  }
  .plan-name {
    font-size: var(--size-heading);
  }
  .plan-meta {
    margin-top: var(--space-1);
    font-size: var(--size-small);
    color: var(--text-muted);
  }
  .sheet {
    list-style: none;
    margin: var(--space-3) 0 0;
    padding: 0;
    border-top: 1px solid var(--line-strong);
  }
  .sheet li {
    display: grid;
    grid-template-columns: 1fr auto 5rem;
    align-items: baseline;
    column-gap: var(--space-4);
    padding: var(--space-3) 0;
    border-bottom: 1px solid var(--line);
  }
  .ex-name {
    font-weight: 600;
  }
  .ex-reps,
  .ex-kg .num {
    font-size: var(--size-heading);
  }
  .ex-kg {
    text-align: right;
    white-space: nowrap;
  }
  .ex-notes,
  .ex-change {
    grid-column: 1 / -1;
    font-size: var(--size-small);
    margin-top: var(--space-1);
  }
  .ex-change {
    color: var(--text-muted);
  }
  .ex-change.up::before {
    content: '▲ ';
    color: var(--signal);
    font-size: 0.7em;
  }
  .plan-head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: var(--space-3);
  }
  .pick {
    margin-top: var(--space-3);
  }
  .empty-plan .small {
    margin-top: var(--space-2);
  }
  .empty {
    display: grid;
    gap: var(--space-3);
    justify-items: start;
    margin-top: var(--space-6);
  }
  .empty h1 {
    font-size: var(--size-heading);
  }
  .recent {
    margin-top: var(--space-6);
  }
</style>
