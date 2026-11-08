<script lang="ts">
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import X from '@lucide/svelte/icons/x';
  import ActionBar from '../components/ActionBar.svelte';
  import { AuthError, HTTPError, OfflineError, getJSON, postForJSON } from '../lib/api';
  import { link, router } from '../lib/router.svelte';
  import type { LoggedDraft } from '../lib/types';

  // A workout already done, written the way the athlete would jot it down.
  // Outpost's LLM reads it into a draft; nothing is saved until the athlete
  // has checked it and pressed Save.

  type DraftSet = { key: number; reps: string; weight: string };
  type DraftEx = { key: number; name: string; sets: DraftSet[] };

  let aiOn = $state<boolean | null>(null);
  let typed = $state('');
  let reading = $state(false);
  let saving = $state(false);
  let error = $state('');
  let read = $state(false);
  let name = $state('');
  let when = $state('');
  let minutes = $state('');
  let exercises = $state<DraftEx[]>([]);
  let notLogged = $state<string[]>([]);
  let keySeq = 0;

  $effect(() => {
    getJSON<{ ai?: boolean }>('/v1/me').then(
      (m) => (aiOn = !!m.ai),
      () => (aiOn = false),
    );
  });

  const pad = (n: number) => String(n).padStart(2, '0');
  /** Epoch ms as a datetime-local value, in the phone's time zone. */
  function localValue(ms: number): string {
    const d = new Date(ms);
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
  }
  const maxWhen = localValue(Date.now());

  function failText(err: unknown, what: string): string {
    if (err instanceof OfflineError) return `No connection. ${what} needs the Mac Mini.`;
    if (err instanceof AuthError) return 'Your Outpost sign-in has expired. Reload to sign in again.';
    if (err instanceof HTTPError) return err.message;
    return "Something went wrong. Try again.";
  }

  async function readText() {
    if (!typed.trim() || reading) return;
    reading = true;
    error = '';
    try {
      const d = await postForJSON<LoggedDraft>('/v1/sessions/parse-log', { text: typed });
      name = d.name;
      when = localValue(d.started_at);
      minutes = String(d.minutes);
      exercises = d.exercises.map((e) => ({
        key: ++keySeq,
        name: e.name,
        sets: e.sets.map((s) => ({ key: ++keySeq, reps: String(s.reps), weight: s.weight_kg ? String(s.weight_kg) : '' })),
      }));
      notLogged = d.not_logged ?? [];
      read = true;
    } catch (err) {
      error = failText(err, 'Reading a workout');
    } finally {
      reading = false;
    }
  }

  function removeSet(e: DraftEx, s: DraftSet) {
    e.sets = e.sets.filter((x) => x.key !== s.key);
    if (e.sets.length === 0) exercises = exercises.filter((x) => x.key !== e.key);
  }

  /** The draft as the server takes it, or a message saying what's wrong. */
  function toDraft(): LoggedDraft | string {
    const start = new Date(when).getTime();
    const mins = Number(minutes);
    if (!name.trim()) return 'Give the workout a name.';
    if (!Number.isFinite(start)) return 'Say when you did it.';
    if (start > Date.now() + 5 * 60_000) return "That's in the future.";
    if (!Number.isInteger(mins) || mins < 1 || mins > 300) return 'Minutes must be a whole number from 1 to 300.';
    if (exercises.length === 0) return 'There are no exercises left to log.';
    const out: LoggedDraft = { name: name.trim(), started_at: start, minutes: mins, exercises: [] };
    for (const e of exercises) {
      if (!e.name.trim()) return 'Every exercise needs a name.';
      const sets = [];
      for (const s of e.sets) {
        const reps = Number(s.reps);
        const kg = s.weight.trim() === '' ? 0 : Number(s.weight);
        if (!Number.isInteger(reps) || reps < 1) return `${e.name}: reps must be a whole number of 1 or more.`;
        if (!Number.isFinite(kg) || kg < 0) return `${e.name}: weight must be 0 or more.`;
        sets.push({ reps, weight_kg: kg });
      }
      out.exercises.push({ name: e.name.trim(), rest_seconds: 0, sets });
    }
    return out;
  }

  async function save() {
    if (saving) return;
    const d = toDraft();
    if (typeof d === 'string') {
      error = d;
      return;
    }
    saving = true;
    error = '';
    try {
      const r = await postForJSON<{ id: string }>('/v1/sessions/logged', d);
      router.go(`/session/${r.id}/summary`, { replace: true });
    } catch (err) {
      error = failText(err, 'Saving');
      saving = false;
    }
  }

  let setTotal = $derived(exercises.reduce((n, e) => n + e.sets.length, 0));
</script>

