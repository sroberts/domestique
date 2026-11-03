<script lang="ts">
  // Ride streams as stacked small multiples on one shared time axis: one
  // measure per panel (never two y-scales), one crosshair across all of them.
  import { areaPath, linePath, nearest, niceMax, ticks } from '../lib/chart';
  import { clock } from '../lib/format';
  import type { ChartStream } from '../lib/types';

  interface Props {
    stream: ChartStream;
    hrZones?: number[]; // lower bounds of Z1..Z5
    cursor: number | null;
    oncursor: (i: number | null) => void;
  }
  let { stream, hrZones, cursor, oncursor }: Props = $props();

  type Panel = { key: string; label: string; unit: string; values: number[]; kind: 'line' | 'area'; fmt: (v: number) => string };
  const f0 = (v: number) => String(Math.round(v));
  const f1 = (v: number) => v.toFixed(1);
  let panels = $derived.by(() => {
    const p: Panel[] = [];
    if (stream.power?.some((v) => v > 0)) p.push({ key: 'power', label: 'Power', unit: 'W', values: stream.power, kind: 'line', fmt: f0 });
    if (stream.hr?.some((v) => v > 0)) p.push({ key: 'hr', label: 'Heart rate', unit: 'bpm', values: stream.hr, kind: 'line', fmt: f0 });
    if (stream.cadence?.some((v) => v > 0)) p.push({ key: 'cadence', label: 'Cadence', unit: 'rpm', values: stream.cadence, kind: 'line', fmt: f0 });
    if (stream.speed?.some((v) => v > 0)) p.push({ key: 'speed', label: 'Speed', unit: 'km/h', values: stream.speed, kind: 'line', fmt: f1 });
    if (stream.altitude?.some((v) => v !== 0)) p.push({ key: 'altitude', label: 'Elevation', unit: 'm', values: stream.altitude, kind: 'area', fmt: f0 });
    return p;
  });

  let width = $state(0);
  const H = 92;
  const PAD = { top: 22, right: 4, bottom: 6, left: 0 };
  let plotW = $derived(Math.max(0, width - PAD.left - PAD.right));
  let t0 = $derived(stream.t[0] ?? 0);
  let t1 = $derived(stream.t.at(-1) ?? 1);
  // One point per ~1.5px: more than that and a noisy channel paints a solid band.
  let step = $derived(Math.max(1, Math.ceil(stream.t.length / Math.max(1, plotW / 1.5))));
  let idx = $derived(Array.from({ length: Math.ceil(stream.t.length / step) }, (_, i) => i * step));
  let xs = $derived(idx.map((i) => PAD.left + ((stream.t[i]! - t0) / Math.max(1, t1 - t0)) * plotW));
  function bucket(v: number[]): number[] {
    return idx.map((i) => {
      let sum = 0;
      let n = 0;
      for (let k = i; k < Math.min(i + step, v.length); k++) if (v[k]! >= 0) (sum += v[k]!), n++;
      return n ? sum / n : -1;
    });
  }

  function geom(p: Panel) {
    const valid = p.values.filter((v) => v >= 0);
    let lo = 0;
    let hi = niceMax(Math.max(...valid, 1));
    if (p.key === 'hr' || p.key === 'altitude') {
      const mn = Math.min(...valid);
      lo = Math.max(0, Math.floor(mn / 10) * 10 - (p.key === 'hr' ? 10 : 0));
      hi = Math.max(lo + 10, Math.ceil(Math.max(...valid) / 10) * 10);
    }
    const y = (v: number) => PAD.top + (1 - (v - lo) / (hi - lo)) * (H - PAD.top - PAD.bottom);
    const ys = bucket(p.values).map((v) => (v >= 0 ? y(v) : null));
    let avg = 0;
    if (valid.length) avg = valid.reduce((a, b) => a + b, 0) / valid.length;
    return { lo, hi, y, ys, avg, max: valid.length ? Math.max(...valid) : 0 };
  }

  // HR coloured by zone, like the strength trace.
  function zoneRuns(p: Panel, ys: (number | null)[]) {
    if (!hrZones) return [];
    const zoneOf = (b: number) => {
      for (let i = 4; i >= 0; i--) if (b >= hrZones[i]!) return i + 1;
      return 1;
    };
    const runs: { z: number; d: string }[] = [];
    let cur: { z: number; pts: string[] } | null = null;
    bucket(p.values).forEach((v, i) => {
      const y = ys[i];
      if (y == null) {
        if (cur) runs.push({ z: cur.z, d: 'M' + cur.pts.join('L') });
        cur = null;
        return;
      }
      const z = zoneOf(v);
      const pt = `${xs[i]!.toFixed(1)},${y.toFixed(1)}`;
      if (!cur || cur.z !== z) {
        if (cur) {
          cur.pts.push(pt);
          runs.push({ z: cur.z, d: 'M' + cur.pts.join('L') });
        }
        cur = { z, pts: [pt] };
      } else cur.pts.push(pt);
    });
    if (cur) runs.push({ z: (cur as { z: number }).z, d: 'M' + (cur as { pts: string[] }).pts.join('L') });
    return runs;
  }

  function move(e: PointerEvent) {
    const rect = (e.currentTarget as Element).getBoundingClientRect();
    oncursor(idx[nearest(xs, e.clientX - rect.left)] ?? null);
  }

  let timeTicks = $derived(ticks(0, (t1 - t0) / 60, width > 500 ? 8 : 4).map((m) => ({ m, x: PAD.left + ((m * 60) / Math.max(1, t1 - t0)) * plotW })));
