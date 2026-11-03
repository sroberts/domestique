<script lang="ts">
  // Fitness and fatigue share a unit (TSS/day), so one chart; form is a
  // different quantity around zero, so its own panel underneath on the same
  // time axis. One crosshair across both.
  import { linePath, nearest, niceMax, ticks } from '../lib/chart';
  import { day } from '../lib/format';
  import type { LoadDay } from '../lib/types';

  let { days }: { days: LoadDay[] } = $props();
  let width = $state(0);
  const H1 = 180;
  const H2 = 96;
  const PAD = { top: 10, right: 84, bottom: 4, left: 32 };
  let plotW = $derived(Math.max(0, width - PAD.left - PAD.right));
  let xs = $derived(days.map((_, i) => PAD.left + (days.length > 1 ? (i / (days.length - 1)) * plotW : 0)));
  let top = $derived(niceMax(Math.max(10, ...days.map((d) => Math.max(d.ctl, d.atl)))));
  const y1 = (v: number) => PAD.top + (1 - v / top) * (H1 - PAD.top - PAD.bottom);
  let tsbLo = $derived(Math.min(-35, Math.floor(Math.min(...days.map((d) => d.tsb)) / 10) * 10));
  let tsbHi = $derived(Math.max(25, Math.ceil(Math.max(...days.map((d) => d.tsb)) / 10) * 10));
  const y2 = (v: number) => 8 + (1 - (v - tsbLo) / (tsbHi - tsbLo)) * (H2 - 16);
  let barW = $derived(Math.max(1, Math.min(6, plotW / Math.max(1, days.length) - 1)));
  let hover = $state<number | null>(null);
  let sel = $derived(hover ?? days.length - 1);
  let cur = $derived(days[sel]);

  // Form bands as conventionally read.
  const bands = [
    { lo: 5, hi: 100, label: 'Fresh' },
    { lo: -10, hi: 5, label: 'Neutral' },
    { lo: -30, hi: -10, label: 'Building' },
    { lo: -100, hi: -30, label: 'Overreach' },
  ];

  function move(e: PointerEvent) {
    const rect = (e.currentTarget as Element).getBoundingClientRect();
    hover = nearest(xs, e.clientX - rect.left);
  }

  let monthTicks = $derived.by(() => {
    const out: { x: number; label: string }[] = [];
    days.forEach((d, i) => {
      if (d.date.endsWith('-01')) out.push({ x: xs[i]!, label: new Date(d.date + 'T12:00').toLocaleString('en-GB', { month: 'short' }) });
    });
    return out;
  });
</script>

