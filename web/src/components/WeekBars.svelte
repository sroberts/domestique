<script lang="ts">
  // Weekly load stacked by sport: rides, strength, other. Legend always shown;
  // hovering a week reads out its parts.
  import { niceMax, ticks } from '../lib/chart';
  import { day } from '../lib/format';
  import type { Week } from '../lib/types';

  let { weeks }: { weeks: Week[] } = $props();
  let width = $state(0);
  const H = 160;
  const PAD = { top: 8, right: 4, bottom: 22, left: 32 };
  const parts = [
    { key: 'ride_tss', label: 'Rides', color: 'var(--sport-ride)' },
    { key: 'strength_tss', label: 'Strength', color: 'var(--sport-strength)' },
    { key: 'other_tss', label: 'Other', color: 'var(--sport-other)' },
  ] as const;
  let total = (w: Week) => w.ride_tss + w.strength_tss + w.other_tss;
  let top = $derived(niceMax(Math.max(50, ...weeks.map(total))));
  let slot = $derived((width - PAD.left - PAD.right) / Math.max(1, weeks.length));
  let bw = $derived(Math.min(24, Math.max(4, slot - 4)));
  const y = (v: number) => PAD.top + (1 - v / top) * (H - PAD.top - PAD.bottom);
  let hover = $state<number | null>(null);
  let sel = $derived(hover ?? weeks.length - 1);
  let cur = $derived(weeks[sel]);
  const GAP = 2;

  function segs(w: Week) {
    let base = 0;
    const out: { color: string; y: number; h: number; top: boolean }[] = [];
    const nonzero = parts.filter((p) => w[p.key] > 0);
    nonzero.forEach((p, i) => {
      const v = w[p.key];
      const yTop = y(base + v);
      const yBot = y(base);
      out.push({ color: p.color, y: yTop + (i === nonzero.length - 1 ? 0 : 0), h: Math.max(1, yBot - yTop - (i > 0 ? GAP : 0)), top: i === nonzero.length - 1 });
      base += v;
    });
    return out;
  }
</script>

<div class="weeks" bind:clientWidth={width}>
  <div class="legend">
    {#each parts as p (p.key)}<span class="key"><span class="sw" style:background={p.color} aria-hidden="true"></span>{p.label}</span>{/each}
  </div>
  {#if cur}
    <p class="read" aria-live="polite">
      <span class="mono when">{hover == null ? 'This week' : `Week of ${day(new Date(cur.start + 'T12:00').getTime())}`}</span>
      <strong class="num">{Math.round(total(cur))}</strong> TSS · {cur.hours} h{#if cur.ride_km} · {cur.ride_km} km{/if}
      <span class="muted">({Math.round(cur.ride_tss)} ride, {Math.round(cur.strength_tss)} strength{cur.other_tss ? `, ${Math.round(cur.other_tss)} other` : ''})</span>
    </p>
  {/if}
  {#if width > 0}
    <svg {width} height={H} role="img" aria-label={`Weekly training load for ${weeks.length} weeks`}>
      {#each ticks(0, top, 3) as v (v)}
        <line class="grid" x1={PAD.left} x2={width - PAD.right} y1={y(v)} y2={y(v)} />
        <text class="tick" x={PAD.left - 6} y={y(v) + 4} text-anchor="end">{v}</text>
      {/each}
      {#each weeks as w, i (w.start)}
        {@const cx = PAD.left + slot * i + slot / 2}
        <g class="wk" class:dim={hover != null && hover !== i}>
          {#each segs(w) as s, j (j)}
            <path fill={s.color} d={s.top
              ? `M${cx - bw / 2},${s.y + s.h} V${s.y + Math.min(4, s.h)} Q${cx - bw / 2},${s.y} ${cx - bw / 2 + Math.min(4, bw / 2)},${s.y} H${cx + bw / 2 - Math.min(4, bw / 2)} Q${cx + bw / 2},${s.y} ${cx + bw / 2},${s.y + Math.min(4, s.h)} V${s.y + s.h} Z`
              : `M${cx - bw / 2},${s.y} h${bw} v${s.h} h${-bw} Z`} />
          {/each}
        </g>
        <!-- generous hit target per week -->
        <rect class="hit" x={PAD.left + slot * i} width={slot} y={PAD.top} height={H - PAD.top - PAD.bottom}
          role="presentation" onpointerenter={() => (hover = i)} onpointerdown={() => (hover = i)} onpointerleave={() => (hover = null)} />
        {#if i % 4 === 0}
          <text class="tick" x={cx} y={H - 6} text-anchor="middle">{new Date(w.start + 'T12:00').toLocaleString('en-GB', { day: 'numeric', month: 'short' })}</text>
        {/if}
      {/each}
    </svg>
  {/if}
</div>

<style>
  .legend {
    display: flex;
    gap: var(--space-4);
    font-size: var(--size-small);
  }
  .key {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
  }
  .sw {
    width: 0.625rem;
    height: 0.625rem;
    border-radius: 2px;
  }
  .read {
    margin-top: var(--space-2);
    min-height: 1.5rem;
    font-size: var(--size-small);
  }
  .read .num {
    font-size: var(--size-lead);
  }
  .when {
    font-size: var(--size-label);
    color: var(--text-muted);
    margin-right: var(--space-2);
  }
  svg {
    display: block;
    overflow: visible;
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
  .wk {
    transition: opacity var(--dur-state) var(--ease-out);
  }
  .wk.dim {
    opacity: 0.45;
  }
  .hit {
    fill: transparent;
    cursor: pointer;
  }
</style>