</script>

<div class="panels" bind:clientWidth={width} role="group" aria-label="Ride streams over time">
  {#if width > 0}
    {#each panels as p (p.key)}
      {@const g = geom(p)}
      <figure class="panel">
        <figcaption>
          <span class="label">{p.label}</span>
          <span class="read mono">
            {#if cursor != null && p.values[cursor] != null && p.values[cursor]! >= 0}
              <strong>{p.fmt(p.values[cursor]!)}</strong> {p.unit}
            {:else}
              avg {p.fmt(g.avg)} · max {p.fmt(g.max)} {p.unit}
            {/if}
          </span>
        </figcaption>
        <svg {width} height={H} onpointermove={move} onpointerdown={move} onpointerleave={() => oncursor(null)} aria-hidden="true">
          <line class="grid" x1="0" x2={width} y1={g.y(g.hi)} y2={g.y(g.hi)} />
          <line class="grid" x1="0" x2={width} y1={g.y(g.lo)} y2={g.y(g.lo)} />
          {#if p.kind === 'area'}
            <path class="area" d={areaPath(xs, g.ys, g.y(g.lo))} />
            <path class="line muted" d={linePath(xs, g.ys)} />
          {:else if p.key === 'hr' && hrZones}
            {#each zoneRuns(p, g.ys) as r, i (i)}
              <path class="line" d={r.d} style:stroke={`var(--zone-${r.z})`} />
            {/each}
          {:else}
            <path class="area faint" d={areaPath(xs, g.ys, g.y(g.lo))} />
            <path class="line" d={linePath(xs, g.ys)} />
          {/if}
          {#if cursor != null}
            {@const ci = Math.min(xs.length - 1, Math.floor(cursor / step))}
            <line class="cursor" x1={xs[ci]} x2={xs[ci]} y1={PAD.top - 4} y2={H} />
            {#if g.ys[ci] != null}
              <circle class="dot" cx={xs[ci]} cy={g.ys[ci]} r="4" />
            {/if}
          {/if}
        </svg>
      </figure>
    {/each}
    <svg class="axis" {width} height="20" aria-hidden="true">
      {#each timeTicks as tk (tk.m)}
        <text class="tick" x={tk.x} y="14" text-anchor={tk.m === 0 ? 'start' : 'middle'}>{tk.m === 0 ? '0' : clock(tk.m * 60).replace(/:00$/, '') + (tk.m >= 60 ? '' : '')}</text>
      {/each}
    </svg>
    {#if cursor != null}
      <p class="mono cur-time" aria-live="off">{clock(stream.t[cursor]! - t0)}{#if stream.distance?.[cursor] != null} · {stream.distance[cursor]!.toFixed(1)} km{/if}</p>
    {/if}
  {/if}
</div>

<style>
  .panels {
    position: relative;
    touch-action: pan-y;
  }
  .panel {
    margin: 0;
    border-top: 1px solid var(--line);
  }
  figcaption {
    position: absolute;
    display: flex;
    justify-content: space-between;
    width: 100%;
    padding-top: var(--space-1);
    pointer-events: none;
  }
  .panel {
    position: relative;
  }
  .read {
    font-size: var(--size-label);
    color: var(--text-muted);
  }
  .read strong {
    color: var(--text);
    font-weight: 500;
    font-size: var(--size-small);
  }
  svg {
    display: block;
    overflow: visible;
    user-select: none;
    -webkit-user-select: none;
    cursor: crosshair;
  }
  .grid {
    stroke: var(--grid);
  }
  .tick {
    font-family: var(--font-mono);
    font-size: 11px;
    fill: var(--text-muted);
  }
  .line {
    fill: none;
    stroke: var(--text);
    stroke-width: 1.5;
    stroke-linejoin: round;
  }
  .line.muted {
    stroke: var(--text-muted);
  }
  .area {
    fill: var(--text-muted);
    opacity: 0.18;
  }
  .area.faint {
    fill: var(--text);
    opacity: 0.06;
  }
  .cursor {
    stroke: var(--text-muted);
  }
  .dot {
    fill: var(--text);
    stroke: var(--bg);
    stroke-width: 2;
  }
  .axis {
    cursor: default;
  }
  .cur-time {
    position: absolute;
    right: 0;
    bottom: -1.5rem;
    font-size: var(--size-label);
    color: var(--text);
  }
</style>
