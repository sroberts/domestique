<script lang="ts">
  import { fly } from 'svelte/transition';
  import { quintOut } from 'svelte/easing';
  import X from '@lucide/svelte/icons/x';
  import ActionBar from '../components/ActionBar.svelte';
  import Plate from '../components/Plate.svelte';
  import Stepper from '../components/Stepper.svelte';
  import SyncStatus from '../components/SyncStatus.svelte';
  import { sync } from '../lib/sync.svelte';
  import { buzz, chime, clock, keepAwake, primeAudio } from '../lib/device.svelte';
  import { clock as fmtClock, setText, weight } from '../lib/format';
  import { link, router } from '../lib/router.svelte';
  import { Workout, type Slot } from '../lib/workout.svelte';

  let { id }: { id: string } = $props();

  let w = $state<Workout | null>(null);
  let missing = $state(false);
  let busy = $state(false);
  let dialog = $state<HTMLDialogElement | null>(null);
  let announce = $state('');

  $effect(() => {
    let cancelled = false;
    Workout.load(id).then((x) => {
      if (cancelled) return;
      if (!x) missing = true;
      else if (x.session.finished_at) router.go(`/session/${id}/summary`, { replace: true });
      else w = x;
    });
    const releaseClock = clock.use();
    const releaseWake = keepAwake();
    return () => {
      cancelled = true;
      releaseClock();
      releaseWake();
    };
  });

  let c = $derived(w?.session.cursor);
  let slot = $derived(w?.current ?? null);
  let exerciseSlots = $derived(w && slot ? w.slots.filter((s) => s.exerciseIndex === slot.exerciseIndex) : []);
  let target = $derived(w && slot ? w.target(slot) : null);
  let phaseElapsed = $derived(c ? (clock.now - c.phase_started_at) / 1000 : 0);
  let sessionElapsed = $derived(w ? (clock.now - w.session.started_at) / 1000 : 0);
  let restLeft = $derived(c && c.phase === 'rest' ? c.rest_s - phaseElapsed : 0);
  let restFraction = $derived(c && c.phase === 'rest' && c.rest_s > 0 ? Math.max(0, Math.min(1, restLeft / c.rest_s)) : 0);
  let restOver = $derived(c?.phase === 'rest' && restLeft <= 0);
  let newExercise = $derived(!!(w?.lastSet && slot && w.lastSet.exercise_index !== slot.exerciseIndex));

  // Signal the end of rest once per rest period: haptics where the platform
  // allows, and a screen-reader announcement everywhere.
  let alerted = $state<number | null>(null);
  $effect(() => {
    if (restOver && c && alerted !== c.phase_started_at) {
      alerted = c.phase_started_at;
      buzz();
      chime();
      announce = slot ? `Rest over. ${slot.exercise.name}, set ${slot.setIndex + 1} of ${exerciseSlots.length}.` : 'Rest over.';
    }
  });

  async function act(fn: () => Promise<void>) {
    if (busy) return;
    busy = true;
    try {
      await fn();
    } finally {
      busy = false;
    }
  }

  const start = () => act(() => w!.startSet());
  const finishSet = () => {
    primeAudio(); // a tap: lets the rest-over chime play later
    return act(() => w!.finishSet());
  };
  const finishSession = () =>
    act(async () => {
      await w!.finish();
      try {
        sessionStorage.setItem('domestique:just-finished', id);
      } catch {
        /* ignore */
      }
      router.go(`/session/${id}/summary`, { replace: true });
    });
  const discard = () =>
    act(async () => {
      dialog?.close();
      await w!.discard();
      router.go('/', { replace: true });
    });
  // A session with nothing (or almost nothing) in it can be thrown away
  // rather than kept as training.
  let discardable = $derived(!!w && (w.sets.length <= 1 || sessionElapsed < 120));
  const skipExercise = () =>
    act(async () => {
      dialog?.close();
      await w!.skipExercise();
      if (w!.session.finished_at) router.go(`/session/${id}/summary`, { replace: true });
    });

  function weightStep(kg: number): number {
    return kg < 20 ? 1 : 2.5;
  }

  function ladderState(s: Slot): 'done' | 'current' | 'skipped' | 'todo' {
    if (!w || !c) return 'todo';
    if (c.skipped.includes(s.index)) return 'skipped';
    if (s.index < c.index) return 'done';
    return s.index === c.index ? 'current' : 'todo';
  }

  const compact = (reps: number, kg: number) => `${reps}×${weight(kg)}`;

  function doneText(s: Slot): string {
    const rec = w?.sets.find((x) => x.exercise_index === s.exerciseIndex && x.set_index === s.setIndex);
    return rec ? compact(rec.reps, rec.weight_kg) : '';
  }
