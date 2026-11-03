<script lang="ts">
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import CurveChart from '../components/CurveChart.svelte';
  import RouteMap from '../components/RouteMap.svelte';
  import Sources from '../components/Sources.svelte';
  import StreamPanels from '../components/StreamPanels.svelte';
  import ZoneRows from '../components/ZoneRows.svelte';
  import { fetchActivity } from '../lib/data';
  import { SOURCE_LABEL, SPORT_LABEL, day, duration, km, thousands, time } from '../lib/format';
  import { link, router } from '../lib/router.svelte';
  import type { ActivityDetail } from '../lib/types';

  let { id }: { id: string } = $props();
  let d = $state<ActivityDetail | null>(null);
  let stale = $state<number | null>(null);
  let failed = $state(false);
  let cursor = $state<number | null>(null);

  $effect(() => {
    fetchActivity(id).then((res) => {
      d = res.data;
      stale = res.stale;
      failed = !res.data;
      // A logged strength session has its own, richer screen.
      if (res.data?.activity.session_id) router.go(`/session/${res.data.activity.session_id}/summary`, { replace: true });
    });
  });

  let a = $derived(d?.activity);
  let m = $derived(d?.metrics);
  let isRide = $derived(a?.sport === 'ride' || a?.sport === 'virtual_ride');
  let hrZoneBounds = $derived(d ? [0.5, 0.6, 0.7, 0.8, 0.9].map((p) => Math.round(p * d!.max_hr)) : undefined);
  let cursorLatLng = $derived.by((): [number, number] | null => {
    if (cursor == null || !d?.stream.route?.length || !d.stream.t.length) return null;
    const frac = cursor / Math.max(1, d.stream.t.length - 1);
    return d.stream.route[Math.round(frac * (d.stream.route.length - 1))] ?? null;
  });

  const powerZoneNames = ['Recovery', 'Endurance', 'Tempo', 'Threshold', 'VO2 max', 'Anaerobic', 'Neuromuscular'];
  const powerZoneShades = ['var(--zone-1)', 'var(--zone-2)', 'var(--zone-3)', 'var(--zone-4)', 'var(--zone-4)', 'var(--zone-5)', 'var(--zone-5)'];

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
        return 'Scored by heart rate: no power meter on this one.';
      case 'strain':
        return `Scored from Whoop strain ${a.strain?.toFixed(1)}.`;
    }
    return 'No power or heart rate, so no load score.';
  }
</script>

<main class="page">
  <header class="top">
    <a class="btn btn-quiet back" href="/activities" use:link><ChevronLeft size={20} aria-hidden="true" /> Activities</a>
  </header>

  {#if failed && !d}
    <section class="gone">
      <h1>Activity not available</h1>
      <p class="muted">It isn't on the Mac Mini, or there's no connection and it hasn't been opened on this device before.</p>
    </section>
  {:else if d && a && m}
    <h1 class="name">{a.name}</h1>
    <p class="mono meta">
      {day(a.start_at)} · {time(a.start_at)} · {SPORT_LABEL[a.sport] ?? a.sport}{#if a.route} · {a.route}{/if}
    </p>
    <div class="srcs"><Sources sources={d.sources} /></div>
    {#if stale}<p class="notice">No connection. Showing this activity as last opened at {time(stale)}.</p>{/if}

    {#if d.stream.route && d.stream.route.length > 1}
      <div class="map-wrap"><RouteMap route={d.stream.route} cursor={cursorLatLng} virtual={a.sport === 'virtual_ride'} /></div>
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
    <p class="mono note">{loadNote()}</p>

    {#if d.stream.t.length > 1}
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
        <ZoneRows rows={d.hr_zones.slice(1).map((s, i) => ({ label: `Z${i + 1}`, hint: `${Math.round([0.5, 0.6, 0.7, 0.8, 0.9][i]! * d!.max_hr)}+`, seconds: s, color: `var(--zone-${i + 1})` }))} />
      </section>
    {/if}

    <section class="block" aria-labelledby="src-h">
      <h2 id="src-h" class="block-h">Recorded by</h2>
      <ul class="sources">
        {#each d.sources as s (s.id)}
          <li>
            <span class="s-name">{SOURCE_LABEL[s.source] ?? s.source}{#if s.device}<span class="muted">{' · '}{s.device}</span>{/if}</span>
            <span class="mono muted">{time(s.start_at)} · {duration(s.elapsed_s)}{s.has_power ? ' · power' : ''}{s.has_hr ? ' · HR' : ''}{s.has_gps ? ' · GPS' : ''}</span>
          </li>
        {/each}
      </ul>
      {#if d.sources.length > 1}
        <p class="mono note">Merged into one: {channelNote.join('; ')}. Every original file is kept.</p>
      {/if}
    </section>
  {/if}
</main>

<style>
  .page {
    max-width: 40rem;
    margin: 0 auto;
    padding: calc(env(safe-area-inset-top) + var(--space-2)) var(--gutter) 0;
  }
  @media (min-width: 900px) {
    .page {
      max-width: 56rem;
    }
  }
  .top {
    display: flex;
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
</style>
