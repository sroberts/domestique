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
  import { clock as fmtClock, roundsText, setText, weight } from '../lib/format';
  import { link, router } from '../lib/router.svelte';
  import { circuitLetter, circuitsOf, type Circuit } from '../lib/rounds';
  import { Workout, type Slot } from '../lib/workout.svelte';
  import { weightStep } from '../lib/weights';

  let { id }: { id: string } = $props();

  let w = $state<Workout | null>(null);
  let missing = $state(false);
  let busy = $state(false);
  let dialog = $state<HTMLDialogElement | null>(null);
  let announce = $state('');
  /** The logged set whose steppers are open ("Fix"), and the slot whose
   * next-set weight stepper is open ("Change weight"). Corrections are by
   * exception: closed, rest is the clock, ±15 s and Start set. */
  let fixing = $state<string | null>(null);
  let weighing = $state<number | null>(null);

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
  /** The exercise the header and ladder show. Resting after an exercise's
   * last set, that's the one just done: the rest belongs to it, and Next
   * below names what comes after. */
  let shown = $derived.by(() => {
    const ls = w?.lastSet;
    if (!w || !slot || c?.phase !== 'rest' || !newExercise || !ls) return slot;
    return w.slots.find((s) => s.exerciseIndex === ls.exercise_index && s.setIndex === ls.set_index) ?? slot;
  });
  let shownSlots = $derived(w && shown ? w.slots.filter((s) => s.exerciseIndex === shown.exerciseIndex) : []);
  let circuits = $derived(w ? circuitsOf(w.session.plan) : []);
  /** "Circuit B", lettered as the editor and summary letter them. */
  const circuitName = (circ: Circuit) => `Circuit ${circuitLetter(circuits.findIndex((x) => x.start === circ.start))}`;
  // Circuits count rounds, not sets, and say what comes after this exercise.
  let unit = $derived<'Set' | 'Round'>(slot?.circuit ? 'Round' : 'Set');
  let then = $derived(w && shown?.circuit ? w.nextAfter(shown.index) : null);
  /** Where a circuit goes after this exercise: the next one in the round,
   * rest before the next round, or whatever follows the circuit. */
  let thenText = $derived.by(() => {
    if (!shown?.circuit || !then) return '';
    if (then.circuit?.start !== shown.circuit.start) return then.exercise.name;
    return then.setIndex === shown.setIndex ? then.exercise.name : 'rest';
  });
  // Why today's sets differ from the template ("Eased for today", a held
  // weight) beats the standing note: it explains a short ladder like 1 of 1.
  let posText = $derived(
    !w || !shown ? ''
    : shown.circuit
      ? [`${circuitName(shown.circuit)} · exercise ${shown.exerciseIndex - shown.circuit.start + 1} of ${shown.circuit.end - shown.circuit.start}`, thenText && `then ${thenText}`, shown.exercise.change || shown.exercise.notes].filter(Boolean).join(' · ')
      : [`Lift ${shown.exerciseIndex + 1} of ${w.session.plan.length}`, shown.exercise.change || shown.exercise.notes].filter(Boolean).join(' · '),
  );
  /** "set 2", or "round 2" for a set done in a circuit. */
  const setWord = (exerciseIndex: number, setIndex: number) =>
    `${w?.slots.find((s) => s.exerciseIndex === exerciseIndex)?.circuit ? 'round' : 'set'} ${setIndex + 1}`;
  // Moving straight on in a circuit (no rest) skips the rest screen, so the
  // set just logged can still be corrected from the ready screen.
  let correctable = $derived(c?.phase === 'rest' || c?.phase === 'last' || (c?.phase === 'ready' && !!slot?.circuit && newExercise));

  // Signal the end of rest once per rest period: haptics where the platform
  // allows, and a screen-reader announcement everywhere.
  let alerted = $state<number | null>(null);
  $effect(() => {
    if (restOver && c && alerted !== c.phase_started_at) {
      alerted = c.phase_started_at;
      buzz();
      chime();
      announce = slot ? `Rest over. ${slot.exercise.name}, ${unit.toLowerCase()} ${slot.setIndex + 1} of ${exerciseSlots.length}.` : 'Rest over.';
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

  // A circuit's rounds can change mid-session: one more after the last
  // planned round, or stop with the rounds done so far.
  const addRound = () =>
    act(async () => {
      const circ = w!.roundsDone!;
      await w!.addRound();
      announce = `${circuitName(circ)}: round ${circ.rounds + 1} added.`;
    });
  const endCircuit = () =>
    act(async () => {
      dialog?.close();
      const e = w!.endable!;
      await w!.endCircuit();
      announce = `${circuitName(e.circuit)} ended after ${roundsText(e.rounds)}.`;
    });
  // Between rounds (resting before the next one starts), ending is offered
  // in place, not only from End.
  let betweenRounds = $derived(!!slot && !!w?.lastSet && slot.setIndex > w.lastSet.set_index);

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

  // Space starts and finishes sets from a keyboard (an iPad on a stand),
  // unless focus is on a control that already uses it.
  function onKey(e: KeyboardEvent) {
    if (!c || e.code !== 'Space' || e.repeat || e.metaKey || e.ctrlKey || e.altKey || busy || dialog?.open) return;
    const t = e.target as HTMLElement | null;
    if (t?.closest('button, a, input, textarea, select, [role="button"], [role="radio"], [contenteditable="true"]')) return;
    if (c.phase === 'ready' || (c.phase === 'rest' && slot)) {
      e.preventDefault();
      start();
    } else if (c.phase === 'active') {
      e.preventDefault();
      finishSet();
    }
  }
</script>

<!-- The weight for the set about to start, changeable before it does. It
     carries to the rest of the exercise; the plan's weight stays the target. -->
{#snippet nextWeight()}
  {#if w && slot && target}
    {@const open = weighing === slot.index}
    <div class="next-weight">
      <div class="toggle-row">
        <button class="btn btn-quiet toggle" aria-expanded={open} aria-controls="next-weight-step" onclick={() => (weighing = open ? null : slot.index)}>
          {open ? 'Done' : 'Change weight'}
        </button>
        <p class="muted next-planned" aria-live="polite">
          {#if target.weight_kg !== slot.target.weight_kg}Planned {slot.target.weight_kg === 0 ? 'bodyweight' : `${weight(slot.target.weight_kg)} kg`}{/if}
        </p>
      </div>
      {#if open}
        <div id="next-weight-step" class="half">
          <Stepper
            label="Next set weight"
            unit={target.weight_kg === 0 ? 'Weight' : 'kg'}
            value={target.weight_kg === 0 ? 'BW' : weight(target.weight_kg)}
            canDecrease={target.weight_kg > 0}
            onstep={(d) => w!.adjustNext(d)}
          />
        </div>
      {/if}
    </div>
  {/if}
{/snippet}

{#snippet endText(e: { circuit: Circuit; rounds: number })}End {circuitName(e.circuit)} after {roundsText(e.rounds)}{/snippet}

<div class="visually-hidden" aria-live="assertive">{announce}</div>

<svelte:window onkeydown={onKey} />

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

    {#if shown && c.phase !== 'last'}
      <section class="exercise" aria-label="Current exercise">
        <h1 class="ex-name">{shown.exercise.name}</h1>
        <p class="mono ex-pos">
          {posText}
        </p>
        <ol class="ladder" aria-label={shown.circuit ? 'Rounds' : 'Sets'} style:--n={shownSlots.length}>
          {#each shownSlots as s (s.index)}
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
          <Plate n={slot.setIndex + 1} of={exerciseSlots.length} {unit} />
          <div class="target-big" aria-label={`Target ${setText(target.reps, target.weight_kg)}`}>
            <div class="tb"><span class="num live">{target.reps}</span><span class="label">Reps</span></div>
            <span class="times num" aria-hidden="true">×</span>
            <div class="tb"><span class="num live">{weight(target.weight_kg)}</span><span class="label">{target.weight_kg === 0 ? 'Bodyweight' : 'Kg'}</span></div>
          </div>
          {@render nextWeight()}
        {:else if c.phase === 'active' && slot && target}
          <Plate n={slot.setIndex + 1} of={exerciseSlots.length} {unit} active />
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
            <p class="label rest-label"><span class:signal-label={restOver}>{restOver ? 'Rest over' : 'Rest'}</span> <span class="mono muted">of {fmtClock(c.rest_s)}</span></p>
            <!-- Clock and its adjusters share a row: rest carries the logged
                 and next sets too, and they need the height. -->
            <div class="rest-row">
              <p class="num live" class:signal={restOver} aria-label={restOver ? `${Math.floor(-restLeft)} seconds over` : `${Math.ceil(restLeft)} seconds left`}>
                {restOver ? `+${fmtClock(-restLeft)}` : fmtClock(Math.ceil(restLeft))}
              </p>
              <span class="rest-adjust">
                <button class="btn" onclick={() => w!.addRest(-15)} disabled={c.rest_s <= 15} aria-label="15 seconds less rest">−15 s</button>
                <button class="btn" onclick={() => w!.addRest(15)} aria-label="15 seconds more rest">+15 s</button>
              </span>
            </div>
            <div class="drain" class:gone={restOver} role="progressbar" aria-label="Rest remaining" aria-valuemin={0} aria-valuemax={c.rest_s} aria-valuenow={Math.max(0, Math.ceil(restLeft))}>
              <div class="drain-fill" style:transform={`scaleX(${restFraction})`}></div>
            </div>
          </div>
        {:else if c.phase === 'last'}
          <div class="done">
            <h1 class="done-title">All sets in.</h1>
            <p class="muted">Check the last set, then finish. Heart rate attaches when your watch syncs.</p>
          </div>
        {/if}
      </section>
    {/key}

    {#if correctable && w.lastSet}
      {@const ls = w.lastSet}
      {@const open = fixing === ls.id}
      <section class="logged" aria-labelledby="logged-h">
        <!-- One line: what was logged. The steppers open only to fix it. -->
        <div class="toggle-row">
          <h2 id="logged-h" class="logged-h">
            <span class="label">Logged</span>
            <span class="num logged-num">{setText(ls.reps, ls.weight_kg)}</span>
            <span class="mono muted logged-where">{ls.exercise_name} · {setWord(ls.exercise_index, ls.set_index)}</span>
          </h2>
          <button class="btn btn-quiet toggle" aria-expanded={open} aria-controls="logged-fix" onclick={() => (fixing = open ? null : ls.id)}>
            {open ? 'Done' : 'Fix'}
          </button>
        </div>
        {#if open}
          <div id="logged-fix" class="steppers">
            <Stepper label="Logged reps" unit="Reps" value={String(ls.reps)} canDecrease={ls.reps > 0} onstep={(d) => w!.adjustLast('reps', d)} />
            <Stepper
              label="Logged weight"
              unit={ls.weight_kg === 0 ? 'Weight' : 'kg'}
              value={ls.weight_kg === 0 ? 'BW' : weight(ls.weight_kg)}
              canDecrease={ls.weight_kg > 0}
              onstep={(d) => w!.adjustLast('weight_kg', d * weightStep(ls.weight_kg, d))}
            />
          </div>
        {/if}
        <!-- Open, space is reserved so the note appearing never shifts the screen mid-glance. -->
        <p class="muted adj" class:reserve={open} aria-live="polite">
          {#if ls.reps !== ls.target_reps || ls.weight_kg !== ls.target_weight_kg}Target was {setText(ls.target_reps, ls.target_weight_kg)}{ls.weight_kg !== ls.target_weight_kg && w.carries(ls) ? '. New weight carries to the next sets.' : '.'}{/if}
        </p>
      </section>
    {/if}

    {#if w.roundsDone}
      {@const another = w.roundsDone}
      <section class="again" aria-label="Another round">
        <p class="muted">That was the last of {roundsText(another.rounds)} of {circuitName(another)}.</p>
        <button class="btn again-btn" onclick={addRound} disabled={busy}>Add round {another.rounds + 1}</button>
      </section>
    {/if}

    {#if c.phase === 'rest' && slot && target}
      <section class="next" aria-label="Next set">
        <p class="label">Next</p>
        {#if newExercise}
          <p class="next-main">{slot.exercise.name}</p>
          <p class="mono muted">{unit} {slot.setIndex + 1} of {exerciseSlots.length} · {setText(target.reps, target.weight_kg)}</p>
        {:else}
          <p class="next-main">Set {slot.setIndex + 1} of {exerciseSlots.length} · <span class="num next-num">{setText(target.reps, target.weight_kg)}</span></p>
        {/if}
        {@render nextWeight()}
        {#if betweenRounds && w.endable}
          <button class="btn btn-quiet end-circuit" onclick={endCircuit} disabled={busy}>{@render endText(w.endable)}</button>
        {/if}
      </section>
    {/if}
  </main>

  {#snippet key()}<kbd class="key" aria-hidden="true">Space</kbd>{/snippet}

  <ActionBar session>
    {#if c.phase === 'ready'}
      <button class="btn btn-primary btn-go" onclick={start} disabled={busy} aria-keyshortcuts="Space">Start set{@render key()}</button>
    {:else if c.phase === 'active'}
      <button class="btn btn-primary btn-stop" onclick={finishSet} disabled={busy} aria-keyshortcuts="Space">Finish set{@render key()}</button>
    {:else if c.phase === 'rest' && slot}
      <button class="btn btn-primary btn-go" onclick={start} disabled={busy} aria-keyshortcuts="Space">
        {newExercise ? `Start ${slot.exercise.name}` : `Start set ${slot.setIndex + 1}`}{@render key()}
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
        {#if w.endable}
          <button class="btn sheet-btn" onclick={endCircuit}>{@render endText(w.endable)}</button>
        {/if}
        {#if slot && c.phase !== 'active' && c.phase !== 'last'}
          <button class="btn sheet-btn" onclick={skipExercise}>Skip the rest of {slot.exercise.name}</button>
        {/if}
        <button class="btn sheet-btn" class:btn-danger={!discardable} onclick={() => { dialog?.close(); finishSession(); }}>
          Finish session now
        </button>
        {#if discardable}
          <button class="btn btn-danger sheet-btn" onclick={discard}>
            Discard session{w.sets.length ? ` (${w.sets.length} ${w.sets.length === 1 ? "set" : "sets"})` : ''}
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
    max-width: var(--page-narrow);
    margin: 0 auto;
    padding: calc(env(safe-area-inset-top) + var(--space-2)) var(--gutter) 0;
    display: flex;
    flex-direction: column;
    min-height: calc(100dvh - var(--action-height) - var(--space-6) - env(safe-area-inset-bottom));
  }
  /* An iPad propped on the rack is read from two metres: scale the whole
     screen up rather than leave a phone column in the middle. */
  @media (min-width: 700px) and (min-height: 700px) {
    .session {
      max-width: 46rem;
      --size-live: 8rem;
      --size-target: 4rem;
      --size-heading: 2rem;
      --size-body: 1.1875rem;
      --size-small: 1.1875rem;
      --size-label: 1.0625rem;
      font-size: var(--size-body);
      min-height: calc(100dvh - var(--action-height) * 1.25 - var(--space-6) - env(safe-area-inset-bottom));
    }
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
    border-color: var(--text);
  }
  .rung.skipped {
    text-decoration: line-through;
  }

  /* Hero */
  .hero {
    flex: 1;
    display: flex;
    flex-direction: column;
    /* Centred in what's left, so a tall phone splits the spare height above
       and below instead of leaving one empty block under the ladder. */
    justify-content: center;
    gap: var(--space-5);
    padding: var(--space-6) 0;
  }
  /* Rest carries more (logged set, next set, its weight): keep the clock
     tight so the next weight clears the action bar without scrolling. */
  .resting .hero {
    flex: 0 0 auto;
    gap: var(--space-3);
    padding: var(--space-4) 0 var(--space-3);
  }
  .live {
    font-size: var(--size-live);
    font-weight: 700;
  }
  .signal {
    color: var(--signal-ink);
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
    color: var(--text);
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
  }
  .live-dot {
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 50%;
    background: var(--stop); /* recording, like the red Finish set below */
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
  .rest-label .mono {
    margin-left: var(--space-2);
    letter-spacing: 0;
    text-transform: none;
  }
  .rest-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: var(--space-3);
  }
  .rest-adjust {
    display: flex;
    gap: var(--space-2);
  }
  /* Rest over: the clock turns signal and counts up; the solid Start set
     below stays the loudest thing on screen. */
  .rest.over .num {
    color: var(--signal-ink);
  }
  .rest.over .signal-label {
    color: var(--signal-ink);
  }
  .drain.gone {
    visibility: hidden; /* keeps its height: nothing jumps when rest ends */
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
  .toggle-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
  }
  .toggle {
    min-height: var(--tap-min);
    min-width: 5rem;
    margin-right: calc(var(--space-3) * -1);
    flex: none;
  }
  .logged-h {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    column-gap: var(--space-3);
    row-gap: var(--space-1);
    min-width: 0;
    font-size: var(--size-body);
    font-weight: 400;
  }
  .logged-num {
    font-size: var(--size-lead);
    font-weight: 600;
  }
  .logged-where {
    font-size: var(--size-small);
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
    color: var(--text);
  }
  .discard-note {
    font-size: var(--size-label);
    text-align: center;
  }
  .adj {
    margin-top: var(--space-1);
    font-size: var(--size-label);
  }
  .adj:empty {
    display: none;
  }
  .adj.reserve {
    display: block;
    min-height: 2.4em;
  }
  .next-weight {
    display: grid;
    gap: var(--space-2);
  }
  .next .next-weight {
    margin-top: var(--space-1);
  }
  .next-weight .toggle-row {
    justify-content: flex-start;
  }
  .next-weight .toggle {
    margin: 0 0 0 calc(var(--space-3) * -1);
  }
  .half {
    width: min(100%, 20rem);
  }
  .next-planned {
    font-size: var(--size-label);
  }
  .next {
    margin-top: var(--space-4);
    padding-bottom: var(--space-2);
  }
  .again {
    margin-top: var(--space-5);
    display: grid;
    gap: var(--space-2);
    font-size: var(--size-small);
  }
  .again-btn {
    min-height: var(--tap-min);
    justify-self: start;
    min-width: 12rem;
  }
  .end-circuit {
    margin-top: var(--space-3);
    min-height: var(--tap-min);
    margin-left: calc(var(--space-3) * -1);
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
  /* The shortcut shows only where there's likely a keyboard. */
  .key {
    display: none;
  }
  @media (hover: hover) and (pointer: fine) {
    .key {
      display: inline-block;
      margin-left: var(--space-3);
      padding: 0 var(--space-2);
      border: 1px solid currentColor;
      border-radius: var(--radius-s);
      font-family: var(--font-mono);
      font-size: var(--size-label);
      font-weight: 600;
      line-height: 1.6;
      opacity: 0.7;
      vertical-align: middle;
    }
  }
</style>
