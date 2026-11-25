<script lang="ts">
  import { untrack } from 'svelte';
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import ActionBar from '../components/ActionBar.svelte';
  import CheckIn from '../components/CheckIn.svelte';
  import CoachNote from '../components/CoachNote.svelte';
  import HrAlignment from '../components/HrAlignment.svelte';
  import ExportMenu from '../components/ExportMenu.svelte';
  import HrTrace from '../components/HrTrace.svelte';
  import Stepper from '../components/Stepper.svelte';
  import SyncStatus from '../components/SyncStatus.svelte';
  import ZoneBar from '../components/ZoneBar.svelte';
  import { AuthError, HTTPError, OfflineError, fetchSummary, mergeSessions, mergedInto, previewMerge } from '../lib/api';
  import { checkInPath, fetchCheckIn } from '../lib/data';
  import { discardSession, pendingFor } from '../lib/db';
  import { sync } from '../lib/sync.svelte';
  import { sessionExports } from '../lib/export';
  import { clock, day, duration, roundsText, setText, thousands, time, weight } from '../lib/format';
  import { backTarget, link, router } from '../lib/router.svelte';
  import { Workout, earlyText, emptySession, endedEarly, isReal, progressOf, roundsOf, shortText, type CircuitPlace } from '../lib/workout.svelte';
  import type { CheckInResp, MergePreview, SessionRef, SetRecord, SetStat, Summary } from '../lib/types';

  /** Heart rate change after a set: a fall reads −12, a rise +2 (still
   * climbing when the next set began). */
  const dropText = (d: number) => (d > 0 ? `−${d}` : d < 0 ? `+${-d}` : '0');

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
  let checkin = $state<CheckInResp | null>(null);
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
      if (s.session.finished_at && !checkin) {
        fetchCheckIn(checkInPath('session', s.session.id)).then((c) => (checkin = c), () => {});
      }
    } catch (err) {
      // Merged into another session since: that's where it lives now.
      const into = mergedInto(err);
      if (into) {
        await sync.followMerges(true);
        router.go(`/session/${into}/summary`, { replace: true });
        return;
      }
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
    // Also while Outpost's models write the debrief and set efforts, for a few
    // minutes at most.
    let aiPolls = 0;
    const t = setInterval(() => {
      if (document.visibilityState !== 'visible') return;
      const aiPending = !!(summary?.note?.pending || summary?.efforts_pending) && aiPolls < 18;
      if (summary?.hr_status === 'pending' || problem || aiPending) {
        if (aiPending && summary?.hr_status !== 'pending') aiPolls++;
        void load();
      }
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
  // Rest only shows when some set has one (a single set, or a circuit, may not).
  let hasRest = $derived((summary?.set_stats ?? []).some((x) => x.rest_s > 0));
  let sel = $derived(summary?.sets.find((s) => s.id === selected) ?? null);
  let selStat = $derived(sel ? stats.get(sel.id) : undefined);
  let end = $derived(summary ? (summary.session.finished_at ?? summary.sets.at(-1)?.ended_at ?? summary.session.started_at) : 0);

  // Merging: one workout saved as two sessions, put back together.
  let mergeDialog = $state<HTMLDialogElement | null>(null);
  let mergeWith = $state<SessionRef | null>(null);
  let preview = $state<MergePreview | null>(null);
  let mergeError = $state('');
  let merging = $state(false);
  let suggested = $derived(summary?.same_day?.find((o) => o.id === summary?.suggest_merge) ?? null);

  function openMerge(other: SessionRef | null) {
    mergeWith = null;
    preview = null;
    mergeError = '';
    mergeDialog?.showModal();
    if (other) void choose(other);
  }

  async function choose(other: SessionRef) {
    mergeWith = other;
    preview = null;
    mergeError = '';
    try {
      preview = await previewMerge(id, other.id);
    } catch (err) {
      mergeError = err instanceof HTTPError ? err.message : "Couldn't reach the Mac Mini. Try again when you're connected.";
    }
  }

  async function confirmMerge() {
    if (!mergeWith || merging) return;
    merging = true;
    mergeError = '';
    try {
      // Every set of both must be on the Mac Mini first, or it would merge
      // without them.
      await sync.flush();
      if (await pendingFor([id, mergeWith.id])) {
        mergeError = 'Some sets are still syncing to the Mac Mini. Try again in a moment.';
        return;
      }
      const res = await mergeSessions(id, mergeWith.id);
      await sync.followMerges(true);
      mergeDialog?.close();
      if (res.id === id) {
        workout = await Workout.load(id);
        await load();
      } else {
        router.go(`/session/${res.id}/summary`, { replace: true });
      }
    } catch (err) {
      mergeError = err instanceof HTTPError ? err.message : "Couldn't reach the Mac Mini. Try again when you're connected.";
    } finally {
      merging = false;
    }
  }

  // Nothing in it counted: offer to throw it away, with one confirming tap.
  let isEmpty = $derived(!!summary?.session.finished_at && emptySession(summary?.progress));
  let confirmDiscard = $state(false);
  let discarding = $state(false);
  async function discard() {
    if (discarding) return;
    discarding = true;
    await discardSession(id);
    sync.kick();
    router.go('/', { replace: true });
  }

  /** "4 rounds" for a circuit exercise, "5 sets" otherwise. */
  const countText = (n: number, circuit?: string) => (circuit ? roundsText(n) : `${n} ${n === 1 ? 'set' : 'sets'}`);

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
    {#if isEmpty}
      <section class="empty" aria-labelledby="empty-h">
        <h2 id="empty-h" class="empty-h">Nothing lifted</h2>
        <p class="muted">
          {summary.progress.done ? `${summary.progress.done === 1 ? 'The set was' : `All ${summary.progress.done} sets were`} too quick to be lifting: under 5\u00a0s, or 2\u00a0s a rep.` : 'No sets were logged.'}
          If it was started by mistake, discard it.
        </p>
        {#if confirmDiscard}
          <div class="empty-actions">
            <button class="btn btn-danger" onclick={discard} disabled={discarding}>{discarding ? 'Discarding…' : 'Discard session'}</button>
            <button class="btn btn-quiet" onclick={() => (confirmDiscard = false)}>Keep it</button>
          </div>
          <p class="small muted">It goes from history and training load on every device.</p>
        {:else}
          <div class="empty-actions">
            <button class="btn" onclick={() => (confirmDiscard = true)}>Discard this session…</button>
          </div>
        {/if}
      </section>
    {:else}
      {#if endedEarly(summary.progress)}<p class="early">{earlyText(summary.progress)}</p>{/if}
      {#if summary.progress.short}
        <p class="muted short-note">{shortText(summary.progress)}, under 5&nbsp;s or 2&nbsp;s a rep. It still counts.</p>
      {/if}
    {/if}

    {#if source === 'local'}
      <p class="notice">
        {#if problem === 'unsynced'}Still syncing to the Mac Mini. These are the sets on this phone.
        {:else if problem === 'auth'}Sign in again to sync and fetch heart rate. These are the sets on this phone.
        {:else}No connection. These are the sets on this phone; heart rate needs the Mac Mini.{/if}
      </p>
    {/if}

    {#if suggested && source === 'server'}
      <p class="merge-hint">
        Looks like the rest of <strong>{suggested.name}</strong> at&nbsp;{time(suggested.started_at)}.
        <button class="link" onclick={() => openMerge(suggested)}>Merge them…</button>
      </p>
    {/if}

    <dl class="strip">
      <div><dt class="label">Volume</dt><dd><span class="num">{thousands(summary.volume_kg)}</span> <span class="unit">kg</span></dd></div>
      <div><dt class="label">Sets</dt><dd><span class="num">{summary.sets.length}</span></dd></div>
      <div><dt class="label">Peak</dt><dd>{#if summary.peak_bpm}<span class="num">{summary.peak_bpm}</span> <span class="unit">bpm</span>{:else}<span class="num dim">–</span>{/if}</dd></div>
      <div><dt class="label">Strain</dt><dd>{#if summary.session.strain != null}<span class="num">{summary.session.strain.toFixed(1)}</span>{:else}<span class="num dim">–</span>{/if}</dd></div>
    </dl>

    {#if summary.note?.text}<CoachNote text={summary.note.text} />{/if}

    <section class="hr" aria-labelledby="hr-h">
      <h2 id="hr-h" class="block-h">Heart rate by set</h2>
      <div class="readout" aria-live="polite">
        {#if sel}
          {@const p = places.get(sel.id)}
          <p class="ro-main">
            <span>{p ? `Round ${p.round} · ${p.pos} ${sel.exercise_name}` : `Set ${sel.set_index + 1} · ${sel.exercise_name}`}</span>
            {#if summary.efforts?.[sel.id]}<span class="effort">looked {summary.efforts[sel.id]!.level}</span>{/if}
          </p>
          {#if selStat?.has_hr}
            <p class="ro-stats">
              <span><span class="num">{selStat.peak_bpm}</span> <span class="unit">peak</span></span>
              <span class="zone-tag"><span class="sw" style:background={`var(--zone-${Math.max(1, selStat.zone)})`} aria-hidden="true"></span>Z{selStat.zone}</span>
              <span><span class="num">{dropText(selStat.recovery_drop)}</span> <span class="unit">in {selStat.recovery_window_s} s</span></span>
            </p>
          {:else}
            <p class="muted ro-hint">{setText(sel.reps, sel.weight_kg)}</p>
          {/if}
        {:else}
          <p class="muted ro-hint">{summary.hr_status === 'ready' ? 'Pick a set on the trace or in the table to read it.' : 'Each band is a set, in order. Pick one to read it.'}{places.size ? ' Wider, fainter bands are circuit rounds.' : ''}</p>
        {/if}
      </div>
      <HrTrace {summary} {selected} onselect={(sid) => (selected = sid)} />
      {#if summary.hr_status === 'ready' && source === 'server' && summary.alignment}
        <HrAlignment sessionId={id} alignment={summary.alignment} onchange={load} />
      {/if}
    </section>

    <!-- After the payoff: the session ends on the effort, then asks how it felt. -->
    {#if checkin && summary && !isEmpty}
      <CheckIn path={checkInPath('session', summary.session.id)} questions={checkin.questions} initial={checkin.checkin}
        srpe={checkin.srpe ?? 0} session heading="How did that go?" />
    {/if}

    <!-- Wide screens: sets beside zones, rather than one stretched column. -->
    <div class="lower">
    {#if summary.hr_status === 'ready'}
      <section class="block zones-block" aria-labelledby="z-h">
        <h2 id="z-h" class="block-h">Time in zones <span class="mono muted">avg {summary.avg_bpm} bpm · zones from max {summary.max_hr}</span></h2>
        <ZoneBar seconds={summary.zone_seconds} />
        {#if summary.peak_bpm > summary.max_hr}
          <p class="muted foot">Peak {summary.peak_bpm} is above the max HR of {summary.max_hr} that zones use. <a href="/settings" use:link>Change it in Settings</a></p>
        {/if}
      </section>
    {/if}

    <section class="block sets-block" aria-labelledby="sets-h">
      <h2 id="sets-h" class="block-h">Sets</h2>
      <div class="table" class:no-rest={!hasRest} role="table" aria-label="Sets with heart rate">
        <div class="tr th" role="row">
          <span role="columnheader" class="label">Set</span>
          <span role="columnheader" class="label">Done</span>
          <span role="columnheader" class="label r">Peak</span>
          <span role="columnheader" class="label r">Recovery</span>
          {#if hasRest}<span role="columnheader" class="label r">Rest</span>{/if}
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
                  {#if summary.efforts?.[s.id]}<span class="effort"><span class="visually-hidden">effort </span>{summary.efforts[s.id]!.level}</span>{/if}
                  {#if !isReal(s)}<span class="mono uncounted">quick</span>{/if}
                  {#if p}<span class="ex-in">{s.exercise_name}</span>{/if}
                </span>
                <span role="cell" class="r">
                  {#if st?.has_hr}
                    <span class="zone-tag"><span class="num">{st.peak_bpm}</span><span class="sw" style:background={`var(--zone-${Math.max(1, st.zone)})`} aria-hidden="true"></span><span class="zl">Z{st.zone}</span><span class="visually-hidden"> bpm</span></span>
                  {:else}<span class="dim">–</span>{/if}
                </span>
                <span role="cell" class="r">{#if st?.has_hr}<span class="num">{dropText(st.recovery_drop)}</span><span class="unit"> bpm</span>{:else}<span class="dim">–</span>{/if}</span>
                {#if hasRest}<span role="cell" class="r mono">{st?.rest_s ? clock(st.rest_s) : '–'}</span>{/if}
              </div>
              {#if workout && s.id === selected}
                {#if editing === s.id}
                  <div class="edit">
                    <Stepper label="Reps" value={String(s.reps)} canDecrease={s.reps > 0} onstep={(d) => step(s, 'reps', d)} />
                    <Stepper label="Weight" unit={s.weight_kg === 0 ? 'Weight' : 'kg'} value={weight(s.weight_kg)} canDecrease={s.weight_kg > 0} onstep={(d) => step(s, 'weight_kg', d)} />
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
    </div>

    {#if summary.same_day?.length && source === 'server'}
      <section class="block merge-row">
        <button class="btn btn-quiet" onclick={() => openMerge(null)}>Merge with another session from {day(summary.session.started_at)}…</button>
      </section>
    {/if}
  {/if}
</main>

<dialog bind:this={mergeDialog} class="sheet" aria-labelledby="merge-h" onclick={(e) => e.target === mergeDialog && mergeDialog?.close()}>
  <div class="sheet-inner">
    {#if !mergeWith}
      <h2 id="merge-h">Merge with…</h2>
      <p class="muted">One workout saved as two? Pick the other part.</p>
      <div class="sheet-actions">
        {#each summary?.same_day ?? [] as o (o.id)}
          <button class="btn sheet-btn pick" onclick={() => choose(o)}>
            <span>{o.name}</span>
            <span class="mono muted">{time(o.started_at)}–{time(o.finished_at)} · {o.sets} {o.sets === 1 ? 'set' : 'sets'}</span>
          </button>
        {/each}
        <!-- svelte-ignore a11y_autofocus -->
        <button class="btn btn-quiet sheet-btn" autofocus onclick={() => mergeDialog?.close()}>Cancel</button>
      </div>
    {:else}
      <h2 id="merge-h">Merge into one session?</h2>
      {#if preview}
        <p class="muted">
          {preview.name} · {time(preview.started_at)}–{time(preview.finished_at)} · {preview.sets} sets
        </p>
        <ul class="preview" aria-label="The merged session">
          {#each preview.plan as ex, i (i)}
            <li><span>{ex.name}</span><span class="mono muted">{countText(ex.sets.length, ex.circuit)}</span></li>
          {/each}
        </ul>
        <p class="muted merge-note">Kept as the earlier session. Exercises in both carry on where the first part stopped. Heart rate, load and any Whoop recording follow. This can't be undone.</p>
      {:else if !mergeError}
        <p class="muted" aria-live="polite">Working out the merged session…</p>
      {/if}
      {#if mergeError}<p class="merge-error" role="alert">{mergeError}</p>{/if}
      <div class="sheet-actions">
        <button class="btn btn-primary sheet-btn" onclick={confirmMerge} disabled={!preview || merging}>{merging ? 'Merging…' : 'Merge'}</button>
        <button class="btn btn-quiet sheet-btn" onclick={() => mergeDialog?.close()}>Cancel</button>
      </div>
    {/if}
  </div>
</dialog>

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
    .lower {
      display: grid;
      grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
      grid-template-areas: 'sets zones';
      gap: 0 var(--space-6);
      align-items: start;
    }
    .sets-block {
      grid-area: sets;
    }
    .zones-block {
      grid-area: zones;
    }
    .lower .zones-block :global(.legend) {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
    /* The check-in reads as a short form, not a banner across the page. */
    .summary :global(.checkin) {
      max-width: 40rem;
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
    margin-top: var(--space-1);
    font-size: var(--size-small);
    font-weight: 600;
    color: var(--text-muted);
  }
  .short-note {
    margin-top: var(--space-1);
    font-size: var(--size-small);
  }
  /* Ruled, not boxed: the one question this screen asks first. */
  .empty {
    display: grid;
    gap: var(--space-2);
    margin-top: var(--space-4);
    padding-top: var(--space-4);
    border-top: 1px solid var(--line-strong);
  }
  .empty-h {
    font-size: var(--size-body);
  }
  .empty-actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
  }
  .empty-actions .btn {
    min-height: var(--tap-min);
  }
  .empty .small {
    font-size: var(--size-small);
  }
  .uncounted {
    margin-left: var(--space-1);
    font-size: var(--size-label);
    color: var(--text-muted);
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
  .no-rest .tr {
    grid-template-columns: 2rem 1fr 4.25rem 4.5rem;
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
  /* A quiet line, not a card: admin stays out of the payoff's way. */
  .merge-hint {
    margin-top: var(--space-2);
    font-size: var(--size-small);
    color: var(--text-muted);
  }
  .merge-hint strong {
    color: var(--text);
    font-weight: 600;
  }
  /* A full-size target that doesn't push the line apart. */
  .merge-hint .link {
    display: inline-flex;
    align-items: center;
    min-height: 2.75rem;
    margin-block: -0.75rem;
    padding: 0 var(--space-1);
    border: 0;
    background: none;
    color: var(--text);
    font: inherit;
    font-weight: 600;
    text-decoration: underline;
    text-underline-offset: 0.2em;
    cursor: pointer;
  }
  .merge-row {
    padding-bottom: var(--space-6);
  }
  .merge-row .btn {
    margin-left: calc(var(--space-2) * -1);
    font-size: var(--size-small);
  }

  /* Merge sheet */
  .sheet {
    margin: auto auto 0;
    width: 100%;
    max-width: 35rem;
    padding: 0;
    border: 1px solid var(--line-strong);
    border-bottom: 0;
    border-radius: var(--radius-m) var(--radius-m) 0 0;
    background: var(--surface);
    color: var(--text);
  }
  .sheet::backdrop {
    background: rgb(0 0 0 / 0.55);
  }
  .sheet-inner {
    display: grid;
    gap: var(--space-3);
    padding: var(--space-5) var(--gutter) calc(var(--space-5) + env(safe-area-inset-bottom));
  }
  .sheet-inner h2 {
    font-size: var(--size-heading);
  }
  .sheet-actions {
    display: grid;
    gap: var(--space-2);
    margin-top: var(--space-2);
  }
  .sheet-btn {
    min-height: var(--tap-min);
    width: 100%;
  }
  .pick {
    display: flex;
    justify-content: space-between;
    gap: var(--space-3);
    text-align: left;
  }
  .pick .mono {
    font-size: var(--size-small);
  }
  .preview {
    margin: 0;
    padding: 0;
    list-style: none;
    border-top: 1px solid var(--line);
  }
  .preview li {
    display: flex;
    justify-content: space-between;
    gap: var(--space-3);
    padding: var(--space-2) 0;
    border-bottom: 1px solid var(--line);
  }
  .preview .mono {
    font-size: var(--size-small);
  }
  .merge-note {
    font-size: var(--size-small);
  }
  .merge-error {
    color: var(--danger-ink);
    font-size: var(--size-small);
  }
  .foot {
    margin-top: var(--space-4);
    font-size: var(--size-label);
    max-width: 60ch;
  }
  /* System 1's reading of a set: a word, never a colour (it's a guess, not
     a measurement). */
  .effort {
    margin-left: var(--space-2);
    font-family: var(--font-mono);
    font-size: var(--size-small);
    color: var(--text-muted);
  }
</style>