<main class="page">
  <header class="top">
    <a class="back" href="/workouts" use:link><ChevronLeft size={20} aria-hidden="true" />Workouts</a>
  </header>
  <h1>Log a finished workout</h1>

  {#if aiOn === false}
    <p class="lead muted">Reading a workout from text needs a model from Outpost, and none is available right now.</p>
  {:else}
    <p class="lead muted">Write down what you did. You'll check it before anything is saved.</p>
    <label class="fl" for="log-text">What did you do?</label>
    <textarea id="log-text" class="text" rows="4" maxlength="4000" bind:value={typed} disabled={reading || saving}
      placeholder="This morning: 3x10 push-ups, squats 5x5 at 80, last set only 3. Then a 20 min walk."></textarea>
    <button class="btn" class:btn-primary={!read} onclick={readText} disabled={reading || saving || !typed.trim() || aiOn === null} aria-busy={reading}>
      {reading ? 'Reading…' : read ? 'Read it again' : 'Read it'}
    </button>
  {/if}

  <p class="err" role="alert">{error}</p>

  {#if read}
    <section class="draft" aria-labelledby="draft-h">
      <h2 id="draft-h">Check it</h2>
      <div class="fields">
        <label class="field">
          <span class="fl">Name</span>
          <input class="text" bind:value={name} maxlength="80" />
        </label>
        <label class="field">
          <span class="fl">Started</span>
          <input class="text" type="datetime-local" bind:value={when} max={maxWhen} />
        </label>
        <label class="field narrow">
          <span class="fl">Minutes</span>
          <input class="text" type="number" inputmode="numeric" min="1" max="300" bind:value={minutes} />
        </label>
      </div>

      {#if notLogged.length}
        <p class="note">Not logged, since Domestique logs lifting here: {notLogged.join(', ')}. Record cardio with your watch or bike computer.</p>
      {/if}

      <ol class="exercises">
        {#each exercises as e (e.key)}
          <li class="ex">
            <label class="visually-hidden" for={`lx${e.key}`}>Exercise name</label>
            <input id={`lx${e.key}`} class="text ex-name" bind:value={e.name} maxlength="80" />
            <ul class="sets" aria-label={`Sets of ${e.name}`}>
              {#each e.sets as s, j (s.key)}
                <li class="set">
                  <span class="mono muted set-n">{j + 1}</span>
                  <label class="mini">
                    <span class="visually-hidden">Set {j + 1} reps</span>
                    <input class="text num-in" type="number" inputmode="numeric" min="1" bind:value={s.reps} />
                    <span class="muted">reps</span>
                  </label>
                  <label class="mini">
                    <span class="visually-hidden">Set {j + 1} weight in kg</span>
                    <input class="text num-in" type="number" inputmode="decimal" min="0" step="0.5" bind:value={s.weight} placeholder="BW" />
                    <span class="muted">kg</span>
                  </label>
                  <button class="icon" onclick={() => removeSet(e, s)} aria-label={`Remove set ${j + 1} of ${e.name}`}><X size={18} aria-hidden="true" /></button>
                </li>
              {/each}
            </ul>
          </li>
        {/each}
      </ol>
    </section>
  {/if}
</main>

{#if read}
  <ActionBar>
    <button class="btn btn-primary" onclick={save} disabled={saving || setTotal === 0} aria-busy={saving}>
      {saving ? 'Saving…' : `Save ${setTotal} ${setTotal === 1 ? 'set' : 'sets'}`}
    </button>
  </ActionBar>
{/if}

<style>
  .page {
    max-width: var(--page-list);
    margin: 0 auto;
    padding: calc(env(safe-area-inset-top) + var(--space-2)) var(--gutter) 0;
    display: grid;
    gap: var(--space-3);
    justify-items: start;
  }
  .page > * {
    width: 100%;
  }
  .page > .btn {
    width: auto;
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
  }
  h2 {
    font-size: var(--size-lead);
  }
  .lead {
    max-width: 55ch;
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
  }
  textarea.text {
    padding: var(--space-3);
    resize: vertical;
  }
  .text::placeholder {
    color: var(--text-muted);
  }
  .err {
    color: var(--danger);
    font-size: var(--size-small);
    min-height: 1.3em;
  }
  .err:empty {
    min-height: 0;
  }
  .draft {
    display: grid;
    gap: var(--space-4);
    padding-top: var(--space-4);
    border-top: 1px solid var(--line-strong);
  }
  .fields {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
  }
  .field {
    display: grid;
    gap: var(--space-1);
    flex: 1 1 14rem;
  }
  .field.narrow {
    flex: 0 1 7rem;
  }
  .note {
    font-size: var(--size-small);
    padding: var(--space-3) var(--space-4);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    max-width: 60ch;
  }
  .exercises {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: var(--space-5);
  }
  .ex {
    display: grid;
    gap: var(--space-2);
  }
  .ex-name {
    font-weight: 600;
  }
  .sets {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: var(--space-2);
  }
  .set {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }
  .set-n {
    width: 1.5rem;
    text-align: right;
  }
  .mini {
    display: flex;
    align-items: center;
    gap: var(--space-1);
  }
  .num-in {
    width: 5rem;
    text-align: right;
  }
  .icon {
    display: inline-grid;
    place-items: center;
    min-width: var(--tap-min);
    min-height: var(--tap-min);
    margin-left: auto;
    border: 0;
    background: none;
    color: var(--text-muted);
    border-radius: var(--radius-m);
    cursor: pointer;
  }
  .icon:hover {
    color: var(--text);
  }
</style>
