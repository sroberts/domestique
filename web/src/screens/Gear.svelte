<script lang="ts">
  import Plus from '@lucide/svelte/icons/plus';
  import { AuthError } from '../lib/api';
  import { localDate } from '../lib/api';
  import { day, thousands, time } from '../lib/format';
  import {
    KIND_LABEL,
    KIND_LIFE_KM,
    dateToMs,
    fetchGear,
    kmWhole,
    msToDate,
    newID,
    removeBike,
    removeComponent,
    removeSensor,
    saveBike,
    saveComponent,
    saveSensor,
    type BikeView,
    type ComponentKind,
    type ComponentView,
    type GearStatus,
    type GearView,
    type SensorView,
  } from '../lib/gear';

  let g = $state<GearView | null>(null);
  let stale = $state<number | null>(null);
  let failed = $state(false);

  async function load() {
    const r = await fetchGear();
    g = r.data;
    stale = r.stale;
    failed = !r.data;
  }
  $effect(() => {
    void load();
  });

  const rank: Record<GearStatus, number> = { ok: 0, soon: 1, due: 2 };
  let empty = $derived(!!g && g.bikes.length === 0 && g.sensors.length === 0);
  let activeBikes = $derived(g?.bikes.filter((b) => !b.retired) ?? []);
  let retiredBikes = $derived(g?.bikes.filter((b) => b.retired) ?? []);
  let sensorWorst = $derived(Math.max(0, ...(g?.sensors.filter((s) => !s.retired).map((s) => rank[s.status]) ?? [])));
  // Due and soon rise to the top: sensors go first when they're the most urgent.
  let sensorsFirst = $derived(sensorWorst > 0 && sensorWorst > (activeBikes[0] ? rank[activeBikes[0].status] : 0));

  const today = localDate();
  /** A date input's value as a time: now for today, else that day's start. */
  const when = (d: string) => (d === today ? Date.now() : dateToMs(d));
  const kmNum = (s: string) => Math.max(0, Number(String(s).replace(/[^\d.]/g, '')) || 0);

  function partStatus(c: ComponentView): string {
    if (!c.replace_at_m) return 'No replace-at distance set';
    const left = c.replace_at_m - c.distance_m;
    if (c.status === 'due') return left < 0 ? `Due: ${kmWhole(-left)} km over` : 'Due now';
    if (c.status === 'soon') return `Soon: ${kmWhole(left)} km left`;
    return `${kmWhole(left)} km left`;
  }
  function sensorStatus(s: SensorView): string {
    const left = s.battery_life_h - s.used_s / 3600;
    if (s.status === 'due') return left < 0 ? `Due: ${thousands(-left)} h over` : 'Due now';
    if (s.status === 'soon') return `Soon: ${thousands(left)} h left`;
    return `${thousands(left)} h left`;
  }
  const hours = (s: number) => thousands(s / 3600);
  function defaults(b: BikeView): string {
    if (b.default_ride && b.default_virtual) return 'Default for all rides';
    if (b.default_ride) return 'Default for outdoor rides';
    if (b.default_virtual) return 'Default for indoor rides';
    return '';
  }

  // ---- Sheets -----------------------------------------------------------------
  type Edit =
    | { type: 'bike'; isNew: boolean; id: string; name: string; aliases: string; defRide: boolean; defVirtual: boolean; startKm: string; retired: boolean }
    | { type: 'part'; isNew: boolean; id: string; bikeId: string; bikeName: string; kind: ComponentKind; name: string; installed: string; replaceKm: string; startKm: string; removedAt: number | null; replacedBy: string | null }
    | { type: 'replace'; old: ComponentView; bikeName: string; date: string; name: string; replaceKm: string }
    | { type: 'sensor'; isNew: boolean; id: string; name: string; match: string; batteryDate: string; lifeH: string; retired: boolean };

  let dialog = $state<HTMLDialogElement | null>(null);
  let edit = $state<Edit | null>(null);
  let saving = $state(false);
  let msg = $state('');

  function open(e: Edit) {
    edit = e;
    msg = '';
    if (!dialog?.open) dialog?.showModal();
  }
  function close() {
    dialog?.close();
  }

  function addBike(name = '') {
    const first = !g?.bikes.some((b) => !b.retired);
    open({ type: 'bike', isNew: true, id: newID('b_'), name, aliases: '', defRide: first, defVirtual: first, startKm: '', retired: false });
  }
  function editBike(b: BikeView) {
    open({ type: 'bike', isNew: false, id: b.id, name: b.name, aliases: b.aliases.join(', '), defRide: b.default_ride,
      defVirtual: b.default_virtual, startKm: b.start_distance_m ? String(Math.round(b.start_distance_m / 1000)) : '', retired: b.retired });
  }
  function addPart(b: BikeView, kind: ComponentKind = 'chain') {
    open({ type: 'part', isNew: true, id: newID('c_'), bikeId: b.id, bikeName: b.name, kind, name: '', installed: today,
      replaceKm: String(KIND_LIFE_KM[kind] || ''), startKm: '', removedAt: null, replacedBy: null });
  }
  function editPart(b: BikeView, c: ComponentView) {
    open({ type: 'part', isNew: false, id: c.id, bikeId: b.id, bikeName: b.name, kind: c.kind, name: c.name, installed: msToDate(c.installed_at),
      replaceKm: c.replace_at_m ? String(Math.round(c.replace_at_m / 1000)) : '', startKm: c.start_distance_m ? String(Math.round(c.start_distance_m / 1000)) : '',
      removedAt: c.removed_at, replacedBy: c.replaced_by });
  }
  function replacePart(b: BikeView, c: ComponentView) {
    open({ type: 'replace', old: c, bikeName: b.name, date: today, name: '', replaceKm: c.replace_at_m ? String(Math.round(c.replace_at_m / 1000)) : String(KIND_LIFE_KM[c.kind] || '') });
  }
  function addSensor() {
    open({ type: 'sensor', isNew: true, id: newID('s_'), name: 'Chest strap', match: '', batteryDate: today, lifeH: '400', retired: false });
  }
  function editSensor(s: SensorView) {
    open({ type: 'sensor', isNew: false, id: s.id, name: s.name, match: s.device_match, batteryDate: msToDate(s.battery_at), lifeH: String(s.battery_life_h), retired: s.retired });
  }

  async function run(fn: () => Promise<void>) {
    saving = true;
    msg = '';
    try {
      await fn();
      close();
      await load();
    } catch (err) {
      msg = err instanceof AuthError ? 'Sign in again to save.' : `Couldn't save: ${(err as Error).message}`;
    } finally {
      saving = false;
    }
  }

  function submit(e: SubmitEvent) {
    e.preventDefault();
    const x = edit;
    if (!x) return;
    if (x.type === 'bike') {
      void run(() =>
        saveBike({
          id: x.id, name: x.name.trim(), aliases: x.aliases.split(',').map((s) => s.trim()).filter(Boolean),
          default_ride: x.defRide && !x.retired, default_virtual: x.defVirtual && !x.retired,
          start_distance_m: kmNum(x.startKm) * 1000, retired: x.retired,
        }),
      );
    } else if (x.type === 'part') {
      void run(() =>
        saveComponent({
          id: x.id, bike_id: x.bikeId, kind: x.kind, name: x.name.trim(), installed_at: when(x.installed),
          removed_at: x.removedAt, replace_at_m: kmNum(x.replaceKm) * 1000, start_distance_m: kmNum(x.startKm) * 1000, replaced_by: x.replacedBy,
        }),
      );
    } else if (x.type === 'replace') {
      const o = x.old;
      void run(() =>
        saveComponent(
          { id: newID('c_'), bike_id: o.bike_id, kind: o.kind, name: x.name.trim(), installed_at: when(x.date), removed_at: null,
            replace_at_m: kmNum(x.replaceKm) * 1000, start_distance_m: 0, replaced_by: null },
          o.id,
        ),
      );
    } else {
      void run(() =>
        saveSensor({
          id: x.id, name: x.name.trim(), kind: 'hr_strap', device_match: x.match.trim(), battery_at: when(x.batteryDate),
          battery_life_h: Math.max(1, kmNum(x.lifeH)), retired: x.retired,
        }),
      );
    }
  }

  function newBattery(s: SensorView) {
    void run(() => saveSensor({ ...s, battery_at: Date.now() }));
  }

  function removePartNow() {
    const x = edit;
    if (x?.type !== 'part') return;
    void run(() =>
      saveComponent({ id: x.id, bike_id: x.bikeId, kind: x.kind, name: x.name.trim(), installed_at: when(x.installed),
        removed_at: Date.now(), replace_at_m: kmNum(x.replaceKm) * 1000, start_distance_m: kmNum(x.startKm) * 1000, replaced_by: x.replacedBy }),
    );
  }

  function destroy() {
    const x = edit;
    if (!x || x.type === 'replace') return;
    const what = x.type === 'bike' ? `${x.name || 'this bike'} and its parts` : x.type === 'part' ? 'this part' : x.name || 'this sensor';
    const note = x.type === 'bike' ? ' Retiring keeps the history; deleting removes it.' : '';
    if (!confirm(`Delete ${what}?${note}`)) return;
    void run(() => (x.type === 'bike' ? removeBike(x.id) : x.type === 'part' ? removeComponent(x.id) : removeSensor(x.id)));
  }

  const wearScale = (w: number | null) => `scaleX(${Math.min(1, Math.max(0, w ?? 0))})`;