<div class="load" bind:clientWidth={width}>
  {#if cur}
    <div class="read" aria-live="polite">
      <span class="mono when">{hover == null ? 'Today' : day(new Date(cur.date + 'T12:00').getTime())}</span>
      <span class="k"><span class="sw fit" aria-hidden="true"></span>Fitness <strong class="num">{Math.round(cur.ctl)}</strong></span>
      <span class="k"><span class="sw fat" aria-hidden="true"></span>Fatigue <strong class="num">{Math.round(cur.atl)}</strong></span>
      <span class="k"><span class="sw frm" aria-hidden="true"></span>Form <strong class="num">{cur.tsb > 0 ? '+' : ''}{Math.round(cur.tsb)}</strong></span>
      {#if hover != null}<span class="k muted">TSS {Math.round(cur.tss)}</span>{/if}
    </div>
  {/if}
  {#if width > 0 && days.length > 1}
    <svg {width} height={H1 + H2 + 22} onpointermove={move} onpointerdown={move} onpointerleave={() => (hover = null)}
      role="img" aria-label={`Fitness ${Math.round(days.at(-1)!.ctl)}, fatigue ${Math.round(days.at(-1)!.atl)}, form ${Math.round(days.at(-1)!.tsb)} today, over ${days.length} days.`}>
      {#each ticks(0, top, 4).filter((v) => v > 0) as v (v)}
        <line class="grid" x1={PAD.left} x2={PAD.left + plotW} y1={y1(v)} y2={y1(v)} />
        <text class="tick" x={PAD.left - 6} y={y1(v) + 4} text-anchor="end">{v}</text>
      {/each}
      <path class="line fit" d={linePath(xs, days.map((d) => y1(d.ctl)))} />
      <path class="line fat" d={linePath(xs, days.map((d) => y1(d.atl)))} />
      <!-- direct end labels, nudged apart only if they'd touch -->
      {#if days.length}
        {@const last = days.at(-1)!}
        {@const ya = y1(last.ctl)}
        {@const yb = Math.abs(y1(last.atl) - ya) < 14 ? ya + (y1(last.atl) >= ya ? 14 : -14) : y1(last.atl)}
        <circle class="end fit" cx={xs.at(-1)} cy={ya} r="4" />
        <text class="end-label" x={PAD.left + plotW + 8} y={ya + 4}>Fitness</text>
        <text class="end-label muted" x={PAD.left + plotW + 8} y={yb + 4}>Fatigue</text>
      {/if}

      <g transform={`translate(0, ${H1 + 6})`}>
        {#each bands as b (b.label)}
          {@const top2 = y2(Math.min(b.hi, tsbHi))}
          {@const bot2 = y2(Math.max(b.lo, tsbLo))}
          {#if bot2 > top2}
            <rect class="band" class:alt={b.label === 'Building' || b.label === 'Fresh'} x={PAD.left} width={plotW} y={top2} height={bot2 - top2} />
            <text class="band-label" x={PAD.left + plotW + 8} y={(top2 + bot2) / 2 + 4}>{b.label}</text>
          {/if}
        {/each}
        <line class="zero" x1={PAD.left} x2={PAD.left + plotW} y1={y2(0)} y2={y2(0)} />
        <text class="tick" x={PAD.left - 6} y={y2(0) + 4} text-anchor="end">0</text>
        {#each days as d, i (d.date)}
          {@const yy = y2(d.tsb)}
          <rect class="tsb" class:on={i === sel} x={xs[i]! - barW / 2} width={barW} y={Math.min(yy, y2(0))} height={Math.max(1, Math.abs(yy - y2(0)))} />
        {/each}
      </g>
      {#each monthTicks as m (m.x)}
        <text class="tick" x={m.x} y={H1 + H2 + 20} text-anchor="middle">{m.label}</text>
      {/each}
      {#if hover != null}
        <line class="cursor" x1={xs[hover]} x2={xs[hover]} y1={PAD.top} y2={H1 + H2 + 6} />
      {/if}
    </svg>
  {/if}
</div>

<style>
  .read {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-2) var(--space-4);
    min-height: 2rem;
    font-size: var(--size-small);
  }
  .when {
    font-size: var(--size-label);
    color: var(--text-muted);
    min-width: 5.5rem;
  }
  .k {
    display: inline-flex;
    align-items: baseline;
    gap: var(--space-1);
  }
  .k strong {
    font-size: var(--size-lead);
    font-weight: 600;
  }
  .sw {
    align-self: center;
    width: 1rem;
    height: 0;
    border-top: 2px solid var(--text);
  }
  .sw.fat {
    border-top-style: dashed;
    border-top-color: var(--text-muted);
  }
  .sw.frm {
    height: 0.625rem;
    width: 0.375rem;
    border: 0;
    background: var(--text-muted);
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
  .tick,
  .band-label,
  .end-label {
    font-family: var(--font-mono);
    font-size: 11px;
    fill: var(--text-muted);
  }
  .end-label {
    font-family: var(--font-ui);
    font-size: 12px;
    font-weight: 600;
    fill: var(--text);
  }
  .end-label.muted {
    fill: var(--text-muted);
  }
  .line {
    fill: none;
    stroke-width: 2;
    stroke-linejoin: round;
  }
  .line.fit {
    stroke: var(--text);
  }
  .line.fat {
    stroke: var(--text-muted);
    stroke-dasharray: 4 3;
  }
  .end {
    fill: var(--text);
    stroke: var(--bg);
    stroke-width: 2;
  }
  .band {
    fill: transparent;
  }
  .band.alt {
    fill: var(--band);
  }
  .zero {
    stroke: var(--line-strong);
  }
  .tsb {
    fill: var(--text-muted);
  }
  .tsb.on {
    fill: var(--text);
  }
  .cursor {
    stroke: var(--text-muted);
  }
</style>
