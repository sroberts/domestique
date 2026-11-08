<script lang="ts">
  import { untrack } from 'svelte';
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import ActionBar from '../components/ActionBar.svelte';
  import HrAlignment from '../components/HrAlignment.svelte';
  import ExportMenu from '../components/ExportMenu.svelte';
  import HrTrace from '../components/HrTrace.svelte';
  import Stepper from '../components/Stepper.svelte';
  import SyncStatus from '../components/SyncStatus.svelte';
  import ZoneBar from '../components/ZoneBar.svelte';
  import { AuthError, HTTPError, OfflineError, fetchSummary } from '../lib/api';
  import { sessionExports } from '../lib/export';
  import { clock, day, duration, setText, thousands, time, weight } from '../lib/format';
  import { backTarget, link } from '../lib/router.svelte';
  import { Workout, earlyText, endedEarly, progressOf, roundsOf, type CircuitPlace } from '../lib/workout.svelte';
  import type { SetRecord, SetStat, Summary } from '../lib/types';

  let { id }: { id: string } = $props();
  // Back goes where you came from; on a direct load, to this tab's list.
  const back = backTarget({ href: '/activities', label: 'Activities' });

  let summary = $state<Summary | null>(null);
  // "Done" ends a session just finished; elsewhere it would be a second Back.
  // (The screen is keyed by id, so reading it once is correct.)
  // svelte-ignore state_referenced_locally
  const justFinished = consumeJustFinished(id);
  function consumeJustFinished(sid: string): boolean {
    try {
      if (sessionStorage.getItem('domestique:just-finished') !== sid) return false;
      sessionStorage.removeItem('domestique:just-finished');
      return true;
    } catch {
      return false;
    }
  }
  let source = $state<'server' | 'local' | null>(null);
  let problem = $state<'offline' | 'auth' | 'unsynced' | 'missing' | null>(null);
  let workout = $state<Workout | null>(null);
  let selected = $state<string | null>(null);
  let editing = $state<string | null>(null);

  /** Build what we can from the device when the server can't help. */
  function localSummary(w: Workout): Summary {
    const sets = w.sets;
    const end = w.session.finished_at ?? sets.at(-1)?.ended_at ?? w.session.started_at;
    return {
      session: w.session,
      sets,
      hr: [],
      hr_status: 'pending',
      max_hr: 190,
      zones: [95, 114, 133, 152, 171],
      duration_s: Math.round((end - w.session.started_at) / 1000),
      volume_kg: sets.reduce((v, s) => v + s.reps * s.weight_kg, 0),
      total_reps: sets.reduce((v, s) => v + s.reps, 0),
      peak_bpm: 0,
      avg_bpm: 0,
      zone_seconds: [0, 0, 0, 0, 0, 0],
      progress: progressOf(w.session.plan, sets),
      set_stats: sets.map((s, i) => ({
        set_id: s.id,
        has_hr: false,
        peak_bpm: 0,
        avg_bpm: 0,
        zone: 0,
        recovery_drop: 0,
        recovery_window_s: 0,
        rest_s: sets[i + 1] ? Math.round((sets[i + 1]!.started_at - s.ended_at) / 1000) : 0,
      })),
    };
  }

  async function load() {
    workout ??= await Workout.load(id);
    try {
      const s = await fetchSummary(id);
      // Local edits that haven't reached the server yet win.
      if (workout) {
        const local = new Map(workout.sets.map((x) => [x.id, x]));
        s.sets = s.sets.map((x) => {
          const l = local.get(x.id);
          return l && l.updated_at > x.updated_at ? { ...l } : x;
        });
        for (const l of workout.sets) if (!s.sets.some((x) => x.id === l.id)) s.sets.push({ ...l });
        s.sets.sort((a, b) => a.started_at - b.started_at);
        recompute(s);
        s.progress = progressOf(s.session.plan, s.sets);
      }
      summary = s;
      source = 'server';
      problem = null;
    } catch (err) {
      if (workout) {
        summary = localSummary(workout);
        source = 'local';
      }
      problem =
        err instanceof AuthError ? 'auth'
        : err instanceof OfflineError ? 'offline'
        : err instanceof HTTPError && err.status === 404 ? (workout ? 'unsynced' : 'missing')
        : 'offline';
    }
  }

  function recompute(s: Summary) {
    s.volume_kg = s.sets.reduce((v, x) => v + x.reps * x.weight_kg, 0);
    s.total_reps = s.sets.reduce((v, x) => v + x.reps, 0);
  }

  $effect(() => {
    // untrack: load() reads `workout` before its first await, and assigning it
    // would otherwise re-run this effect (a second fetch, a new interval).
    untrack(() => void load());
    // Poll while heart rate hasn't arrived, and only while the screen is visible.
    const t = setInterval(() => {
      if (document.visibilityState === 'visible' && (summary?.hr_status === 'pending' || problem)) void load();
    }, 10_000);
    return () => clearInterval(t);
  });

  let stats = $derived(new Map<string, SetStat>((summary?.set_stats ?? []).map((s) => [s.set_id, s])));
  let places = $derived(summary ? roundsOf(summary.session.plan, summary.sets).places : new Map<string, CircuitPlace>());
  // Straight sets group by lift; a circuit groups by round, as it was done.
  let groups = $derived.by(() => {
    const out: { name: string; sets: SetRecord[] }[] = [];
    for (const s of summary?.sets ?? []) {
      const p = places.get(s.id);
      const name = p ? `Circuit ${p.letter} · Round ${p.round} of ${p.rounds}` : s.exercise_name;
      if (!out.length || out.at(-1)!.name !== name) out.push({ name, sets: [] });
      out.at(-1)!.sets.push(s);
    }
    return out;
  });
  let sel = $derived(summary?.sets.find((s) => s.id === selected) ?? null);
  let selStat = $derived(sel ? stats.get(sel.id) : undefined);
  let end = $derived(summary ? (summary.session.finished_at ?? summary.sets.at(-1)?.ended_at ?? summary.session.started_at) : 0);

  async function step(s: SetRecord, field: 'reps' | 'weight_kg', dir: -1 | 1) {
    if (!workout) return;
    const kgStep = s.weight_kg + (dir < 0 ? -0.01 : 0) < 20 ? 1 : 2.5;
    const reps = field === 'reps' ? s.reps + dir : s.reps;
    const kg = field === 'weight_kg' ? s.weight_kg + dir * kgStep : s.weight_kg;
    await workout.editSet(s.id, reps, Math.max(0, kg));
    const updated = workout.sets.find((x) => x.id === s.id);
    if (updated && summary) {
      s.reps = updated.reps;
      s.weight_kg = updated.weight_kg;
      s.updated_at = updated.updated_at;
      recompute(summary);
    }
  }