</script>

<div class="visually-hidden" aria-live="assertive">{announce}</div>

{#if missing}
  <main class="session">
    <h1 class="gone">This session isn't on this device.</h1>
    <p class="muted">Sessions are logged on the phone that started them. Finished sessions are in Recent sessions on the Today screen.</p>
    <a class="btn" href="/" use:link>Back to today</a>
  </main>
{:else if w && c}
  <main class="session" class:resting={c.phase === 'rest'}>
    <header class="top">
      <button class="btn btn-quiet end" onclick={() => dialog?.showModal()}>
        <X size={20} aria-hidden="true" /> End
      </button>
      <p class="mono elapsed"><span class="visually-hidden">Session time </span>{w.session.name} · {fmtClock(sessionElapsed)}</p>
    </header>
    <!-- Quiet unless something is waiting: sets are safe on the phone either way. -->
    {#if sync.pending > 0 && sync.status !== 'syncing' && sync.status !== 'idle' || sync.status === 'auth'}
      <div class="sync-row"><SyncStatus /></div>
    {/if}

    {#if slot && c.phase !== 'last'}
      <section class="exercise" aria-label="Current exercise">
        <h1 class="ex-name">{slot.exercise.name}</h1>
        <p class="mono ex-pos">Lift {slot.exerciseIndex + 1} of {w.session.plan.length}{#if slot.exercise.notes} · {slot.exercise.notes}{/if}</p>
        <ol class="ladder" aria-label="Sets" style:--n={exerciseSlots.length}>
          {#each exerciseSlots as s (s.index)}
            {@const st = ladderState(s)}
            <li class="rung {st}" aria-current={st === 'current' ? 'step' : undefined}>
              <span class="rung-n mono">{s.setIndex + 1}</span>
              <span class="rung-v mono">{st === 'done' ? doneText(s) : st === 'skipped' ? 'Skipped' : compact(w.target(s).reps, w.target(s).weight_kg)}</span>
            </li>
          {/each}
        </ol>
      </section>
    {/if}

    {#key `${c.phase}:${c.index}`}
      <section class="hero" in:fly={{ y: 8, duration: 150, easing: quintOut }}>
        {#if c.phase === 'ready' && slot && target}
          <Plate n={slot.setIndex + 1} of={exerciseSlots.length} />
          <div class="target-big" aria-label={`Target ${setText(target.reps, target.weight_kg)}`}>
            <div class="tb"><span class="num live">{target.reps}</span><span class="label">Reps</span></div>
            <span class="times num" aria-hidden="true">×</span>
            <div class="tb"><span class="num live">{weight(target.weight_kg)}</span><span class="label">{target.weight_kg === 0 ? 'Bodyweight' : 'Kg'}</span></div>
          </div>
        {:else if c.phase === 'active' && slot && target}
          <Plate n={slot.setIndex + 1} of={exerciseSlots.length} active />
          <div class="in-set">
            <p class="in-set-clock"><span class="label signal-label"><span class="live-dot" aria-hidden="true"></span>In set</span>
              <span class="num set-clock" aria-label={`${Math.floor(phaseElapsed)} seconds`}>{fmtClock(phaseElapsed)}</span></p>
            <div class="target-big" aria-label={`Target ${setText(target.reps, target.weight_kg)}`}>
              <div class="tb"><span class="num live">{target.reps}</span><span class="label">Reps</span></div>
              <span class="times num" aria-hidden="true">×</span>
              <div class="tb"><span class="num live">{weight(target.weight_kg)}</span><span class="label">{target.weight_kg === 0 ? 'Bodyweight' : 'Kg'}</span></div>
            </div>
          </div>
        {:else if c.phase === 'rest'}
          <div class="rest" class:over={restOver}>
            <p class="label" class:signal-label={restOver}>{restOver ? 'Rest over' : 'Rest'}</p>
            <p class="num live" class:signal={restOver} aria-label={restOver ? `${Math.floor(-restLeft)} seconds over` : `${Math.ceil(restLeft)} seconds left`}>
              {restOver ? `+${fmtClock(-restLeft)}` : fmtClock(Math.ceil(restLeft))}
            </p>
            <div class="drain" class:gone={restOver} role="progressbar" aria-label="Rest remaining" aria-valuemin={0} aria-valuemax={c.rest_s} aria-valuenow={Math.max(0, Math.ceil(restLeft))}>
              <div class="drain-fill" style:transform={`scaleX(${restFraction})`}></div>
            </div>
            <div class="rest-tools">
              <span class="mono muted">of {fmtClock(c.rest_s)}</span>
              <span class="rest-adjust">
                <button class="btn" onclick={() => w!.addRest(-15)} disabled={c.rest_s <= 15} aria-label="15 seconds less rest">−15 s</button>
                <button class="btn" onclick={() => w!.addRest(15)} aria-label="15 seconds more rest">+15 s</button>
              </span>
            </div>
          </div>
        {:else if c.phase === 'last'}
          <div class="done">
            <h1 class="done-title">All sets in.</h1>
            <p class="muted">Check the last set, then finish. Heart rate attaches once Whoop syncs.</p>
          </div>
        {/if}
      </section>
    {/key}

    {#if (c.phase === 'rest' || c.phase === 'last') && w.lastSet}
      {@const ls = w.lastSet}
      <section class="logged" aria-labelledby="logged-h">
        <h2 id="logged-h" class="logged-h">
          <span>Logged</span>
          <span class="mono muted">{ls.exercise_name} · set {ls.set_index + 1}</span>
        </h2>
        <div class="steppers">
          <Stepper label="Reps" value={String(ls.reps)} canDecrease={ls.reps > 0} onstep={(d) => w!.adjustLast('reps', d)} />
          <Stepper
            label="Weight"
            unit={ls.weight_kg === 0 ? 'BW' : 'kg'}
            value={ls.weight_kg === 0 ? '0' : weight(ls.weight_kg)}
            canDecrease={ls.weight_kg > 0}
            onstep={(d) => w!.adjustLast('weight_kg', d * weightStep(ls.weight_kg + (d < 0 ? -0.01 : 0)))}
          />
        </div>
        <!-- Space reserved so the note appearing never shifts the screen mid-glance. -->
        <p class="mono muted adj" aria-live="polite">
          {#if ls.reps !== ls.target_reps || ls.weight_kg !== ls.target_weight_kg}Target was {setText(ls.target_reps, ls.target_weight_kg)}{ls.weight_kg !== ls.target_weight_kg ? '. New weight carries to the next sets.' : '.'}{/if}
        </p>
      </section>
    {/if}

    {#if c.phase === 'rest' && slot && target}
      <section class="next" aria-label="Next set">
        <p class="label">Next</p>
        {#if newExercise}
          <p class="next-main">{slot.exercise.name}</p>
          <p class="mono muted">Set 1 of {exerciseSlots.length} · {setText(target.reps, target.weight_kg)}</p>
        {:else}
          <p class="next-main">Set {slot.setIndex + 1} of {exerciseSlots.length} · <span class="num next-num">{setText(target.reps, target.weight_kg)}</span></p>
        {/if}
      </section>
    {/if}
  </main>

  <ActionBar>
    {#if c.phase === 'ready'}
      <button class="btn btn-primary" onclick={start} disabled={busy}>Start set</button>
    {:else if c.phase === 'active'}
      <button class="btn btn-primary" onclick={finishSet} disabled={busy}>Finish set</button>
    {:else if c.phase === 'rest' && slot}
      <button class="btn btn-primary" class:inverse={restOver} onclick={start} disabled={busy}>
        {newExercise ? `Start ${slot.exercise.name}` : `Start set ${slot.setIndex + 1}`}
      </button>
    {:else if c.phase === 'last'}
      <button class="btn btn-primary" onclick={finishSession} disabled={busy}>Finish session</button>
    {/if}
  </ActionBar>

  <dialog bind:this={dialog} class="sheet" aria-labelledby="end-h" onclick={(e) => e.target === dialog && dialog?.close()}>
    <div class="sheet-inner">
      <h2 id="end-h">End early?</h2>
      {#if c.phase === 'active'}
        <p class="muted">The set in progress won't be logged.</p>
      {/if}
      <div class="sheet-actions">
        {#if slot && c.phase !== 'active' && c.phase !== 'last'}
          <button class="btn sheet-btn" onclick={skipExercise}>Skip the rest of {slot.exercise.name}</button>
        {/if}
        <button class="btn sheet-btn" class:btn-danger={!discardable} onclick={() => { dialog?.close(); finishSession(); }}>
          Finish session now
        </button>
        {#if discardable}
          <button class="btn btn-danger sheet-btn" onclick={discard}>
            Discard session{w.sets.length ? ` (${w.sets.length} set)` : ''}
          </button>
          <p class="muted discard-note">Discarding removes it from history and training load.</p>
        {/if}
        <!-- svelte-ignore a11y_autofocus -->
        <button class="btn sheet-btn" autofocus onclick={() => dialog?.close()}>Keep going</button>
      </div>
    </div>
  </dialog>
{/if}

<style>
  .session {
    max-width: 35rem;
    margin: 0 auto;
    padding: calc(env(safe-area-inset-top) + var(--space-2)) var(--gutter) 0;
    display: flex;
    flex-direction: column;
    min-height: calc(100dvh - var(--action-height) - var(--space-6) - env(safe-area-inset-bottom));
  }
  .gone {
    font-size: var(--size-heading);
    margin: var(--space-7) 0 var(--space-3);
  }
  .session > .btn {
    margin-top: var(--space-5);
    align-self: flex-start;
  }
  .top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
  }
  .sync-row {
    display: flex;
    justify-content: flex-end;
    margin-top: calc(var(--space-2) * -1);
  }
  .end {
    margin-left: calc(var(--space-2) * -1);
    min-height: 2.75rem;
  }
  .elapsed {
    font-size: var(--size-small);
    color: var(--text-muted);
  }

  /* Exercise and set ladder */
  .exercise {
    margin-top: var(--space-4);
  }
  .ex-name {
    font-size: var(--size-heading);
  }
  .ex-pos {
    margin-top: var(--space-1);
    font-size: var(--size-small);
    color: var(--text-muted);
  }
  /* A splits board: one equal column per set */
  .ladder {
    display: grid;
    grid-template-columns: repeat(var(--n), minmax(0, 1fr));
    gap: var(--space-1);
    list-style: none;
    margin: var(--space-4) 0 0;
    padding: 0;
  }
  .rung {
    display: grid;
    align-content: start;
    gap: 0.125rem;
    min-width: 0;
    padding: var(--space-1) var(--space-2) var(--space-2);
    white-space: nowrap;
    overflow: hidden;
    border: 1px solid var(--line);
    border-radius: var(--radius-s);
    font-size: var(--size-small);
    color: var(--text-muted);
  }
  .rung-n {
    font-size: var(--size-label);
  }
  .rung-v {
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .rung.done {
    color: var(--text);
    border-color: var(--line-strong);
    background: var(--surface);
  }
  .rung.current {
    color: var(--text);
    border-color: var(--signal);
  }
  .rung.skipped {
    text-decoration: line-through;
  }

  /* Hero */
  .hero {
    flex: 1;
    display: flex;
    flex-direction: column;
    /* Anchor the numbers just above the action, in the eye line of the thumb. */
    justify-content: flex-end;
    gap: var(--space-5);
    padding: var(--space-6) 0;
  }
  .live {
    font-size: var(--size-live);
    font-weight: 700;
  }
  .signal {
    color: var(--signal);
  }
  .target-big {
    display: flex;
    align-items: flex-start;
    gap: var(--space-4);
  }
  .tb {
    display: grid;
    gap: var(--space-2);
  }
  .times {
    font-size: var(--size-target);
    color: var(--text-muted);
    padding-top: 0.6rem;
  }
  .in-set {
    display: grid;
    gap: var(--space-3);
  }
  .signal-label {
    color: var(--signal);
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
  }
  .live-dot {
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 50%;
    background: var(--signal);
  }
  .target-mid {
    font-size: var(--size-target);
    color: var(--text-muted);
  }

  /* Rest: the bar drains edge to edge */
  .rest {
    display: grid;
    gap: var(--space-3);
  }
  .drain {
    height: 1rem;
    margin: var(--space-2) calc(var(--gutter) * -1) 0;
    background: var(--line);
    overflow: hidden;
  }
  .drain-fill {
    height: 100%;
    background: var(--signal);
    transform-origin: left center;
    transition: transform 250ms linear;
    will-change: transform;
  }
  .rest-tools {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    font-size: var(--size-small);
  }
  .rest-adjust {
    display: flex;
    gap: var(--space-2);
  }
  .rest.over {
    margin: 0 calc(var(--gutter) * -1);
    padding: var(--space-5) var(--gutter);
    background: var(--signal);
    color: var(--on-signal);
    transition: background-color var(--dur-state) var(--ease-out);
  }
  .rest.over .label,
  .rest.over .signal,
  .rest.over .muted {
    color: var(--on-signal);
  }
  .rest.over .btn {
    border-color: color-mix(in srgb, var(--on-signal) 45%, transparent);
    color: var(--on-signal);
  }
  .drain.gone {
    visibility: hidden;
    height: 0;
    margin: 0;
  }
  .rest-adjust .btn {
    min-height: var(--tap-min);
    min-width: 4rem;
    font-size: var(--size-small);
  }
  .done {
    display: grid;
    gap: var(--space-3);
  }
  .done-title {
    font-family: var(--font-num);
    font-size: var(--size-target);
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.01em;
  }

  /* Logged set, editable during rest */
  .logged {
    padding-top: var(--space-4);
    border-top: 1px solid var(--line);
  }
  .logged-h {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-2);
    font-size: var(--size-body);
  }
  .logged-h .mono {
    font-size: var(--size-small);
    font-weight: 400;
  }
  .steppers {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    gap: var(--space-3);
    margin-top: var(--space-3);
  }
  .in-set-clock {
    display: flex;
    align-items: baseline;
    gap: var(--space-3);
  }
  .set-clock {
    font-size: var(--size-target);
    color: var(--signal);
  }
  .discard-note {
    font-size: var(--size-label);
    text-align: center;
  }
  :global(.btn-primary.inverse) {
    background: var(--on-signal);
    color: var(--signal);
    border: 2px solid var(--signal);
  }
  .adj {
    min-height: 2.4em;
    margin-top: var(--space-2);
    font-size: var(--size-label);
  }
  .next {
    margin-top: var(--space-5);
    padding-bottom: var(--space-2);
  }
  .next-main {
    margin-top: var(--space-1);
    font-weight: 600;
    font-size: var(--size-lead);
  }
  .next-num {
    font-size: var(--size-heading);
    font-weight: 600;
  }

  /* End sheet */
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
  .sheet[open] {
    animation: sheet-in 200ms var(--ease-out);
  }
  @keyframes sheet-in {
    from {
      transform: translateY(1.5rem);
      opacity: 0;
    }
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
</style>
