<script lang="ts">
  // The check-in after a workout: a few questions, one tap each. Every tap
  // saves; Skip records a pass so nothing asks again. Answered, it folds to
  // one line with Change.
  import { saveCheckIn } from '../lib/data';
  import { BODY_PARTS, answered, feltText, questionTexts } from '../lib/checkin';
  import type { CheckIn, Question } from '../lib/types';

  let {
    path,
    questions,
    initial = null,
    session = false,
    heading = 'How did it go?',
    sub = '',
    srpe = 0,
    ondone,
  }: {
    path: string;
    questions: Question[];
    initial?: CheckIn | null;
    session?: boolean;
    heading?: string;
    sub?: string;
    srpe?: number;
    ondone?: () => void;
  } = $props();

  // Copied once: the card owns its answers from here on.
  // svelte-ignore state_referenced_locally
  let c = $state<CheckIn>({ ...(initial ?? {}), where: [...(initial?.where ?? [])] });
  // svelte-ignore state_referenced_locally
  let editing = $state(!answered(initial) && !initial?.skipped);
  // svelte-ignore state_referenced_locally
  let load = $state(srpe);
  let status = $state<'idle' | 'saving' | 'saved' | 'error'>('idle');
  // svelte-ignore state_referenced_locally
  let notesOpen = $state(!!initial?.notes);
  let timer: ReturnType<typeof setTimeout> | undefined;

  let texts = $derived(questionTexts(questions, session));
  let hurting = $derived(c.pain === 'niggle' || c.pain === 'pain');
  let any = $derived(answered(c));

  async function save(body: CheckIn): Promise<boolean> {
    clearTimeout(timer);
    status = 'saving';
    try {
      const r = await saveCheckIn(path, body);
      load = r.srpe ?? 0;
      status = 'saved';
      return true;
    } catch {
      status = 'error';
      return false;
    }
  }

  function soon() {
    clearTimeout(timer);
    status = 'saving';
    timer = setTimeout(() => void save(answers()), 400);
  }

  const answers = (): CheckIn => {
    const s = $state.snapshot(c) as CheckIn;
    return { effort: s.effort, legs: s.legs, plan: s.plan, pain: s.pain, where: s.where, fuel: s.fuel, notes: s.notes };
  };

  function pick(k: Question, v: string) {
    c[k] = c[k] === v ? '' : v; // a second tap clears it
    if (k === 'pain' && !(c.pain === 'niggle' || c.pain === 'pain')) c.where = [];
    soon();
  }

  function part(v: string) {
    const w = c.where ?? [];
    c.where = w.includes(v) ? w.filter((x) => x !== v) : w.length < 4 ? [...w, v] : w;
    soon();
  }

  async function done() {
    if (await save(answers())) {
      editing = false;
      ondone?.();
    }
  }

  async function skip() {
    if (await save({ skipped: true })) {
      c.skipped = true;
      editing = false;
      ondone?.();
    }
  }
</script>

<section class="checkin" aria-labelledby="ci-h">
  {#if editing}
    <div>
      <h2 id="ci-h" class="ci-h">{heading}</h2>
      {#if sub}<p class="small muted">{sub}</p>{/if}
    </div>
    {#each texts as q (q.k)}
      <fieldset class="q">
        <legend class="label">{q.label}</legend>
        <div class="chips">
          {#each q.answers as a (a.v)}
            <button type="button" class="chip" aria-pressed={c[q.k] === a.v} onclick={() => pick(q.k, a.v)}>{a.label}</button>
          {/each}
        </div>
        {#if q.k === 'pain' && hurting}
          <div class="chips parts" role="group" aria-label="Where">
            {#each BODY_PARTS as p (p.v)}
              <button type="button" class="chip small-chip" aria-pressed={c.where?.includes(p.v) ?? false} onclick={() => part(p.v)}>{p.label}</button>
            {/each}
          </div>
        {/if}
      </fieldset>
    {/each}
    {#if notesOpen}
      <label class="q">
        <span class="label">Notes</span>
        <textarea bind:value={c.notes} oninput={soon} rows="2" maxlength="500" placeholder="Anything else"></textarea>
      </label>
    {:else}
      <button type="button" class="link" onclick={() => (notesOpen = true)}>Add a note</button>
    {/if}
    <div class="ci-actions">
      {#if any}
        <button type="button" class="btn" onclick={done}>Done</button>
      {:else}
        <button type="button" class="btn btn-quiet" onclick={skip}>Skip</button>
      {/if}
      <span class="small status" role="status">
        {#if status === 'saving'}Saving…{:else if status === 'saved'}Saved{:else if status === 'error'}Couldn't save. Check the connection.{/if}
      </span>
    </div>
  {:else if answered(c)}
    <div class="folded">
      <p><span class="label">How it felt</span> {feltText(c, session)}{#if load > 0}<span class="mono srpe"> · {load} sRPE</span>{/if}</p>
      <button type="button" class="link" onclick={() => (editing = true)}>Change</button>
    </div>
    {#if c.notes}<p class="small muted notes">{c.notes}</p>{/if}
  {:else}
    <div class="folded">
      <p class="muted small">No check-in.</p>
      <button type="button" class="link" onclick={() => (editing = true)}>Say how it went</button>
    </div>
  {/if}
</section>

<style>
  .checkin {
    margin-top: var(--space-4);
    padding: var(--space-4);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    display: grid;
    gap: var(--space-3);
  }
  .ci-h {
    font-size: var(--size-body);
  }
  .q {
    display: grid;
    gap: var(--space-1);
    margin: 0;
    padding: 0;
    border: 0;
    min-width: 0;
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1);
  }
  .chip {
    flex: 1 1 4.5rem;
    min-height: var(--tap-min);
    padding: 0 var(--space-2);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    background: var(--bg);
    color: var(--text);
    font: inherit;
    font-weight: 600;
    cursor: pointer;
    transition: background-color var(--dur-state) var(--ease-out);
  }
  .chip[aria-pressed='true'] {
    background: var(--text);
    color: var(--bg);
    border-color: var(--text);
  }
  .parts {
    margin-top: var(--space-1);
  }
  .small-chip {
    flex: 0 1 auto;
    min-height: 2.75rem;
    font-weight: 500;
    font-size: var(--size-small);
  }
  textarea {
    width: 100%;
    padding: var(--space-2) var(--space-3);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    background: var(--bg);
    color: var(--text);
    font: inherit;
    resize: vertical;
  }
  .link {
    justify-self: start;
    min-height: 2.75rem;
    padding: 0;
    border: 0;
    background: none;
    color: var(--text-muted);
    font: inherit;
    font-size: var(--size-small);
    font-weight: 600;
    text-decoration: underline;
    cursor: pointer;
  }
  .ci-actions {
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }
  .ci-actions .btn {
    min-height: var(--tap-min);
    min-width: 7rem;
  }
  .status {
    color: var(--text-muted);
  }
  .folded {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
  }
  .folded .label {
    margin-right: var(--space-2);
  }
  .srpe {
    font-size: var(--size-small);
    color: var(--text-muted);
  }
  .small {
    font-size: var(--size-small);
  }
  .notes {
    white-space: pre-line;
  }
</style>
