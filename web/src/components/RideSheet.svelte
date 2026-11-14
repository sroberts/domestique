<script lang="ts">
  // Plan a ride for a day, or change or remove one: a sheet from the bottom.
  import { localDate } from '../lib/api';
  import { clearRidePlan, saveRidePlan } from '../lib/data';
  import { day } from '../lib/format';
  import { INTENSITIES, addDays, dateMs, formToPlan, type RideForm } from '../lib/rides';
  import type { RidePlan } from '../lib/types';

  let { planned = [], onsaved }: { planned?: RidePlan[]; onsaved: (msg: string) => void } = $props();

  let dialog = $state<HTMLDialogElement>();
  let form = $state<RideForm | null>(null);
  let editing = $state<RidePlan | null>(null);
  let saving = $state(false);
  let msg = $state('');
  const today = localDate();

  /** Opens the sheet: on a plan to change it, else blank for date. */
  export function open(p?: RidePlan, date?: string) {
    editing = p ?? null;
    msg = '';
    form = p
      ? { date: p.date, name: p.name, kind: p.kind, minutes: String(p.duration_min), intensity: p.intensity, tss: p.tss ? String(p.tss) : '', notes: p.notes }
      : { date: date ?? today, name: '', kind: 'indoor', minutes: '60', intensity: 'endurance', tss: '', notes: '' };
    dialog?.showModal();
  }

  // Another ride already on the chosen day, which saving replaces.
  let clash = $derived(form && planned.find((p) => p.date === form!.date && p.date !== editing?.date));

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!form || saving) return;
    const p = formToPlan($state.snapshot(form) as RideForm, today);
    if (typeof p === 'string') {
      msg = p;
      return;
    }
    saving = true;
    msg = '';
    try {
      await saveRidePlan(p);
      if (editing && editing.date !== p.date) await clearRidePlan(editing.date);
      dialog?.close();
      onsaved(`${p.name} is planned for ${p.date === today ? 'today' : day(dateMs(p.date))}.`);
    } catch {
      msg = "Couldn't save. Check the connection and try again.";
    } finally {
      saving = false;
    }
  }

  async function remove() {
    if (!editing || saving) return;
    saving = true;
    try {
      await clearRidePlan(editing.date);
      dialog?.close();
      onsaved(`${editing.name} is off the plan.`);
    } catch {
      msg = "Couldn't remove it. Check the connection and try again.";
    } finally {
      saving = false;
    }
  }
</script>

