<script lang="ts">
  // Mean-maximal power curve on a log time axis. Two series at most, with a
  // legend; points where the primary series beats the comparison are PRs.
  import { linePath, ticks } from '../lib/chart';
  import { dur } from '../lib/format';

  interface Series {
    label: string;
    values: number[]; // aligned with durations; 0 = no data
    style: 'primary' | 'compare';
  }
  interface Props {
    durations: number[];
    series: Series[];
    markPRs?: boolean;
  }
  let { durations, series, markPRs = false }: Props = $props();

  let width = $state(0);
  const H = 200;
  const PAD = { top: 12, right: 8, bottom: 24, left: 36 };
  let lastIdx = $derived.by(() => {
    let last = 0;
    for (const s of series) s.values.forEach((v, i) => v > 0 && (last = Math.max(last, i)));
    return Math.max(1, last);
  });
  let dmin = $derived(Math.log(durations[0] ?? 1));
  let dmax = $derived(Math.log(durations[lastIdx] ?? 3600));
  let vmax = $derived(Math.max(100, ...series.flatMap((s) => s.values)) * 1.08);
  let vmin = $derived.by(() => {
    const vs = series.flatMap((s) => s.values.slice(0, lastIdx + 1)).filter((v) => v > 0);
    return vs.length ? Math.max(0, Math.floor((Math.min(...vs) * 0.85) / 50) * 50) : 0;
  });
  const x = (i: number) => PAD.left + ((Math.log(durations[i]!) - dmin) / (dmax - dmin)) * (width - PAD.left - PAD.right);
  const y = (v: number) => PAD.top + (1 - (v - vmin) / (vmax - vmin)) * (H - PAD.top - PAD.bottom);

  let xs = $derived(durations.slice(0, lastIdx + 1).map((_, i) => x(i)));
  // Label durations people think in, skipping any that would collide.
  let labelIdx = $derived.by(() => {
    const out: number[] = [];
    for (const i of [0, 2, 5, 8, 11, 14, 16]) {
      if (i > lastIdx) continue;
      if (out.length && x(i) - x(out.at(-1)!) < 40) continue;
      out.push(i);
    }
    return out;
  });
  let hover = $state<number | null>(null);

  function move(e: PointerEvent) {
    const rect = (e.currentTarget as Element).getBoundingClientRect();
    const px = e.clientX - rect.left;
    let best = 0;
    xs.forEach((xx, i) => Math.abs(xx - px) < Math.abs(xs[best]! - px) && (best = i));
    hover = best;
  }

  let primary = $derived(series.find((s) => s.style === 'primary'));
  let compare = $derived(series.find((s) => s.style === 'compare'));
  let prs = $derived(
    markPRs && primary && compare ? primary.values.map((v, i) => v > 0 && v >= (compare.values[i] ?? 0) && i <= lastIdx) : [],
  );
</script>

<div class="curve" bind:clientWidth={width}>
  <div class="legend">
    {#each series as s (s.label)}
      <span class="key"><span class="sw {s.style}" aria-hidden="true"></span>{s.label}</span>
    {/each}
    {#if markPRs && prs.some(Boolean)}<span class="key"><span class="pr-dot" aria-hidden="true"></span>Personal best</span>{/if}
  </div>
  <p class="read mono" aria-live="polite">
    {#if hover != null}
      <strong>{dur(durations[hover]!)}</strong>
      {#each series as s (s.label)}
        · {s.label} <strong>{s.values[hover] ? `${s.values[hover]} W` : '–'}</strong>
      {/each}
    {:else}
      Touch the chart to read a duration.
    {/if}
  </p>
  {#if width > 0}
    <svg {width} height={H} onpointermove={move} onpointerdown={move} onpointerleave={() => (hover = null)} role="img"
      aria-label={`Power curve: ${series.map((s) => `${s.label} ${s.values[0] ?? 0} W at 1 s, ${s.values[11] ?? 0} W at 20 min`).join('; ')}`}>
      {#each ticks(vmin, vmax, 4) as v (v)}
        <line class="grid" x1={PAD.left} x2={width - PAD.right} y1={y(v)} y2={y(v)} />
        <text class="tick" x={PAD.left - 6} y={y(v) + 4} text-anchor="end">{v}</text>
      {/each}
      {#each labelIdx as i (i)}
        <text class="tick" x={x(i)} y={H - 6} text-anchor="middle">{dur(durations[i]!).replace(' ', '')}</text>
      {/each}
      {#if compare}
        <path class="line compare" d={linePath(xs, compare.values.slice(0, lastIdx + 1).map((v) => (v > 0 ? y(v) : null)))} />
      {/if}
      {#if primary}
        <path class="line primary" d={linePath(xs, primary.values.slice(0, lastIdx + 1).map((v) => (v > 0 ? y(v) : null)))} />
        {#each prs as pr, i (i)}
          {#if pr}<circle class="pr" cx={xs[i]} cy={y(primary.values[i]!)} r="4" />{/if}
        {/each}
      {/if}
      {#if hover != null}
        <line class="cursor" x1={xs[hover]} x2={xs[hover]} y1={PAD.top} y2={H - PAD.bottom} />
      {/if}
    </svg>
  {/if}
</div>

<style>
  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
    font-size: var(--size-small);
  }
  .key {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
  }
  .sw {
    width: 1.25rem;
    height: 0;
    border-top: 2px solid var(--text);
  }
  .sw.compare {
    border-top: 2px dashed var(--text-muted);
  }
  .pr-dot {
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 50%;
    background: var(--signal);
  }
  .read {
    min-height: 1.5rem;
    margin-top: var(--space-2);
    font-size: var(--size-label);
    color: var(--text-muted);
  }
  .read strong {
    color: var(--text);
    font-weight: 500;
  }
  svg {
    display: block;
    overflow: visible;
    cursor: crosshair;
    touch-action: pan-y;
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
    stroke-width: 2;
    stroke-linejoin: round;
    stroke-linecap: round;
  }
  .line.primary {
    stroke: var(--text);
  }
  .line.compare {
    stroke: var(--text-muted);
    stroke-dasharray: 5 4;
  }
  .pr {
    fill: var(--signal);
    stroke: var(--bg);
    stroke-width: 2;
  }
  .cursor {
    stroke: var(--text-muted);
  }
</style>