</script>

{#snippet meter(status: GearStatus, wear: number | null, label: string)}
  <span class="meter m-{status}" role="meter" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round((wear ?? 0) * 100)} aria-label={label}>
    <span style:transform={wearScale(wear)}></span>
  </span>
{/snippet}

{#snippet bikeBlock(b: BikeView)}
  <section class="bike" aria-labelledby={`bk-${b.id}`}>
    <div class="bike-head">
      <div>
        <h2 id={`bk-${b.id}`}>{b.name}{#if b.retired}<span class="tag">Retired</span>{/if}</h2>
        <p class="bike-meta">
          <span class="num big">{kmWhole(b.distance_m)}</span><span class="unit">km</span>
          <span class="num big">{hours(b.moving_s)}</span><span class="unit">h</span>
          <span class="muted small">{b.rides} {b.rides === 1 ? 'ride' : 'rides'}{defaults(b) ? ` · ${defaults(b)}` : ''}</span>
        </p>
      </div>
      <button class="btn btn-quiet" onclick={() => editBike(b)} disabled={!!stale}>Edit<span class="visually-hidden"> {b.name}</span></button>
    </div>
    {#if b.components.length}
      <ul class="parts">
        {#each b.components as c (c.id)}
          <li class="part" class:has-act={c.status !== 'ok' && !b.retired}>
            <button class="row" onclick={() => editPart(b, c)} disabled={!!stale}>
              <span class="p-name">{c.label}{#if c.name && c.label !== KIND_LABEL[c.kind]}<span class="muted">{' · '}{KIND_LABEL[c.kind]}</span>{/if}</span>
              <span class="p-dist"><span class="num">{kmWhole(c.distance_m)}</span>{#if c.replace_at_m}<span class="muted">{' of '}{kmWhole(c.replace_at_m)}</span>{/if} km</span>
              {#if c.replace_at_m}{@render meter(c.status, c.wear, `${c.label} wear`)}{/if}
              <span class="p-status s-{c.status}">{partStatus(c)}</span>
            </button>
            {#if c.status !== 'ok' && !b.retired}
              <button class="btn act" onclick={() => replacePart(b, c)} disabled={!!stale}>Replace<span class="visually-hidden"> {c.label}</span></button>
            {/if}
          </li>
        {/each}
      </ul>
    {:else if !b.retired}
      <p class="muted small hint">Add the chain and tyres to see when each is due.</p>
    {/if}
    {#if !b.retired}
      <button class="btn btn-quiet add" onclick={() => addPart(b)} disabled={!!stale}><Plus size={18} aria-hidden="true" /> Add part</button>
    {/if}
    {#if b.history.length}
      <details class="history">
        <summary>Replaced parts ({b.history.length})</summary>
        <ul>
          {#each b.history as c (c.id)}
            <li>
              <span>{c.label}</span>
              <span class="mono muted">{day(c.installed_at)} – {c.removed_at ? day(c.removed_at) : ''} · {kmWhole(c.distance_m)} km</span>
            </li>
          {/each}
        </ul>
      </details>
    {/if}
  </section>
{/snippet}

{#snippet sensorBlock()}
  <section class="bike" aria-labelledby="sn-h">
    <div class="bike-head">
      <h2 id="sn-h">Sensors</h2>
    </div>
    {#if g && g.sensors.length}
      <ul class="parts">
        {#each g.sensors as s (s.id)}
          <li class="part has-act">
            <button class="row" onclick={() => editSensor(s)} disabled={!!stale}>
              <span class="p-name">{s.name}{#if s.retired}<span class="tag">Retired</span>{/if}</span>
              <span class="p-dist"><span class="num">{hours(s.used_s)}</span><span class="muted">{' of '}{thousands(s.battery_life_h)}</span> h</span>
              {@render meter(s.status, s.wear, `${s.name} battery used`)}
              <span class="p-status s-{s.status}">{sensorStatus(s)} · battery since {day(s.battery_at)}</span>
            </button>
            {#if !s.retired}
              <button class="btn act" onclick={() => newBattery(s)} disabled={!!stale || saving}>New battery<span class="visually-hidden"> for {s.name}</span></button>
            {/if}
          </li>
        {/each}
      </ul>
      <p class="muted small hint">Hours count every activity with heart rate from a chest strap or head unit, not Whoop.</p>
    {/if}
    <button class="btn btn-quiet add" onclick={addSensor} disabled={!!stale}><Plus size={18} aria-hidden="true" /> Add sensor</button>
  </section>
{/snippet}

<main class="page">
  <header class="top">
    <h1>Gear</h1>
    {#if g && !empty}<button class="btn btn-quiet" onclick={() => addBike()} disabled={!!stale}><Plus size={18} aria-hidden="true" /> Add bike</button>{/if}
  </header>

  {#if stale}<p class="notice">No connection. Showing gear as of {time(stale)}; changes need the Mac Mini.</p>{/if}

  {#if failed}
    <section class="empty">
      <h2>Gear needs the Mac Mini</h2>
      <p class="muted">There's no connection, and gear hasn't been opened on this device before.</p>
      <button class="btn" onclick={load}>Try again</button>
    </section>
  {:else if g && empty}
    <section class="empty">
      <h2>Add your first bike</h2>
      <p>
        {#if g.rides > 0}Distance fills in from the {thousands(g.rides)} {g.rides === 1 ? 'ride' : 'rides'} already imported.{:else}Distance fills in from your rides as they're imported.{/if}
        Then add the chain and tyres, and each shows when it's due.
      </p>
      <button class="btn btn-primary first" onclick={() => addBike()}>Add a bike</button>
      <p class="muted small">Chest strap? <button class="linkish" onclick={addSensor}>Track its battery</button> by hours of use.</p>
    </section>
  {:else if g}
    {#if sensorsFirst}{@render sensorBlock()}{/if}
    {#each activeBikes as b (b.id)}{@render bikeBlock(b)}{/each}
    {#if g.unmatched_gear.length}
      <div class="unmatched">
        {#each g.unmatched_gear as u (u.name)}
          <p class="small">
            {u.rides} {u.rides === 1 ? 'ride' : 'rides'} ({kmWhole(u.distance_m)} km) from Strava on <strong>“{u.name}”</strong> count towards no bike.
            <button class="linkish" onclick={() => addBike(u.name)} disabled={!!stale}>Add it as a bike</button>, or add the name to a bike's other names.
          </p>
        {/each}
      </div>
    {/if}
    {#if !sensorsFirst}{@render sensorBlock()}{/if}
    {#if retiredBikes.length}
      <details class="retired">
        <summary>Retired bikes ({retiredBikes.length})</summary>
        {#each retiredBikes as b (b.id)}{@render bikeBlock(b)}{/each}
      </details>
    {/if}
  {/if}
</main>

<dialog bind:this={dialog} class="sheet" aria-labelledby="gear-sheet-h" onclick={(e) => e.target === dialog && close()} onclose={() => (edit = null)}>
  {#if edit}
    <form class="sheet-inner" onsubmit={submit}>
      {#if edit.type === 'bike'}
        <h2 id="gear-sheet-h">{edit.isNew ? 'Add a bike' : `Edit ${edit.name}`}</h2>
        <label class="f">
          <span class="label">Name</span>
          <input bind:value={edit.name} required maxlength="60" autocomplete="off" placeholder="Tarmac" />
        </label>
        <label class="f">
          <span class="label">Other names</span>
          <input bind:value={edit.aliases} autocomplete="off" placeholder="Specialized Tarmac SL7" aria-describedby="alias-help" />
          <span id="alias-help" class="help">Strava's name for it, if different. Separate several with commas.</span>
        </label>
        <fieldset class="f">
          <legend class="label">Default bike for</legend>
          <label class="check"><input type="checkbox" bind:checked={edit.defRide} disabled={edit.retired} /> Outdoor rides</label>
          <label class="check"><input type="checkbox" bind:checked={edit.defVirtual} disabled={edit.retired} /> Indoor and virtual rides</label>
          <span class="help">Rides without a bike from Strava go to the default.</span>
        </fieldset>
        <label class="f">
          <span class="label">Distance before Domestique</span>
          <span class="field"><input class="mono" inputmode="decimal" bind:value={edit.startKm} placeholder="0" /><span class="unit">km</span></span>
        </label>
        {#if !edit.isNew}
          <label class="check"><input type="checkbox" bind:checked={edit.retired} /> Retired</label>
        {/if}
      {:else if edit.type === 'part'}
        <h2 id="gear-sheet-h">{edit.isNew ? `Add a part to ${edit.bikeName}` : `${KIND_LABEL[edit.kind]} on ${edit.bikeName}`}</h2>
        <label class="f">
          <span class="label">Part</span>
          <select bind:value={edit.kind} onchange={() => edit?.type === 'part' && edit.isNew && (edit.replaceKm = String(KIND_LIFE_KM[edit.kind] || ''))}>
            {#each Object.entries(KIND_LABEL) as [k, l] (k)}<option value={k}>{l}</option>{/each}
          </select>
        </label>
        <label class="f">
          <span class="label">Name <span class="opt">optional</span></span>
          <input bind:value={edit.name} maxlength="60" autocomplete="off" placeholder={edit.kind === 'tyres' ? 'GP5000 28 mm' : edit.kind === 'chain' ? 'KMC X11' : ''} />
        </label>
        <label class="f">
          <span class="label">Fitted on</span>
          <input type="date" bind:value={edit.installed} max={today} required aria-describedby="fit-help" />
          <span id="fit-help" class="help">Rides from this day on count towards it.</span>
        </label>
        <div class="two">
          <label class="f">
            <span class="label">Replace at</span>
            <span class="field"><input class="mono" inputmode="decimal" bind:value={edit.replaceKm} placeholder="none" /><span class="unit">km</span></span>
          </label>
          <label class="f">
            <span class="label">Already ridden</span>
            <span class="field"><input class="mono" inputmode="decimal" bind:value={edit.startKm} placeholder="0" /><span class="unit">km</span></span>
          </label>
        </div>
      {:else if edit.type === 'replace'}
        <h2 id="gear-sheet-h">Replace {edit.old.label.toLowerCase()} on {edit.bikeName}</h2>
        <p class="small muted">The old one keeps its {kmWhole(edit.old.distance_m)} km in the bike's history.</p>
        <label class="f">
          <span class="label">Replaced on</span>
          <input type="date" bind:value={edit.date} max={today} min={msToDate(edit.old.installed_at)} required />
        </label>
        <label class="f">
          <span class="label">New part's name <span class="opt">optional</span></span>
          <input bind:value={edit.name} maxlength="60" autocomplete="off" placeholder={edit.old.name} />
        </label>
        <label class="f">
          <span class="label">Replace at</span>
          <span class="field"><input class="mono" inputmode="decimal" bind:value={edit.replaceKm} placeholder="none" /><span class="unit">km</span></span>
        </label>
      {:else}
        <h2 id="gear-sheet-h">{edit.isNew ? 'Add a sensor' : `Edit ${edit.name}`}</h2>
        <label class="f">
          <span class="label">Name</span>
          <input bind:value={edit.name} required maxlength="60" autocomplete="off" />
        </label>
        <div class="two">
          <label class="f">
            <span class="label">Battery changed</span>
            <input type="date" bind:value={edit.batteryDate} max={today} required />
          </label>
          <label class="f">
            <span class="label">Battery lasts</span>
            <span class="field"><input class="mono" inputmode="numeric" bind:value={edit.lifeH} required /><span class="unit">h</span></span>
          </label>
        </div>
        <label class="f">
          <span class="label">Only rides recorded by <span class="opt">optional</span></span>
          <input bind:value={edit.match} maxlength="60" autocomplete="off" placeholder="ELEMNT" aria-describedby="match-help" />
          <span id="match-help" class="help">Part of a device name, or wahoo, garmin, zwift. Blank counts every activity with chest-strap heart rate.</span>
        </label>
        {#if !edit.isNew}
          <label class="check"><input type="checkbox" bind:checked={edit.retired} /> Retired</label>
        {/if}
      {/if}

      {#if msg}<p class="small err" role="status">{msg}</p>{/if}
      <div class="sheet-actions">
        <button class="btn sheet-btn save" type="submit" disabled={saving}>
          {saving ? 'Saving…' : edit.type === 'replace' ? `Fit new ${edit.old.label.toLowerCase()}` : 'Save'}
        </button>
        {#if edit.type === 'part' && !edit.isNew && edit.removedAt == null}
          {@const p = edit}
          <button class="btn sheet-btn" type="button" onclick={() => {
            const b = g?.bikes.find((x) => x.id === p.bikeId);
            const c = b?.components.find((x) => x.id === p.id);
            if (b && c) replacePart(b, c);
          }}>Replace with a new one</button>
          <button class="btn sheet-btn" type="button" onclick={removePartNow} disabled={saving}>Take off the bike</button>
        {/if}
        <button class="btn btn-quiet sheet-btn" type="button" onclick={close}>Cancel</button>
        {#if edit.type !== 'replace' && !edit.isNew}
          <button class="btn btn-quiet sheet-btn danger" type="button" onclick={destroy} disabled={saving}>Delete</button>
        {/if}
      </div>
    </form>
  {/if}
</dialog>

<style>
  .page {
    max-width: var(--page-list);
    margin: 0 auto;
    padding: calc(env(safe-area-inset-top) + var(--space-2)) var(--gutter) 0;
  }
  .top {
    min-height: 2.75rem;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
  }
  h1 {
    font-size: var(--size-heading);
  }
  .small {
    font-size: var(--size-small);
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
    gap: var(--space-4);
    justify-items: start;
    margin-top: var(--space-6);
    max-width: 30rem;
  }
  .empty h2 {
    font-size: var(--size-heading);
  }
  .first {
    max-width: 20rem;
  }
  .linkish {
    padding: 0;
    border: 0;
    background: none;
    color: var(--text);
    font: inherit;
    font-weight: 600;
    text-decoration: underline;
    text-underline-offset: 0.2em;
    cursor: pointer;
    /* a text link in a sentence: pad the hit area without moving the text */
    padding-block: 0.75rem;
    margin-block: -0.75rem;
  }

  /* A bike: name and totals, then its parts as rows. */
  .bike {
    margin-top: var(--space-5);
    padding-top: var(--space-4);
    border-top: 1px solid var(--line-strong);
  }
  .bike-head {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: var(--space-3);
  }
  .bike h2 {
    font-size: var(--size-lead);
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }
  .bike-meta {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    column-gap: 0.25rem;
    row-gap: var(--space-1);
    margin-top: var(--space-2);
  }
  .big {
    font-size: var(--size-stat);
  }
  .bike-meta .unit + .num {
    margin-left: var(--space-3);
  }
  .bike-meta .muted {
    flex-basis: 100%;
  }
  .unit {
    font-size: var(--size-label);
    color: var(--text-muted);
  }
  .tag {
    padding: 0 0.375rem;
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-s);
    font-size: var(--size-label);
    font-weight: 600;
    color: var(--text-muted);
  }
  .parts {
    list-style: none;
    margin: var(--space-3) 0 0;
    padding: 0;
    border-top: 1px solid var(--line);
  }
  .part {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    align-items: center;
    column-gap: var(--space-3);
    border-bottom: 1px solid var(--line);
  }
  .part.has-act {
    grid-template-columns: minmax(0, 1fr) auto;
  }
  .row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: baseline;
    gap: var(--space-1) var(--space-3);
    width: 100%;
    min-height: 3.5rem;
    padding: var(--space-3) 0;
    border: 0;
    background: none;
    text-align: left;
    cursor: pointer;
  }
  .row:disabled {
    cursor: default;
  }
  .row:hover .p-name {
    text-decoration: underline;
    text-underline-offset: 0.2em;
  }
  .p-name {
    font-weight: 600;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .p-dist {
    justify-self: end;
    white-space: nowrap;
    font-size: var(--size-small);
  }
  .p-dist .num {
    font-size: 1.375rem;
  }
  .p-status {
    grid-column: 1 / -1;
    font-size: var(--size-small);
    color: var(--text-muted);
  }
  .s-soon {
    color: var(--zone-4);
    font-weight: 600;
  }
  .s-due {
    color: var(--danger);
    font-weight: 600;
  }
  .meter {
    grid-column: 1 / -1;
    display: block;
    height: 4px;
    margin-top: var(--space-1);
    background: var(--line);
    border-radius: 2px;
    overflow: hidden;
  }
  .meter span {
    display: block;
    height: 100%;
    background: var(--text-muted);
    transform-origin: left center;
  }
  .m-soon span {
    background: var(--zone-4);
  }
  .m-due span {
    background: var(--danger);
  }
  .act {
    white-space: nowrap;
  }
  .hint {
    margin-top: var(--space-3);
  }
  .add {
    margin-top: var(--space-2);
    margin-left: calc(var(--space-2) * -1);
  }
  .history,
  .retired {
    margin-top: var(--space-2);
    font-size: var(--size-small);
  }
  .history summary,
  .retired summary {
    display: list-item;
    padding: 0.7rem 0;
    font-weight: 600;
    cursor: pointer;
    color: var(--text-muted);
  }
  .retired {
    margin-top: var(--space-6);
  }
  .history ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .history li {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: var(--space-1) var(--space-3);
    padding: var(--space-2) 0;
    border-top: 1px solid var(--line);
  }
  .history .mono {
    font-size: var(--size-label);
  }
  .unmatched {
    margin-top: var(--space-5);
    display: grid;
    gap: var(--space-3);
    padding: var(--space-3) var(--space-4);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
  }

  /* Sheet: the same bottom sheet as the live session's. */
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
  .f input:not([type='checkbox']),
  .f select {
    width: 100%;
    min-height: 2.75rem;
    padding: 0 var(--space-3);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    background: var(--bg);
    color: var(--text);
    font: inherit;
  }
  .f input::placeholder {
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
  .help {
    font-size: var(--size-label);
    color: var(--text-muted);
  }
  .check {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    min-height: 2.75rem;
  }
  .check input {
    width: 1.25rem;
    height: 1.25rem;
    accent-color: var(--signal);
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
  .save {
    background: var(--signal);
    color: var(--on-signal);
    border: 0;
  }
  .danger {
    color: var(--danger);
  }
</style>
