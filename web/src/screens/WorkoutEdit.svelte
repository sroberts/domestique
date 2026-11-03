<script lang="ts">
  import { tick, untrack } from 'svelte';
  import ArrowDown from '@lucide/svelte/icons/arrow-down';
  import ArrowUp from '@lucide/svelte/icons/arrow-up';
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import Plus from '@lucide/svelte/icons/plus';
  import X from '@lucide/svelte/icons/x';
  import ActionBar from '../components/ActionBar.svelte';
  import Stepper from '../components/Stepper.svelte';
  import { AuthError, HTTPError, OfflineError, localDate } from '../lib/api';
  import { setText } from '../lib/format';
  import { link, router } from '../lib/router.svelte';
  import { sync } from '../lib/sync.svelte';
  import {
    deleteTemplate,
    fetchExercises,
    fetchPlan,
    fetchTemplate,
    fetchTemplates,
    planToday,
    saveTemplate,
    startTemplate,
    type ExerciseSuggestion,
  } from '../lib/templates';
  import { newId } from '../lib/workout.svelte';
  import type { Template } from '../lib/types';

  let { id }: { id: string } = $props();

  // Field values are kept as typed; they become numbers on save.
  interface DraftSet {
    key: number;
    reps: string;
    weight: string; // '' is bodyweight
  }
  interface DraftEx {
    key: number;
    name: string;
    rest: number;
    notes: string;
    sets: DraftSet[];
    /** Untouched since it was added: picking a known name may fill it in. */
    fresh: boolean;
  }
  type Load = 'loading' | 'ready' | 'offline' | 'auth' | 'missing' | 'error';

  // The screen is keyed on the route id, so it never changes under us.
  const routeId = untrack(() => id);
  const isNew = routeId === 'new';
  const fromId = isNew ? new URLSearchParams(location.search).get('from') : null;

  let load = $state<Load>('loading');
  let tid = $state(isNew ? newId() : routeId);
  let onServer = $state(!isNew);
  let name = $state('');
  let exercises = $state<DraftEx[]>([]);
  let suggestions = $state<ExerciseSuggestion[]>([]);
  let initial = $state('');
  let showErrors = $state(false);
  let saving = $state(false);
  let saveError = $state('');
  let announce = $state('');
  let removed = $state<{ ex: DraftEx; index: number } | null>(null);
  let leaving = false;
  let pendingTo = '/workouts';
  let keySeq = 0;

  let summaryEl = $state<HTMLDivElement | null>(null);
  let guardDialog = $state<HTMLDialogElement | null>(null);
  let savedDialog = $state<HTMLDialogElement | null>(null);
  let deleteDialog = $state<HTMLDialogElement | null>(null);
  let dialogBusy = $state(false);
  let dialogError = $state('');
  let deletePlanned = $state('');
  let deleteToday = $state(false);

  const kgText = (kg: number) => (kg === 0 ? '' : String(kg));

  function draftOf(e: Template['exercises'][number]): DraftEx {
    return {
      key: ++keySeq,
      name: e.name,
      rest: e.rest_seconds,
      notes: e.notes ?? '',
      sets: e.sets.map((s) => ({ key: ++keySeq, reps: String(s.reps), weight: kgText(s.weight_kg) })),
      fresh: false,
    };
  }

  function blank(): DraftEx {
    return {
      key: ++keySeq,
      name: '',
      rest: 120,
      notes: '',
      sets: [0, 1, 2].map(() => ({ key: ++keySeq, reps: '8', weight: '' })),
      fresh: true,
    };
  }

  function snapshot(): string {
    return JSON.stringify([name, exercises.map((e) => [e.name, e.rest, e.notes, e.sets.map((s) => [s.reps, s.weight])])]);
  }

  let dirty = $derived(load === 'ready' && snapshot() !== initial);

  $effect(() => {
    void loadDraft();
  });

  async function loadDraft() {
    load = 'loading';
    try {
      const source = isNew ? fromId : routeId;
      const [sug, t] = await Promise.all([fetchExercises(), source ? fetchTemplate(source) : Promise.resolve(null)]);
      suggestions = sug;
      if (t) {
        name = isNew ? `${t.name} copy` : t.name;
        exercises = t.exercises.map(draftOf);
      } else {
        name = '';
        exercises = [blank()];
      }
      // A duplicate isn't saved yet, so leaving it asks first.
      initial = fromId ? '' : snapshot();
      load = 'ready';
      if (isNew) {
        await tick();
        document.getElementById('wk-name')?.focus();
      }
    } catch (err) {
      load =
        err instanceof OfflineError ? 'offline'
        : err instanceof AuthError ? 'auth'
        : err instanceof HTTPError && err.status === 404 ? 'missing'
        : 'error';
    }
  }

  // Unsaved changes hold navigation (in-app, Back, and closing the page).
  $effect(() => {
    router.leaveGuard = (to) => {
      if (!dirty || leaving) return true;
      pendingTo = to;
      guardDialog?.showModal();
      return false;
    };
    const onUnload = (e: BeforeUnloadEvent) => {
      if (dirty && !leaving) e.preventDefault();
    };
    window.addEventListener('beforeunload', onUnload);
    return () => {
      router.leaveGuard = null;
      window.removeEventListener('beforeunload', onUnload);
    };
  });

  function leave(to: string) {
    leaving = true;
    if (to === '/workouts' && router.prev === '/workouts') history.back();
    else router.go(to, { replace: true });
  }

  function discardAndLeave() {
    guardDialog?.close();
    leaving = true;
    router.go(pendingTo);
  }

  function flash(msg: string) {
    try {
      sessionStorage.setItem('domestique:flash', msg);
    } catch {
      /* ignore */
    }
  }

  // ---- Parsing and validation ----

  function parseReps(v: string): number | null {
    const t = v.trim();
    if (!/^\d+$/.test(t)) return null;
    const n = Number(t);
    return n >= 1 && n <= 100 ? n : null;
  }

  /** kg, 0 for bodyweight; null when not a weight we accept. */
  function parseWeight(v: string): number | null {
    const t = v.trim().replace(',', '.').toLowerCase();
    if (t === '' || t === 'bw') return 0;
    if (!/^\d+(\.\d+)?$/.test(t)) return null;
    const n = Number(t);
    if (n > 1000) return null;
    return Math.abs(n * 4 - Math.round(n * 4)) < 1e-9 ? n : null;
  }

  function weightError(v: string): string {
    const t = v.trim().replace(',', '.');
    if (/^\d+(\.\d+)?$/.test(t) && Number(t) > 1000) return 'Weight must be 1000 kg or less.';
    if (/^\d+(\.\d+)?$/.test(t)) return 'Use steps of 0.25 kg.';
    return 'Enter kg, or leave empty for bodyweight.';
  }

  const exLabel = (e: DraftEx, i: number) => e.name.trim() || `Exercise ${i + 1}`;

  function validate(): Record<string, string> {
    const err: Record<string, string> = {};
    if (!name.trim()) err['wk-name'] = 'Give the workout a name.';
    else if (name.trim().length > 80) err['wk-name'] = 'Keep the name to 80 characters.';
    if (exercises.length === 0) err['add-ex'] = 'Add at least one exercise.';
    exercises.forEach((e) => {
      if (!e.name.trim()) err[`ex${e.key}-name`] = 'Name this exercise.';
      else if (e.name.trim().length > 80) err[`ex${e.key}-name`] = 'Keep the name to 80 characters.';
      e.sets.forEach((s) => {
        if (parseReps(s.reps) === null) err[`ex${e.key}-s${s.key}-reps`] = 'Reps must be a whole number from 1 to 100.';
        if (parseWeight(s.weight) === null) err[`ex${e.key}-s${s.key}-kg`] = weightError(s.weight);
      });
    });
    return err;
  }

  let errors = $derived(showErrors ? validate() : {});
  let errorList = $derived(Object.entries(errors));

  /** Error text for a field: the reps and weight of one set share a line. */
  function setErrors(e: DraftEx, s: DraftSet): string {
    return [errors[`ex${e.key}-s${s.key}-reps`], errors[`ex${e.key}-s${s.key}-kg`]].filter(Boolean).join(' ');
  }

  function fieldName(fid: string): string {
    if (fid === 'wk-name') return 'Name';
    if (fid === 'add-ex') return 'Exercises';
    const m = /^ex(\d+)(?:-s(\d+)-(reps|kg))?/.exec(fid);
    const i = exercises.findIndex((e) => e.key === Number(m?.[1]));
    const e = exercises[i];
    if (!e) return 'Field';
    if (!m?.[2]) return `Exercise ${i + 1} name`;
    const n = e.sets.findIndex((s) => s.key === Number(m[2])) + 1;
    return `${exLabel(e, i)}, set ${n} ${m[3] === 'reps' ? 'reps' : 'weight'}`;
  }

  function toTemplate(): Template {
    return {
      id: tid,
      name: name.trim(),
      updated_at: 0,
      exercises: exercises.map((e) => ({
        name: e.name.trim(),
        rest_seconds: e.rest,
        ...(e.notes.trim() ? { notes: e.notes.trim() } : {}),
        sets: e.sets.map((s) => ({ reps: parseReps(s.reps) ?? 0, weight_kg: parseWeight(s.weight) ?? 0 })),
      })),
    };
  }

  function failText(err: unknown): string {
    if (err instanceof OfflineError) return "Couldn't reach the Mac Mini. Your edits are still here; try again when you're connected.";
    if (err instanceof AuthError) return 'Your Outpost sign-in has expired. Sign in again, then save.';
    return err instanceof Error ? err.message : 'Something went wrong.';
  }

  async function save() {
    if (saving) return;
    showErrors = true;
    saveError = '';
    if (Object.keys(validate()).length > 0) {
      await tick();
      summaryEl?.focus();
      return;
    }
    saving = true;
    try {
      await saveTemplate(toTemplate());
    } catch (err) {
      saveError = failText(err);
      saving = false;
      return;
    }
    saving = false;
    initial = snapshot();
    void fetchTemplates(); // refresh the offline copy
    if (!onServer) {
      onServer = true;
      history.replaceState(null, '', `/workouts/${tid}`);
      dialogError = '';
      savedDialog?.showModal();
    } else {
      flash(`Saved ${name.trim()}.`);
      leave('/workouts');
    }
  }

  // ---- Editing ----

  function say(msg: string) {
    announce = '';
    void tick().then(() => (announce = msg));
  }

  async function focus(fid: string) {
    await tick();
    document.getElementById(fid)?.focus();
  }

  function setField(e: DraftEx, i: number, field: 'reps' | 'weight', value: string) {
    e.fresh = false;
    const first = e.sets[0];
    // While every set matches, set 1 speaks for all of them.
    if (i === 0 && first && e.sets.length > 1 && e.sets.every((s) => s[field] === first[field])) {
      for (const s of e.sets) s[field] = value;
    } else {
      const s = e.sets[i];
      if (s) s[field] = value;
    }
  }

  function tidyWeight(s: DraftSet) {
    const w = parseWeight(s.weight);
    if (w === 0) s.weight = '';
    else if (w !== null) s.weight = String(w);
  }

  function pickName(e: DraftEx) {
    if (!e.fresh) return;
    const sug = suggestions.find((x) => x.name.toLowerCase() === e.name.trim().toLowerCase());
    if (!sug || sug.sets.length === 0) return;
    e.sets = sug.sets.map((s) => ({ key: ++keySeq, reps: String(s.reps), weight: kgText(s.weight_kg) }));
    e.rest = sug.rest_seconds;
    e.fresh = false;
    const s0 = sug.sets[0]!;
    say(`Filled in from ${sug.last_at ? 'last time' : 'your other workouts'}: ${sug.sets.length} × ${setText(s0.reps, s0.weight_kg).replace(/^\d+ × /, `${s0.reps} at `)}.`);
  }

  function addSet(e: DraftEx) {
    const last = e.sets.at(-1);
    e.sets.push({ key: ++keySeq, reps: last?.reps ?? '8', weight: last?.weight ?? '' });
    e.fresh = false;
    say(`Set ${e.sets.length} added.`);
  }

  function removeSet(e: DraftEx, i: number) {
    if (e.sets.length <= 1) return;
    e.sets.splice(i, 1);
    e.fresh = false;
    say(`Set ${i + 1} removed. ${e.sets.length} left.`);
    void focus(`ex${e.key}-add`);
  }

  function addExercise() {
    const e = blank();
    exercises.push(e);
    removed = null;
    void focus(`ex${e.key}-name`);
  }

  function move(i: number, dir: -1 | 1) {
    const j = i + dir;
    const a = exercises[i];
    const b = exercises[j];
    if (!a || !b) return;
    exercises[i] = b;
    exercises[j] = a;
    say(`${exLabel(a, j)} moved to ${j + 1} of ${exercises.length}.`);
    const canKeep = dir === -1 ? j > 0 : j < exercises.length - 1;
    void focus(`ex${a.key}-${(dir === -1) === canKeep ? 'up' : 'down'}`);
  }

  function removeExercise(i: number) {
    const e = exercises[i];
    if (!e) return;
    exercises.splice(i, 1);
    removed = { ex: e, index: i };
    say(`${exLabel(e, i)} removed.`);
    const next = exercises[i] ?? exercises[i - 1];
    void focus(next ? `ex${next.key}-name` : 'add-ex');
  }

  function undoRemove() {
    if (!removed) return;
    const { ex, index } = removed;
    exercises.splice(Math.min(index, exercises.length), 0, ex);
    removed = null;
    say(`${exLabel(ex, index)} is back.`);
    void focus(`ex${ex.key}-name`);
  }

  // ---- After save, duplicate, delete ----

  async function startNow() {
    dialogBusy = true;
    leaving = true;
    const s = await startTemplate(toTemplate());
    savedDialog?.close();
    router.go(`/session/${s.id}`, { replace: true });
  }

  async function makePlan() {
    dialogBusy = true;
    dialogError = '';
    try {
      await planToday(tid);
    } catch (err) {
      dialogError = failText(err);
      dialogBusy = false;
      return;
    }
    savedDialog?.close();
    leaving = true;
    router.go('/', { replace: true });
  }

  function doneAfterSave() {
    savedDialog?.close();
    flash(`Saved ${name.trim()}.`);
    leave('/workouts');
  }

  async function askDelete() {
    dialogError = '';
    deletePlanned = '';
    deleteToday = false;
    const p = await fetchPlan(localDate());
    deleteToday = !p.stale && p.data?.template_id === tid;
    deleteDialog?.showModal();
  }

  async function confirmDelete() {
    dialogBusy = true;
    dialogError = '';
    try {
      const refused = await deleteTemplate(tid, deleteToday || !!deletePlanned);
      if (refused) {
        deletePlanned = refused.planned;
        dialogBusy = false;
        return;
      }
    } catch (err) {
      dialogError = failText(err);
      dialogBusy = false;
      return;
    }
    deleteDialog?.close();
    void fetchTemplates();
    flash(`Deleted ${name.trim() || 'the workout'}.`);
    leave('/workouts');
  }