<dialog bind:this={dialog} class="sheet" aria-labelledby="ride-sheet-h" onclick={(e) => e.target === dialog && dialog?.close()} onclose={() => (form = null)}>
  {#if form}
    <form class="sheet-inner" onsubmit={submit} novalidate>
      <h2 id="ride-sheet-h">{editing ? 'Change the ride' : 'Plan a ride'}</h2>
      <label class="f">
        <span class="label">Day</span>
        <input type="date" bind:value={form.date} min={today} max={addDays(today, 365)} required />
        {#if clash}<span class="help">Replaces {clash.name}, planned that day.</span>{/if}
      </label>
      <label class="f">
        <span class="label">Ride</span>
        <input bind:value={form.name} required maxlength="80" autocomplete="off" placeholder="Zwift: Jungle Circuit, or Endurance, rolling" />
      </label>
      <fieldset class="f">
        <legend class="label">Where</legend>
        <div class="seg" role="radiogroup">
          <label class:on={form.kind === 'indoor'}><input type="radio" bind:group={form.kind} value="indoor" /> Indoor</label>
          <label class:on={form.kind === 'outdoor'}><input type="radio" bind:group={form.kind} value="outdoor" /> Outdoor</label>
        </div>
      </fieldset>
      <div class="two">
        <label class="f">
          <span class="label">Length</span>
          <span class="field"><input class="mono" inputmode="numeric" bind:value={form.minutes} required /><span class="unit">min</span></span>
        </label>
        <label class="f">
          <span class="label">Target <span class="opt">optional</span></span>
          <span class="field"><input class="mono" inputmode="numeric" bind:value={form.tss} placeholder="none" /><span class="unit">TSS</span></span>
        </label>
      </div>
      <label class="f">
        <span class="label">How hard</span>
        <select bind:value={form.intensity}>
          {#each INTENSITIES as i (i.k)}<option value={i.k}>{i.label}: {i.hint}</option>{/each}
          <option value="">Not set</option>
        </select>
      </label>
      <label class="f">
        <span class="label">Notes <span class="opt">optional</span></span>
        <textarea bind:value={form.notes} rows="3" maxlength="1000" placeholder="The intervals, the route, what to eat"></textarea>
      </label>

      {#if msg}<p class="small err" role="status">{msg}</p>{/if}
      <div class="sheet-actions">
        <button class="btn btn-primary sheet-btn" type="submit" disabled={saving}>{saving ? 'Saving…' : editing ? 'Save' : 'Plan it'}</button>
        <button class="btn btn-quiet sheet-btn" type="button" onclick={() => dialog?.close()}>Cancel</button>
        {#if editing}
          <button class="btn btn-quiet sheet-btn danger" type="button" onclick={remove} disabled={saving}>Remove from the plan</button>
        {/if}
      </div>
    </form>
  {/if}
</dialog>

<style>
  .sheet {
    margin: auto auto 0;
    width: 100%;
    max-width: 35rem;
    max-height: calc(100dvh - var(--space-6));
    padding: 0;
    border: 1px solid var(--line-strong);
    border-bottom: 0;
    border-radius: var(--radius-m) var(--radius-m) 0 0;
    background: var(--surface);
    color: var(--text);
    overflow-y: auto;
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
  @media (min-width: 900px) {
    .sheet {
      margin: auto;
      border-bottom: 1px solid var(--line-strong);
      border-radius: var(--radius-m);
    }
  }
  .sheet-inner {
    display: grid;
    gap: var(--space-4);
    padding: var(--space-5) var(--gutter) calc(var(--space-5) + env(safe-area-inset-bottom));
  }
  .sheet-inner h2 {
    font-size: var(--size-heading);
  }
  .f {
    display: grid;
    gap: var(--space-1);
    min-width: 0;
    margin: 0;
    padding: 0;
    border: 0;
  }
  .opt {
    text-transform: none;
    letter-spacing: 0;
    font-weight: 400;
  }
  .two {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-3);
  }
  .f input:not([type='radio']),
  .f select,
  .f textarea {
    width: 100%;
    min-height: 2.75rem;
    padding: 0 var(--space-3);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    background: var(--bg);
    color: var(--text);
    font: inherit;
  }
  .f textarea {
    padding: var(--space-2) var(--space-3);
    resize: vertical;
  }
  .f input::placeholder,
  .f textarea::placeholder {
    color: var(--text-muted);
  }
  .field {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }
  .field input {
    min-width: 0;
  }
  .unit {
    font-size: var(--size-label);
    color: var(--text-muted);
  }
  .help {
    font-size: var(--size-label);
    color: var(--text-muted);
  }
  .seg {
    display: grid;
    grid-template-columns: 1fr 1fr;
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    overflow: hidden;
  }
  .seg label {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 2.75rem;
    font-weight: 600;
    cursor: pointer;
  }
  .seg label + label {
    border-left: 1px solid var(--line-strong);
  }
  .seg label.on {
    background: var(--text);
    color: var(--bg);
  }
  .seg label:has(:focus-visible) {
    outline: 2px solid var(--signal);
    outline-offset: -2px;
  }
  .seg input {
    position: absolute;
    opacity: 0;
    pointer-events: none;
  }
  .small {
    font-size: var(--size-small);
  }
  .err {
    color: var(--danger);
  }
  .sheet-actions {
    display: grid;
    gap: var(--space-2);
  }
  .sheet-btn {
    min-height: var(--tap-min);
    width: 100%;
  }
  .danger {
    color: var(--danger);
  }
</style>