</script>

<main class="summary">
  <header class="top">
    <a class="btn btn-quiet back" href={back.href} use:link><ChevronLeft size={20} aria-hidden="true" /> {back.label}</a>
    <span class="top-end">
      <SyncStatus />
      {#if summary && source === 'server'}<ExportMenu items={sessionExports(summary.session.id, summary.hr_status === 'ready')} />{/if}
    </span>
  </header>

  {#if !summary && problem === 'missing'}
    <section class="gone">
      <h1>Session not found</h1>
      <p class="muted">It isn't on this device or on the Mac Mini.</p>
    </section>
  {:else if !summary && problem}
    <section class="gone">
      <h1>{problem === 'auth' ? 'Sign in to see this session' : 'No connection'}</h1>
      <p class="muted">{problem === 'auth' ? 'Your Outpost sign-in has expired.' : "This session wasn't logged on this device, so it needs the Mac Mini."}</p>
      <button class="btn" onclick={() => (problem === 'auth' ? location.reload() : load())}>{problem === 'auth' ? 'Sign in again' : 'Try again'}</button>
    </section>
  {:else if summary}
    <h1 class="name">{summary.session.name}</h1>
    <p class="mono meta">
      {day(summary.session.started_at)} · {time(summary.session.started_at)}–{time(end)} · {duration(summary.duration_s)}
    </p>
    {#if endedEarly(summary.progress)}
      <p class="early">{earlyText(summary.progress)}</p>
    {/if}

    {#if source === 'local'}
      <p class="notice">
        {#if problem === 'unsynced'}Still syncing to the Mac Mini. These are the sets on this phone.
        {:else if problem === 'auth'}Sign in again to sync and fetch heart rate. These are the sets on this phone.
        {:else}No connection. These are the sets on this phone; heart rate needs the Mac Mini.{/if}
      </p>
    {/if}

    <dl class="strip">
      <div><dt class="label">Volume</dt><dd><span class="num">{thousands(summary.volume_kg)}</span> <span class="unit">kg</span></dd></div>
      <div><dt class="label">Sets</dt><dd><span class="num">{summary.sets.length}</span></dd></div>
      <div><dt class="label">Peak</dt><dd>{#if summary.peak_bpm}<span class="num">{summary.peak_bpm}</span> <span class="unit">bpm</span>{:else}<span class="num dim">–</span>{/if}</dd></div>
      <div><dt class="label">Strain</dt><dd>{#if summary.session.strain != null}<span class="num">{summary.session.strain.toFixed(1)}</span>{:else}<span class="num dim">–</span>{/if}</dd></div>
    </dl>

    <section class="hr" aria-labelledby="hr-h">
      <h2 id="hr-h" class="block-h">Heart rate by set</h2>
      <div class="readout" aria-live="polite">
        {#if sel}
          {@const p = places.get(sel.id)}
          <p class="ro-main">
            <span>{p ? `Round ${p.round} · ${p.pos} ${sel.exercise_name}` : `Set ${sel.set_index + 1} · ${sel.exercise_name}`}</span>
          </p>
          {#if selStat?.has_hr}
            <p class="ro-stats">
              <span><span class="num">{selStat.peak_bpm}</span> <span class="unit">peak</span></span>
              <span class="zone-tag"><span class="sw" style:background={`var(--zone-${Math.max(1, selStat.zone)})`} aria-hidden="true"></span>Z{selStat.zone}</span>
              <span><span class="num">−{selStat.recovery_drop}</span> <span class="unit">in {selStat.recovery_window_s} s</span></span>
            </p>
          {:else}
            <p class="muted ro-hint">{setText(sel.reps, sel.weight_kg)}</p>
          {/if}
        {:else}
          <p class="muted ro-hint">{summary.hr_status === 'ready' ? 'Touch the trace to read a set.' : 'Each band is a set, in order. Touch one to read it.'}{places.size ? ' Wider, fainter bands are circuit rounds.' : ''}</p>
        {/if}
      </div>
      <HrTrace {summary} {selected} onselect={(sid) => (selected = sid)} />
      {#if summary.hr_status === 'ready' && source === 'server' && summary.alignment}
        <HrAlignment sessionId={id} alignment={summary.alignment} onchange={load} />
      {/if}
    </section>

    {#if summary.hr_status === 'ready'}
      <section class="block" aria-labelledby="z-h">
        <h2 id="z-h" class="block-h">Time in zones <span class="mono muted">avg {summary.avg_bpm} bpm · max HR {summary.max_hr}</span></h2>
        <ZoneBar seconds={summary.zone_seconds} />
      </section>
    {/if}

    <section class="block" aria-labelledby="sets-h">
      <h2 id="sets-h" class="block-h">Sets</h2>
      <div class="table" role="table" aria-label="Sets with heart rate">
        <div class="tr th" role="row">
          <span role="columnheader" class="label">Set</span>
          <span role="columnheader" class="label">Done</span>
          <span role="columnheader" class="label r">Peak</span>
          <span role="columnheader" class="label r">Recovery</span>
          <span role="columnheader" class="label r">Rest</span>
        </div>
        {#each groups as g, gi (gi)}
          <div class="group" role="rowgroup">
            <p class="g-name">{g.name}</p>
            {#each g.sets as s (s.id)}
              {@const st = stats.get(s.id)}
              {@const p = places.get(s.id)}
              <div
                class="tr"
                class:selected={s.id === selected}
                role="row"
                tabindex="0"
                onclick={() => (selected = s.id)}
                onkeydown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), (selected = s.id))}
                onfocus={() => (selected = s.id)}
              >
                <span role="cell" class="mono">{p ? p.pos : s.set_index + 1}</span>
                <span role="cell" class="done">
                  <span class="num">{s.reps} × {weight(s.weight_kg)}</span>{#if s.weight_kg}<span class="unit"> kg</span>{/if}
                  {#if s.reps !== s.target_reps || s.weight_kg !== s.target_weight_kg}
                    <span class="mono off"><span class="visually-hidden">target </span>/{s.weight_kg === s.target_weight_kg ? s.target_reps : `${s.target_reps}×${weight(s.target_weight_kg)}`}</span>
                  {/if}
                  {#if p}<span class="ex-in">{s.exercise_name}</span>{/if}
                </span>
                <span role="cell" class="r">
                  {#if st?.has_hr}
                    <span class="zone-tag"><span class="num">{st.peak_bpm}</span><span class="sw" style:background={`var(--zone-${Math.max(1, st.zone)})`} aria-hidden="true"></span><span class="zl">Z{st.zone}</span><span class="visually-hidden"> bpm</span></span>
                  {:else}<span class="dim">–</span>{/if}
                </span>
                <span role="cell" class="r">{#if st?.has_hr}<span class="num">−{st.recovery_drop}</span>{:else}<span class="dim">–</span>{/if}</span>
                <span role="cell" class="r mono">{st?.rest_s ? clock(st.rest_s) : '–'}</span>
              </div>
              {#if workout && s.id === selected}
                {#if editing === s.id}
                  <div class="edit">
                    <Stepper label="Reps" value={String(s.reps)} canDecrease={s.reps > 0} onstep={(d) => step(s, 'reps', d)} />
                    <Stepper label="Weight" unit={s.weight_kg === 0 ? 'BW' : 'kg'} value={weight(s.weight_kg) === 'BW' ? '0' : weight(s.weight_kg)} canDecrease={s.weight_kg > 0} onstep={(d) => step(s, 'weight_kg', d)} />
                    <button class="btn" onclick={() => (editing = null)}>Done editing</button>
                  </div>
                {:else}
                  <div class="edit-row">
                    <button class="btn btn-quiet" onclick={() => (editing = s.id)}>{p ? `Edit ${s.exercise_name}, round ${p.round}` : `Edit set ${s.set_index + 1}`}</button>
                  </div>
                {/if}
              {/if}
            {/each}
          </div>
        {/each}
      </div>
      {#if summary.hr_status === 'ready'}
        <p class="muted foot">Recovery is the heart rate drop from the set's peak to 60 s after it, or to the next set if that started sooner.</p>
      {/if}
    </section>
  {/if}
</main>

{#if justFinished}
  <ActionBar>
    <a class="btn btn-primary" href="/" use:link>Done</a>
  </ActionBar>
{/if}

<style>
  .summary {
    max-width: var(--page-narrow);
    margin: 0 auto;
    padding: calc(env(safe-area-inset-top) + var(--space-2)) var(--gutter) 0;
  }
  @media (min-width: 900px) {
    .summary {
      max-width: var(--page-wide);
    }
  }
  .top {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .back {
    margin-left: calc(var(--space-2) * -1);
    text-decoration: none;
  }
  .top-end {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }
  .gone {
    display: grid;
    gap: var(--space-3);
    justify-items: start;
    margin-top: var(--space-7);
  }
  .gone h1 {
    font-size: var(--size-heading);
  }
  .name {
    margin-top: var(--space-4);
    font-size: var(--size-heading);
  }
  .meta {
    margin-top: var(--space-1);
    font-size: var(--size-small);
    color: var(--text-muted);
  }
  .notice {
    margin-top: var(--space-4);
    padding: var(--space-3) var(--space-4);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    font-size: var(--size-small);
  }

  /* Stat strip: one ruled row, like splits on a clipboard */
  .strip {
    display: grid;
    grid-template-columns: repeat(4, auto);
    justify-content: space-between;
    margin: var(--space-5) 0 0;
    padding: var(--space-3) 0;
    border-top: 1px solid var(--line-strong);
    border-bottom: 1px solid var(--line);
  }
  .strip div {
    display: grid;
    gap: var(--space-1);
  }
  .strip dd {
    margin: 0;
  }
  .strip .num {
    font-size: var(--size-stat);
  }
  .unit {
    margin-left: 0.2em;
    font-size: var(--size-small);
    color: var(--text-muted);
  }
  .dim {
    color: var(--line-strong);
  }

  /* HR */
  .hr {
    margin-top: var(--space-6);
  }
  .readout {
    min-height: 3.75rem;
    margin-bottom: var(--space-2);
  }
  .ro-main {
    font-weight: 600;
  }
  .ro-stats {
    display: flex;
    align-items: baseline;
    gap: var(--space-4);
    margin-top: var(--space-1);
  }
  .ro-stats .num {
    font-size: var(--size-heading);
  }
  .ro-hint {
    padding-top: var(--space-4);
    font-size: var(--size-small);
  }
  .zone-tag {
    display: inline-flex;
    align-items: center;
    gap: var(--space-1);
    font-weight: 600;
    font-size: var(--size-small);
  }
  .zl {
    font-size: var(--size-label);
    font-weight: 600;
    color: var(--text-muted);
  }
  .early {
    display: inline-block;
    margin-top: var(--space-2);
    padding: 0.125rem var(--space-2);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-s);
    font-size: var(--size-small);
    font-weight: 600;
  }
  .summary {
    overflow-x: clip;
  }
  .zone-tag .num {
    font-size: var(--size-lead);
  }
  .sw {
    width: 0.625rem;
    height: 0.625rem;
    border-radius: 2px;
    flex: none;
  }

  .block {
    margin-top: var(--space-6);
  }
  .block-h {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-2);
    font-size: var(--size-body);
    margin-bottom: var(--space-3);
  }
  .block-h .mono {
    font-size: var(--size-label);
    font-weight: 400;
  }

  /* Sets table */
  .table {
    max-width: 40rem;
  }
  .tr {
    display: grid;
    grid-template-columns: 2rem 1fr 4.25rem 4.5rem 3.5rem;
    align-items: baseline;
    gap: var(--space-2);
    padding: var(--space-2) var(--space-2);
    margin: 0 calc(var(--space-2) * -1);
    border-radius: var(--radius-s);
    cursor: pointer;
  }
  .tr.th {
    cursor: default;
    padding-bottom: var(--space-1);
    border-bottom: 1px solid var(--line-strong);
    border-radius: 0;
      margin: 0;
    padding-left: 0;
    padding-right: 0;
  }
  .tr:not(.th):hover {
    background: var(--band);
  }
  .tr.selected {
    background: var(--band-active);
  }
  .tr:focus-visible {
    outline-offset: -2px;
  }
  .r {
    text-align: right;
    justify-self: end;
  }
  .g-name {
    font-weight: 600;
    font-size: var(--size-small);
    padding: var(--space-3) 0 var(--space-1);
    border-bottom: 1px solid var(--line);
    margin-bottom: var(--space-1);
  }
  .ex-in {
    display: block;
    overflow: hidden;
    text-overflow: ellipsis;
    font-size: var(--size-small);
    color: var(--text-muted);
  }
  .done {
    white-space: nowrap;
  }
  .done .num,
  .tr .r .num {
    font-size: var(--size-lead);
  }
  .off {
    font-size: var(--size-label);
    color: var(--text-muted);
    margin-left: var(--space-1);
  }
  .edit {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    gap: var(--space-2);
    padding: var(--space-2) 0 var(--space-3);
  }
  .edit .btn {
    grid-column: 1 / -1;
  }
  .edit-row {
    display: flex;
    justify-content: flex-end;
  }
  .edit-row .btn {
    font-size: var(--size-small);
  }
  .foot {
    margin-top: var(--space-4);
    font-size: var(--size-label);
    max-width: 60ch;
  }
</style>
