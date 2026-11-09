<script lang="ts">
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import BikePicker from '../components/BikePicker.svelte';
  import CoachNote from '../components/CoachNote.svelte';
  import CurveChart from '../components/CurveChart.svelte';
  import ExportMenu from '../components/ExportMenu.svelte';
  import RouteMap from '../components/RouteMap.svelte';
  import Sources from '../components/Sources.svelte';
  import StreamPanels from '../components/StreamPanels.svelte';
  import ZoneRows from '../components/ZoneRows.svelte';
  import { HTTPError, postJSON, putJSON } from '../lib/api';
  import { fetchActivity } from '../lib/data';
  import { Recheck } from '../lib/recheck';
  import { activityExports } from '../lib/export';
  import { SOURCE_LABEL, SPORT_LABEL, day, duration, km, thousands, time } from '../lib/format';
  import { backTarget, link, router } from '../lib/router.svelte';
  import type { ActivityDetail, Version } from '../lib/types';

  let { id }: { id: string } = $props();
  const back = backTarget({ href: '/activities', label: 'Activities' });
  let d = $state<ActivityDetail | null>(null);
  let stale = $state<number | null>(null);
  let failed = $state(false);
  let cursor = $state<number | null>(null);
  // The recording shown alone, or '' for everything merged.
  let view = $state('');
  let loading = $state(false);
  // Bumped to fetch again while the note is being written.
  let tick = $state(0);
  const recheck = new Recheck(() => tick++);
  $effect(() => () => recheck.stop());

  $effect(() => {
    const want = view;
    void tick;
    loading = true;
    fetchActivity(id, want || undefined).then((res) => {
      if (want !== view) return; // switched again before this arrived
      loading = false;
      recheck.after((!!res.data?.note?.pending || !!res.data?.name_suggestion?.pending) && !res.stale);
      if (!res.data && d) return; // keep what's on screen; the switch just didn't load
      d = res.data;
      stale = res.stale;
      failed = !res.data;
      cursor = null;
      // A logged strength session has its own, richer screen, unless another
      // app recorded it too and there are versions to compare.
      const sid = res.data?.activity.session_id;
      if (sid && !want && !res.data?.versions) router.go(`/session/${sid}/summary`, { replace: true });
    });
  });

  let keeping = $state(false);
  let keepError = $state('');
  /** Overrule System 1: this was a real workout, so count its load. */
  async function keep() {
    if (!d || keeping) return;
    keeping = true;
    keepError = '';
    try {
      await postJSON(`/v1/activities/${encodeURIComponent(d.activity.id)}/keep`);
      d.stray = false;
    } catch (err) {
      keepError = err instanceof HTTPError ? err.message : 'No connection. Try again when online.';
    } finally {
      keeping = false;
    }
  }

  let restoring = $state(false);
  let restoreError = $state('');
  /** Overrule System 1: the data it left out was fine, so put it back. */
  async function dataOK() {
    if (!d || restoring) return;
    restoring = true;
    restoreError = '';
    try {
      await postJSON(`/v1/activities/${encodeURIComponent(d.activity.id)}/data-ok`);
      tick++; // zones, charts and load change: fetch again
    } catch (err) {
      restoreError = err instanceof HTTPError ? err.message : 'No connection. Try again when online.';
    } finally {
      restoring = false;
    }
  }

  const CHANNEL: Record<string, string> = { hr: 'Heart rate', power: 'Power' };
  /** "Heart rate looked wrong (strap dropouts)", one line per channel. */
  const badLines = $derived(
    Object.entries(d?.bad_data ?? {}).map(([ch, why]) => `${CHANNEL[ch] ?? ch} looked wrong${why ? ` (${why})` : ''}`)
  );

  let naming = $state(false);
  let nameError = $state('');
  /** Take the suggested name (in Domestique only), or hide it for good. */
  async function useName(take: boolean) {
    const s = d?.name_suggestion?.text;
    if (!d || !s || naming) return;
    naming = true;
    nameError = '';
    const path = `/v1/activities/${encodeURIComponent(d.activity.id)}`;
    try {
      if (take) {
        await putJSON(`${path}/name`, { name: s });
        d.activity.name = s;
      } else {
        await postJSON(`${path}/name-suggestion/dismiss`);
      }
      d.name_suggestion = undefined;
    } catch (err) {
      nameError = err instanceof HTTPError ? err.message : 'No connection. Try again when online.';
    } finally {
      naming = false;
    }
  }

  // Names for each recording, told apart when one app recorded twice (Whoop's
  // own record and the copy it posted to Strava, or a ride Whoop split).
  // In time order; at the same minute, bike computers before wrist bands and
  // an app's own record before its file copy.
  const RANK = ['wahoo', 'garmin', 'zwift', 'device', 'strava', 'upload', 'watch', 'app', 'whoop'];
  let versions = $derived(
    [...(d?.versions ?? [])].sort(
      (x, y) =>
        Math.floor(x.start_at / 60000) - Math.floor(y.start_at / 60000) ||
        RANK.indexOf(x.source) - RANK.indexOf(y.source) ||
        Number(x.file) - Number(y.file),
    ),
  );
  let versionNames = $derived.by(() => {
    const vs = versions;
    const base = vs.map((v) => SOURCE_LABEL[v.source] ?? v.source);
    const named = vs.map((v, i) => (base.filter((b) => b === base[i]).length > 1 && v.file ? `${base[i]} file` : base[i]!));
    return Object.fromEntries(
      vs.map((v, i) => [v.id, named.filter((n) => n === named[i]).length > 1 ? `${named[i]} ${time(v.start_at)}` : named[i]!]),
    );
  });
  let viewName = $derived(view ? versionNames[view] ?? '' : '');

  const METHOD: Record<string, string> = { power: 'power', hr: 'HR', strain: 'strain', none: '' };
  // The comparison: one row per measure any recording has, '–' where one
  // didn't measure it.
  const versionRows: { label: string; value: (v: Version) => string }[] = [
    { label: 'Started', value: (v) => time(v.start_at) },
    { label: 'Duration', value: (v) => duration(v.moving_s || v.elapsed_s) },
    { label: 'Distance', value: (v) => (v.distance_m > 0 ? `${km(v.distance_m)} km` : '') },
    { label: 'Avg power', value: (v) => (v.avg_power > 0 ? `${v.avg_power} W` : '') },
    { label: 'NP', value: (v) => (v.np > 0 ? `${v.np} W` : '') },
    { label: 'Avg HR', value: (v) => (v.avg_hr > 0 ? `${v.avg_hr}` : '') },
    { label: 'Max HR', value: (v) => (v.max_hr > 0 ? `${v.max_hr}` : '') },
    { label: 'Cadence', value: (v) => (v.avg_cadence > 0 ? `${v.avg_cadence} rpm` : '') },
    { label: 'Strain', value: (v) => (v.strain != null ? v.strain.toFixed(1) : '') },
    { label: 'Sets', value: (v) => (v.sets ? `${v.sets} · ${thousands(v.volume_kg ?? 0)} kg` : '') },
    { label: 'TSS', value: (v) => (v.tss > 0 ? `${Math.round(v.tss)} · ${METHOD[v.load_method]}` : '') },
  ];
  let shownRows = $derived(versionRows.filter((r) => versions.some((v) => r.value(v) !== '')));

  let a = $derived(d?.activity);
  let m = $derived(d?.metrics);
  let isRide = $derived(a?.sport === 'ride' || a?.sport === 'virtual_ride');
  let kind = $derived(isRide ? 'ride' : a?.sport === 'strength' ? 'strength' : 'other');
  let hrZoneBounds = $derived(d ? [0.5, 0.6, 0.7, 0.8, 0.9].map((p) => Math.round(p * d!.max_hr)) : undefined);
  let cursorLatLng = $derived.by((): [number, number] | null => {
    if (cursor == null || !d?.stream.route?.length || !d.stream.t?.length) return null;
    const frac = cursor / Math.max(1, (d.stream.t?.length ?? 0) - 1);
    return d.stream.route[Math.round(frac * (d.stream.route.length - 1))] ?? null;
  });

  const powerZoneNames = ['Recovery', 'Endurance', 'Tempo', 'Threshold', 'VO2 max', 'Anaerobic', 'Sprint'];
  // Seven power zones on five zone hues: the top three step through red so
  // neighbours never share a colour (the label carries identity anyway).
  const powerZoneShades = [
    'var(--zone-1)', 'var(--zone-2)', 'var(--zone-3)', 'var(--zone-4)', 'var(--zone-5)',
    'color-mix(in srgb, var(--zone-5) 70%, var(--text))', 'color-mix(in srgb, var(--zone-5) 45%, var(--text))',
  ];

  let channelNote = $derived.by(() => {
    if (!d) return [];
    const by: Record<string, string[]> = {};
    const names: Record<string, string> = { power: 'power', hr: 'heart rate', cadence: 'cadence', gps: 'GPS' };
    for (const [ch, src] of Object.entries(d.channels)) (by[src] ??= []).push(names[ch] ?? ch);
    return Object.entries(by).map(([src, chs]) => `${chs.join(', ')} from ${SOURCE_LABEL[src] ?? src}`);
  });

  function loadNote(): string {
    if (!a) return '';
    switch (a.load_method) {
      case 'power':
        return `Scored by power against an FTP of ${a.ftp} W on the day (IF ${a.intensity.toFixed(2)}).`;
      case 'hr':
        return d?.hr_zones_from === 'whoop'
          ? "Scored by heart rate, from Whoop's time in each zone: no power meter on this one."
          : 'Scored by heart rate: no power meter on this one.';
      case 'strain':
        return `Scored from Whoop strain ${a.strain?.toFixed(1)}.`;
    }
    return 'No power or heart rate, so no load score.';
  }