</script>

<div class="visually-hidden" aria-live="polite">{announce}</div>

<main class="page">
  <header class="top">
    <a class="back" href="/workouts" use:link><ChevronLeft size={20} aria-hidden="true" />Workouts</a>
  </header>

  {#if load === 'loading'}
    <h1>{isNew ? 'New workout' : 'Edit workout'}</h1>
  {:else if load === 'offline'}
    <section class="state">
      <h1>Editing needs the Mac Mini</h1>
      <p class="muted">You're offline. Saved workouts can still be started from Workouts; changes wait until you're connected.</p>
      <div class="row-btns">
        <button class="btn" onclick={() => loadDraft()}>Try again</button>
        <a class="btn btn-quiet" href="/workouts" use:link>Back to workouts</a>
      </div>
    </section>
  {:else if load === 'auth'}
    <section class="state">
      <h1>Sign in to edit</h1>
      <p class="muted">Your Outpost sign-in has expired.</p>
      <button class="btn" onclick={() => location.reload()}>Sign in again</button>
    </section>
  {:else if load === 'missing'}
    <section class="state">
      <h1>This workout is gone</h1>
      <p class="muted">It may have been deleted. Past sessions of it are still in your history.</p>
      <a class="btn" href="/workouts" use:link>Back to workouts</a>
    </section>
  {:else if load === 'error'}
    <section class="state">
      <h1>Couldn't load this workout</h1>
      <button class="btn" onclick={() => loadDraft()}>Try again</button>
    </section>
  {:else}
    <h1>{isNew && !onServer ? (fromId ? 'Copy of workout' : 'New workout') : 'Edit workout'}</h1>

    {#if sync.status === 'offline'}
      <p class="notice">You're offline. Saving needs the Mac Mini; your edits stay on this screen until then.</p>
    {/if}

    {#if errorList.length > 0}
      <div class="summary" bind:this={summaryEl} tabindex="-1" role="alert" aria-labelledby="sum-h">
        <h2 id="sum-h">{errorList.length === 1 ? 'One thing to fix' : `${errorList.length} things to fix`} before saving</h2>
        <ul>
          {#each errorList as [fid, msg] (fid)}
            <li><button class="linkish" onclick={() => document.getElementById(fid)?.focus()}>{fieldName(fid)}: {msg}</button></li>
          {/each}
        </ul>
      </div>
    {/if}

    <div class="field">
      <label class="fl" for="wk-name">Name</label>
      <input
        id="wk-name"
        class="text"
        bind:value={name}
        maxlength="80"
        autocomplete="off"
        placeholder="Upper B"
        aria-invalid={!!errors['wk-name']}
        aria-describedby={errors['wk-name'] ? 'wk-name-err' : undefined}
      />
      {#if errors['wk-name']}<p id="wk-name-err" class="err">{errors['wk-name']}</p>{/if}
    </div>

    <datalist id="exercise-names">
      {#each suggestions as s (s.name)}<option value={s.name}></option>{/each}
    </datalist>

    <ol class="exercises" aria-label="Exercises">
      {#each exercises as e, i (e.key)}
        {@const label = exLabel(e, i)}
        <li class="ex" aria-labelledby={`ex${e.key}-n ex${e.key}-name`}>
          <div class="ex-head">
            <span class="num ex-n" id={`ex${e.key}-n`} aria-hidden="true">{i + 1}</span>
            <div class="ex-name">
              <label class="visually-hidden" for={`ex${e.key}-name`}>Exercise {i + 1} name</label>
              <input
                id={`ex${e.key}-name`}
                class="text"
                list="exercise-names"
                bind:value={e.name}
                onchange={() => pickName(e)}
                maxlength="80"
                autocomplete="off"
                autocapitalize="sentences"
                placeholder="Exercise name"
                aria-invalid={!!errors[`ex${e.key}-name`]}
                aria-describedby={errors[`ex${e.key}-name`] ? `ex${e.key}-name-err` : undefined}
              />
              {#if errors[`ex${e.key}-name`]}<p id={`ex${e.key}-name-err`} class="err">{errors[`ex${e.key}-name`]}</p>{/if}
            </div>
          </div>

          <div class="ex-body">
            <div class="sets" role="group" aria-label={`Sets for ${label}`}>
              <div class="set-row set-head" aria-hidden="true">
                <span class="label">Set</span><span class="label">Reps</span><span></span><span class="label">Kg</span><span></span>
              </div>
              {#each e.sets as s, j (s.key)}
                {@const se = setErrors(e, s)}
                {@const eid = `ex${e.key}-s${s.key}-err`}
                <div class="set-row">
                  <span class="num set-n" aria-hidden="true">{j + 1}</span>
                  <input
                    id={`ex${e.key}-s${s.key}-reps`}
                    class="text numin"
                    inputmode="numeric"
                    pattern="[0-9]*"
                    autocomplete="off"
                    value={s.reps}
                    oninput={(ev) => setField(e, j, 'reps', ev.currentTarget.value)}
                    aria-label={`${label}, set ${j + 1} reps`}
                    aria-invalid={!!errors[`ex${e.key}-s${s.key}-reps`]}
                    aria-describedby={errors[`ex${e.key}-s${s.key}-reps`] ? eid : undefined}
                  />
                  <span class="times" aria-hidden="true">×</span>
                  <input
                    id={`ex${e.key}-s${s.key}-kg`}
                    class="text numin"
                    inputmode="decimal"
                    autocomplete="off"
                    placeholder="BW"
                    value={s.weight}
                    oninput={(ev) => setField(e, j, 'weight', ev.currentTarget.value)}
                    onblur={() => tidyWeight(s)}
                    aria-label={`${label}, set ${j + 1} weight in kg, empty for bodyweight`}
                    aria-invalid={!!errors[`ex${e.key}-s${s.key}-kg`]}
                    aria-describedby={errors[`ex${e.key}-s${s.key}-kg`] ? eid : undefined}
                  />
                  <button class="icon" onclick={() => removeSet(e, j)} disabled={e.sets.length <= 1} aria-label={`Remove set ${j + 1} of ${label}`}>
                    <X size={20} aria-hidden="true" />
                  </button>
                </div>
                {#if se}<p id={eid} class="err set-err">{se}</p>{/if}
              {/each}
              <button class="btn add-set" id={`ex${e.key}-add`} onclick={() => addSet(e)} aria-label={`Add a set to ${label}`}>
                <Plus size={18} aria-hidden="true" /> Add set
              </button>
              {#if e.sets.length > 1 && i === 0}
                <p class="hint muted">While every set matches, editing set 1 changes them all.</p>
              {/if}
            </div>

            <div class="ex-side">
              <Stepper
                label="Rest seconds"
                value={String(e.rest)}
                canDecrease={e.rest > 0}
                onstep={(d) => (e.rest = Math.max(0, Math.min(900, e.rest + d * 15)))}
              />
              <div class="field">
                <label class="fl" for={`ex${e.key}-notes`}>Notes <span class="muted">(optional)</span></label>
                <input id={`ex${e.key}-notes`} class="text notes" bind:value={e.notes} maxlength="500" autocomplete="off" />
              </div>
            </div>
          </div>

          <div class="ex-tools">
            <button class="btn btn-quiet" id={`ex${e.key}-up`} onclick={() => move(i, -1)} disabled={i === 0} aria-label={`Move ${label} up`}>
              <ArrowUp size={18} aria-hidden="true" /> Up
            </button>
            <button class="btn btn-quiet" id={`ex${e.key}-down`} onclick={() => move(i, 1)} disabled={i === exercises.length - 1} aria-label={`Move ${label} down`}>
              <ArrowDown size={18} aria-hidden="true" /> Down
            </button>
            <button class="btn btn-quiet remove" onclick={() => removeExercise(i)} aria-label={`Remove ${label}`}>Remove</button>
          </div>
        </li>
      {/each}
    </ol>

    <button
      class="btn add-ex"
      id="add-ex"
      onclick={addExercise}
      aria-describedby={errors['add-ex'] ? 'add-ex-err' : undefined}
    ><Plus size={20} aria-hidden="true" /> Add exercise</button>
    {#if errors['add-ex']}<p id="add-ex-err" class="err">{errors['add-ex']}</p>{/if}

    {#if onServer}
      <div class="more">
        <a class="btn" href={`/workouts/new?from=${encodeURIComponent(tid)}`} use:link>Duplicate</a>
        <button class="btn btn-danger" onclick={askDelete}>Delete workout</button>
      </div>
    {/if}
  {/if}
</main>

{#if load === 'ready'}
  <ActionBar>
    {#if removed}
      <div class="undo" role="status">
        <span>{exLabel(removed.ex, removed.index)} removed.</span>
        <button class="btn" onclick={undoRemove}>Undo</button>
      </div>
    {/if}
    {#if saveError}<p class="err bar-err" role="alert">{saveError}</p>{/if}
    <button class="btn btn-primary" onclick={save} disabled={saving}>{saving ? 'Saving…' : 'Save workout'}</button>
  </ActionBar>
{/if}

<dialog bind:this={guardDialog} class="sheet" aria-labelledby="guard-h" onclick={(ev) => ev.target === guardDialog && guardDialog?.close()}>
  <div class="sheet-inner">
    <h2 id="guard-h">Discard changes?</h2>
    <p class="muted">{fromId && !onServer ? "This copy isn't saved yet." : "Your edits aren't saved."}</p>
    <div class="sheet-actions">
      <button class="btn btn-danger sheet-btn" onclick={discardAndLeave}>Discard changes</button>
      <!-- svelte-ignore a11y_autofocus -->
      <button class="btn sheet-btn" autofocus onclick={() => guardDialog?.close()}>Keep editing</button>
    </div>
  </div>
</dialog>

<dialog bind:this={savedDialog} class="sheet" aria-labelledby="saved-h">
  <div class="sheet-inner">
    <h2 id="saved-h">Saved {name.trim()}</h2>
    <p class="muted">Start it now, or make it today's plan.</p>
    {#if dialogError}<p class="err" role="alert">{dialogError}</p>{/if}
    <div class="sheet-actions">
      <button class="btn btn-primary" onclick={startNow} disabled={dialogBusy}>Start now</button>
      <button class="btn sheet-btn" onclick={makePlan} disabled={dialogBusy}>Make today's plan</button>
      <button class="btn btn-quiet sheet-btn" onclick={doneAfterSave} disabled={dialogBusy}>Done</button>
    </div>
  </div>
</dialog>

<dialog bind:this={deleteDialog} class="sheet" aria-labelledby="del-h" onclick={(ev) => ev.target === deleteDialog && deleteDialog?.close()}>
  <div class="sheet-inner">
    <h2 id="del-h">Delete {name.trim() || 'this workout'}?</h2>
    <p class="muted">Past sessions keep their own copy, so your history stays.</p>
    {#if deletePlanned}
      <p role="alert">{deletePlanned} Deleting clears those plans too.</p>
    {:else if deleteToday}
      <p>It's today's plan, so today's plan is cleared too.</p>
    {/if}
    {#if dialogError}<p class="err" role="alert">{dialogError}</p>{/if}
    <div class="sheet-actions">
      <button class="btn btn-danger sheet-btn" onclick={confirmDelete} disabled={dialogBusy}>
        {deletePlanned || deleteToday ? 'Delete and clear plan' : 'Delete workout'}
      </button>
      <!-- svelte-ignore a11y_autofocus -->
      <button class="btn sheet-btn" autofocus onclick={() => deleteDialog?.close()}>Keep it</button>
    </div>
  </div>
</dialog>

<style>
  .page {
    max-width: var(--page-list);
    margin: 0 auto;
    padding: calc(env(safe-area-inset-top) + var(--space-2)) var(--gutter) 0;
  }
  /* Keep a focused field clear of the fixed Save bar and tab bar. */
  .page :global(:is(input, button, a)) {
    scroll-margin-bottom: calc(var(--action-height) + var(--tabbar-h) + var(--space-7) + env(safe-area-inset-bottom));
  }
  .top {
    display: flex;
    align-items: center;
    min-height: 2.75rem;
  }
  .back {
    display: inline-flex;
    align-items: center;
    gap: var(--space-1);
    min-height: 2.75rem;
    margin-left: calc(var(--space-2) * -1);
    padding: 0 var(--space-2);
    color: var(--text-muted);
    font-weight: 600;
    text-decoration: none;
  }
  .back:hover {
    color: var(--text);
  }
  h1 {
    font-size: var(--size-heading);
    margin-top: var(--space-2);
  }
  .state {
    display: grid;
    gap: var(--space-3);
    justify-items: start;
    margin-top: var(--space-2);
  }
  .state p {
    max-width: 50ch;
  }
  .row-btns {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  .notice {
    margin-top: var(--space-4);
    padding: var(--space-3) var(--space-4);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    font-size: var(--size-small);
  }
  .summary {
    margin-top: var(--space-4);
    padding: var(--space-3) var(--space-4);
    border: 1px solid var(--danger);
    border-radius: var(--radius-m);
  }
  .summary h2 {
    font-size: var(--size-body);
  }
  .summary ul {
    margin: var(--space-2) 0 0;
    padding-left: 1.25rem;
    font-size: var(--size-small);
  }
  .linkish {
    min-height: 2.75rem;
    padding: 0;
    border: 0;
    background: none;
    color: inherit;
    text-align: left;
    text-decoration: underline;
    text-underline-offset: 3px;
    cursor: pointer;
  }
  .field {
    display: grid;
    gap: var(--space-1);
    margin-top: var(--space-4);
  }
  .fl {
    font-size: var(--size-small);
    font-weight: 600;
  }
  .text {
    width: 100%;
    min-width: 0;
    min-height: 3rem;
    padding: 0 var(--space-3);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    background: var(--surface);
    color: var(--text);
    font: inherit;
    font-size: var(--size-lead);
  }
  .text::placeholder {
    color: var(--text-muted);
  }
  .text[aria-invalid='true'] {
    border-color: var(--danger);
  }
  .err {
    color: var(--danger);
    font-size: var(--size-small);
  }
  .exercises {
    list-style: none;
    margin: var(--space-5) 0 0;
    padding: 0;
    border-top: 1px solid var(--line-strong);
  }
  .ex {
    padding: var(--space-4) 0 var(--space-2);
    border-bottom: 1px solid var(--line-strong);
  }
  .ex-head {
    display: grid;
    grid-template-columns: 2rem minmax(0, 1fr);
    align-items: start;
    gap: var(--space-2);
  }
  .ex-n {
    font-size: var(--size-stat);
    line-height: 3rem;
    text-align: center;
  }
  .ex-name {
    display: grid;
    gap: var(--space-1);
  }
  .ex-name .text {
    font-weight: 600;
  }
  .ex-body {
    display: grid;
    gap: var(--space-4);
    margin-top: var(--space-3);
  }
  .set-row {
    display: grid;
    grid-template-columns: 2rem minmax(0, 1fr) 1rem minmax(0, 1.3fr) 2.75rem;
    align-items: center;
    gap: var(--space-2);
    margin-top: var(--space-2);
  }
  .set-head {
    margin-top: 0;
  }
  .set-head .label:first-child {
    text-align: center;
  }
  .set-n {
    font-size: var(--size-heading);
    text-align: center;
    color: var(--text-muted);
  }
  .numin {
    font-family: var(--font-num);
    font-weight: 600;
    font-size: 1.5rem;
    font-variant-numeric: tabular-nums;
    text-align: center;
    padding: 0 var(--space-2);
  }
  .numin::placeholder {
    font-family: var(--font-num);
  }
  .times {
    text-align: center;
    color: var(--text-muted);
    font-family: var(--font-num);
    font-size: 1.25rem;
  }
  .icon {
    display: grid;
    place-items: center;
    width: 2.75rem;
    height: 2.75rem;
    border: 0;
    border-radius: var(--radius-m);
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
  }
  .icon:hover:not(:disabled) {
    color: var(--text);
    background: var(--band);
  }
  .icon:disabled {
    color: var(--line-strong);
    cursor: default;
  }
  .set-err {
    margin: var(--space-1) 0 0 calc(2rem + var(--space-2));
  }
  .add-set {
    margin: var(--space-3) 0 0 calc(2rem + var(--space-2));
  }
  .hint {
    margin: var(--space-2) 0 0 calc(2rem + var(--space-2));
    font-size: var(--size-small);
  }
  .ex-side {
    display: grid;
    gap: var(--space-2);
    align-content: start;
  }
  .ex-side .field {
    margin-top: 0;
  }
  .notes {
    font-size: var(--size-body);
  }
  .ex-tools {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1);
    margin-top: var(--space-3);
    margin-left: calc(var(--space-2) * -1);
  }
  .ex-tools .remove {
    margin-left: auto;
  }
  .add-ex {
    width: 100%;
    min-height: var(--tap-min);
    margin-top: var(--space-4);
    border-style: dashed;
  }
  .more {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-top: var(--space-6);
    padding-top: var(--space-4);
    border-top: 1px solid var(--line);
  }
  .undo {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    padding: var(--space-1) var(--space-1) var(--space-1) var(--space-4);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    background: var(--surface);
    font-size: var(--size-small);
  }
  .bar-err {
    padding: var(--space-2) var(--space-3);
    background: var(--surface);
    border-radius: var(--radius-m);
  }
  @media (min-width: 700px) {
    .ex-body {
      grid-template-columns: minmax(0, 1.4fr) minmax(0, 1fr);
      gap: var(--space-6);
    }
  }
  @media (min-width: 900px) {
    .page {
      max-width: var(--page-wide);
    }
  }

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