</script>

<main class="page">
  <header class="top">
    <a class="btn btn-quiet back" href={back.href} use:link><ChevronLeft size={20} aria-hidden="true" /> {back.label}</a>
    {#if d && !stale && !d.view}<ExportMenu items={activityExports(d)} />{/if}
  </header>

  {#if failed && !d}
    <section class="gone">
      <h1>Activity not available</h1>
      <p class="muted">It isn't on the Mac Mini, or there's no connection and it hasn't been opened on this device before.</p>
    </section>
  {:else if d && a && m}
    <h1 class="name">{a.name}</h1>
    <p class="mono meta">
      <span class="sport-tag sport-{kind}">{SPORT_LABEL[a.sport] ?? a.sport}</span>
      {day(a.start_at)} · {time(a.start_at)}{#if a.route} · {a.route}{/if}
    </p>
    {#if d.name_suggestion?.text && !d.view}
      <div class="rename" role="group" aria-label="Suggested name">
        <p>Call it <strong>{d.name_suggestion.text}</strong>?</p>
        <div class="rename-actions">
          <button type="button" class="btn" onclick={() => useName(true)} disabled={naming}>Use this name</button>
          <button type="button" class="btn btn-quiet" onclick={() => useName(false)} disabled={naming}>Dismiss</button>
        </div>
        {#if nameError}<p class="small">{nameError}</p>{/if}
      </div>
    {/if}
    {#if d.versions}
      <div class="versions-switch" role="group" aria-label="Show this workout as" aria-busy={loading}>
        <button type="button" class="vbtn" aria-pressed={!view} onclick={() => (view = '')}>Combined</button>
        {#each versions as v (v.id)}
          <button type="button" class="vbtn" aria-pressed={view === v.id} onclick={() => (view = v.id)}>{versionNames[v.id]}</button>
        {/each}
      </div>
      {#if d.view}
        <p class="note view-note">{viewName}'s own recording, before merging with the others.</p>
      {/if}
    {:else}
      <div class="srcs"><Sources sources={d.sources} /></div>
    {/if}
    {#if stale}<p class="notice">No connection. Showing this activity as last opened at {time(stale)}.</p>{/if}

    {#if d.stream.route && d.stream.route.length > 1}
      <div class="map-wrap"><RouteMap route={d.stream.route} cursor={cursorLatLng} world={d.stream.world} tone={`--sport-${kind}`} /></div>
    {/if}

    <dl class="stats">
      {#if a.distance_m > 0}
        <div><dt class="label">Distance</dt><dd><span class="num">{km(a.distance_m)}</span><span class="unit">km</span></dd></div>
      {/if}
      <div><dt class="label">Moving</dt><dd><span class="num">{duration(a.moving_s || a.elapsed_s)}</span></dd></div>
      {#if m.ascent_m > 0 && a.sport !== 'virtual_ride'}
        <div><dt class="label">Climbing</dt><dd><span class="num">{thousands(m.ascent_m)}</span><span class="unit">m</span></dd></div>
      {/if}
      {#if m.np > 0}
        <div><dt class="label">Norm. power</dt><dd><span class="num">{m.np}</span><span class="unit">W</span></dd></div>
        <div><dt class="label">Avg power</dt><dd><span class="num">{m.avg_power}</span><span class="unit">W</span></dd></div>
      {/if}
      {#if a.tss > 0}
        <div><dt class="label">TSS</dt><dd><span class="num">{Math.round(a.tss)}</span>{#if a.intensity}<span class="unit">IF {a.intensity.toFixed(2)}</span>{/if}</dd></div>
      {/if}
      {#if m.avg_hr > 0}
        <div><dt class="label">Avg HR</dt><dd><span class="num">{m.avg_hr}</span><span class="unit">max {m.max_hr}</span></dd></div>
      {/if}
      {#if m.kj > 0}
        <div><dt class="label">Work</dt><dd><span class="num">{thousands(m.kj)}</span><span class="unit">kJ</span></dd></div>
      {/if}
      {#if m.avg_cadence > 0}
        <div><dt class="label">Cadence</dt><dd><span class="num">{m.avg_cadence}</span><span class="unit">rpm</span></dd></div>
      {/if}
      {#if a.strain != null}
        <div><dt class="label">Strain</dt><dd><span class="num">{a.strain.toFixed(1)}</span></dd></div>
      {/if}
    </dl>
    {#if d.stray}
      <div class="notice stray" role="status">
        <p>This looks like a stray entry rather than a workout, so it isn't counted in training load.</p>
        <button class="btn" onclick={keep} disabled={keeping}>It was real: count it</button>
        {#if keepError}<p class="small">{keepError}</p>{/if}
      </div>
    {:else}
      <p class="note">{#if d.ride_type}<span class="tag ride-type" title="What kind of ride, by System 1">{d.ride_type}</span> {/if}{loadNote()}</p>
    {/if}
    {#if badLines.length && !d.view}
      <div class="notice stray" role="status">
        <p>{badLines.join('. ')}, so it's left out of load and zones.</p>
        <button class="btn" onclick={dataOK} disabled={restoring}>It's fine: use it</button>
        {#if restoreError}<p class="small">{restoreError}</p>{/if}
      </div>
    {/if}
    {#if d.note?.text}<CoachNote text={d.note.text} />{/if}
    {#if d.tags?.length || d.tag_note}
      <p class="tags" aria-label="From your description">
        {#each d.tags ?? [] as t (t)}<span class="tag">{t}</span>{/each}
        {#if d.tag_note}<span class="tag-note">{d.tag_note}</span>{/if}
      </p>
    {/if}
    {#if isRide && !stale && !d.view}<BikePicker activityId={a.id} />{/if}

    {#if d.strength && a.session_id}
      <section class="block" aria-labelledby="lift-h">
        <h2 id="lift-h" class="block-h">Lifting</h2>
        <p class="lift">
          <span class="mono">{d.strength.sets.length} sets · {d.strength.total_reps} reps · {thousands(d.strength.volume_kg)} kg</span>
          <a class="btn" href={`/session/${a.session_id}/summary`} use:link>Open the session</a>
        </p>
      </section>
    {/if}

    {#if (d.stream.t?.length ?? 0) > 1}
      <section class="block" aria-labelledby="streams-h">
        <h2 id="streams-h" class="block-h">Over the ride</h2>
        <StreamPanels stream={d.stream} hrZones={hrZoneBounds} {cursor} oncursor={(i) => (cursor = i)} />
      </section>
    {/if}

    {#if isRide && m.power_curve?.some((v) => v > 0)}
      <section class="block" aria-labelledby="curve-h">
        <h2 id="curve-h" class="block-h">Power curve</h2>
        <CurveChart
          durations={d.curve_durations}
          markPRs={!!d.best_curve}
          series={[
            { label: 'This ride', values: m.power_curve, style: 'primary' },
            ...(d.best_curve ? [{ label: 'Best before', values: d.best_curve, style: 'compare' as const }] : []),
          ]}
        />
      </section>
    {/if}

    {#if d.power_zones.some((v) => v > 0)}
      <section class="block" aria-labelledby="pz-h">
        <h2 id="pz-h" class="block-h">Time in power zones <span class="mono muted">FTP {a.ftp} W</span></h2>
        <ZoneRows rows={d.power_zones.map((s, i) => ({ label: `Z${i + 1}`, hint: powerZoneNames[i], seconds: s, color: powerZoneShades[i]! }))} />
      </section>
    {/if}

    {#if d.hr_zones.slice(1).some((v) => v > 0)}
      <section class="block" aria-labelledby="hz-h">
        <h2 id="hz-h" class="block-h">Time in heart rate zones <span class="mono muted">max {d.max_hr}</span></h2>
        {#if d.hr_zones_from === 'whoop'}<p class="muted small zone-src">As Whoop measured them; there's no second-by-second heart rate for this one.</p>{/if}
        <ZoneRows rows={d.hr_zones.slice(1).map((s, i) => ({ label: `Z${i + 1}`, hint: `${d!.hr_zone_bounds?.[i] ?? Math.round([0.5, 0.6, 0.7, 0.8, 0.9][i]! * d!.max_hr)}+`, seconds: s, color: `var(--zone-${i + 1})` }))} />
      </section>
    {/if}

    {#if d.versions}
      <section class="block" aria-labelledby="ver-h">
        <h2 id="ver-h" class="block-h">Versions <span class="mono muted">{d.versions.length} recordings</span></h2>
        <div class="vtable-wrap">
          <table class="vtable">
            <thead>
              <tr>
                <td></td>
                {#each versions as v (v.id)}
                  <th scope="col">
                    <button type="button" class="vhead" aria-pressed={view === v.id} onclick={() => (view = view === v.id ? '' : v.id)}>
                      {versionNames[v.id]}
                    </button>
                  </th>
                {/each}
              </tr>
            </thead>
            <tbody>
              {#each shownRows as r (r.label)}
                <tr>
                  <th scope="row">{r.label}</th>
                  {#each versions as v (v.id)}<td class="mono" class:on={view === v.id}>{r.value(v) || '–'}</td>{/each}
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
        <p class="note">
          {#if !d.view && channelNote.length}Combined: {channelNote.join('; ')}.{/if}
          Every original file is kept. Pick a version to see its own charts{#if d.view}, or Combined for all of them merged{/if}.
        </p>
      </section>
    {:else}
    <section class="block" aria-labelledby="src-h">
      <h2 id="src-h" class="block-h">Recorded by</h2>
      <ul class="sources">
        {#each d.sources as s (s.id)}
          <li>
            <span class="s-name">{SOURCE_LABEL[s.source] ?? s.source}{#if s.device && s.device.toLowerCase() !== (SOURCE_LABEL[s.source] ?? s.source).toLowerCase()}<span class="muted">{' · '}{s.device}</span>{/if}</span>
            <span class="mono muted">{time(s.start_at)} · {duration(s.elapsed_s)}{s.has_power ? ' · power' : ''}{s.has_hr ? ' · HR' : ''}{s.has_gps ? ' · GPS' : ''}</span>
          </li>
        {/each}
      </ul>
      {#if d.sources.length > 1}
        <p class="note">Merged into one: {channelNote.join('; ')}. Every original file is kept.</p>
      {/if}
    </section>
    {/if}
  {/if}
</main>

<style>
  .page {
    max-width: var(--page-list);
    margin: 0 auto;
    padding: calc(env(safe-area-inset-top) + var(--space-2)) var(--gutter) 0;
  }
  @media (min-width: 900px) {
    .page {
      max-width: var(--page-wide);
    }
  }
  .top {
    display: flex;
    justify-content: space-between;
    align-items: center;
    min-height: 2.75rem;
  }
  .back {
    margin-left: calc(var(--space-2) * -1);
  }
  .gone {
    margin-top: var(--space-7);
    display: grid;
    gap: var(--space-3);
  }
  .gone h1,
  .name {
    font-size: var(--size-heading);
  }
  .name {
    margin-top: var(--space-3);
  }
  .meta {
    margin-top: var(--space-1);
    font-size: var(--size-small);
    color: var(--text-muted);
  }
  /* The sport, painted like a team-car door panel: solid colour, dark ink. */
  .sport-tag {
    display: inline-block;
    margin-right: var(--space-2);
    padding: 0.0625rem 0.4375rem;
    border-radius: var(--radius-s);
    font-family: var(--font-ui);
    font-size: var(--size-label);
    font-weight: 700;
    letter-spacing: 0.02em;
    color: var(--on-sport);
    background: var(--sport-other);
    vertical-align: 0.0625rem;
  }
  .sport-tag.sport-ride {
    background: var(--sport-ride);
  }
  .sport-tag.sport-strength {
    background: var(--sport-strength);
  }
  .srcs {
    margin-top: var(--space-2);
  }
  .notice {
    margin-top: var(--space-3);
    padding: var(--space-3) var(--space-4);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    font-size: var(--size-small);
  }
  .map-wrap {
    margin-top: var(--space-4);
  }
  .stats {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    margin: var(--space-4) 0 0;
    border-top: 1px solid var(--line-strong);
  }
  @media (min-width: 640px) {
    .stats {
      grid-template-columns: repeat(5, minmax(0, 1fr));
    }
  }
  .stats div {
    display: grid;
    gap: var(--space-1);
    padding: var(--space-3) 0;
    border-bottom: 1px solid var(--line);
  }
  .stats dd {
    margin: 0;
    display: flex;
    align-items: baseline;
    gap: 0.25rem;
    flex-wrap: wrap;
  }
  .stats .num {
    font-size: 1.75rem;
  }
  .unit {
    font-size: var(--size-label);
    color: var(--text-muted);
  }
  .note {
    margin-top: var(--space-3);
    font-size: var(--size-label);
    color: var(--text-muted);
    max-width: 62ch;
  }
  .block {
    margin-top: var(--space-6);
  }
  .rename {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2) var(--space-3);
    margin-top: var(--space-3);
    padding: var(--space-2) var(--space-3);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-s);
  }
  .rename p {
    margin: 0;
  }
  .rename-actions {
    display: flex;
    gap: var(--space-2);
  }
  .stray {
    display: grid;
    gap: var(--space-2);
  }
  .stray p {
    margin: 0;
  }
  .tags {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-2);
    margin: var(--space-3) 0 0;
  }
  .tag {
    font-family: var(--font-mono);
    font-size: var(--size-small);
    padding: 0.0625rem 0.375rem;
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-s);
  }
  .ride-type {
    color: var(--text);
  }
  .tag-note {
    font-size: var(--size-small);
    color: var(--text-muted);
  }
  .block-h {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    align-items: baseline;
    gap: var(--space-2);
    font-size: var(--size-body);
    margin-bottom: var(--space-3);
  }
  .block-h .mono {
    font-size: var(--size-label);
    font-weight: 400;
  }
  .sources {
    list-style: none;
    margin: 0;
    padding: 0;
    border-top: 1px solid var(--line);
  }
  .sources li {
    display: grid;
    gap: 0.125rem;
    padding: var(--space-2) 0;
    border-bottom: 1px solid var(--line);
  }
  .s-name {
    font-weight: 600;
  }
  .sources .mono {
    font-size: var(--size-label);
  }
  .versions-switch {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1);
    margin-top: var(--space-3);
  }
  .vbtn {
    min-height: var(--tap-min);
    padding: 0 var(--space-3);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    background: var(--surface);
    color: var(--text);
    font: inherit;
    font-size: var(--size-small);
    cursor: pointer;
  }
  .vbtn[aria-pressed='true'] {
    background: var(--text);
    border-color: var(--text);
    color: var(--bg);
    font-weight: 600;
  }
  .view-note {
    margin-top: var(--space-2);
  }
  .lift {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    margin: 0;
  }
  .vtable-wrap {
    overflow-x: auto;
    border-top: 1px solid var(--line);
  }
  .vtable {
    width: 100%;
    border-collapse: collapse;
    font-size: var(--size-small);
  }
  .vtable th,
  .vtable td {
    padding: var(--space-2) var(--space-2) var(--space-2) 0;
    border-bottom: 1px solid var(--line);
    text-align: left;
    white-space: nowrap;
    vertical-align: baseline;
  }
  .vtable tbody th {
    font-weight: 400;
    color: var(--text-muted);
    position: sticky;
    left: 0;
    background: var(--bg);
  }
  .vtable td.on {
    font-weight: 600;
  }
  .vhead {
    min-height: var(--tap-min);
    padding: 0;
    border: 0;
    background: none;
    color: var(--text);
    font: inherit;
    font-weight: 600;
    text-decoration: underline;
    text-decoration-color: var(--line-strong);
    text-underline-offset: 3px;
    cursor: pointer;
  }
  .vhead[aria-pressed='true'] {
    text-decoration-color: var(--text);
    text-decoration-thickness: 2px;
  }
  .zone-src {
    margin: 0 0 var(--space-2);
    font-size: var(--size-small);
  }
</style>
