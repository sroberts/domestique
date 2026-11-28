<script lang="ts">
  // Each night's time asleep as a bar, with a tick for what Whoop said was
  // needed. One series, so no legend box: the key names the bar and the tick.
  // Hover or tap a night to read it.
  import { niceMax, ticks } from '../lib/chart';
  import { day, hm } from '../lib/format';
  import type { SleepNight } from '../lib/types';

  let { nights }: { nights: SleepNight[] } = $props();
  let width = $state(0);
  const H = 168;
  const PAD = { top: 8, right: 4, bottom: 22, left: 28 };
  let top = $derived(niceMax(Math.max(8, ...nights.map((n) => Math.max(n.asleep_s, n.need_s ?? 0) / 3600))));
  let slot = $derived((width - PAD.left - PAD.right) / Math.max(1, nights.length));
  let bw = $derived(Math.min(18, Math.max(2, slot - 2)));
  const y = (h: number) => PAD.top + (1 - h / top) * (H - PAD.top - PAD.bottom);
  let hover = $state<number | null>(null);
  let cur = $derived(nights[hover ?? nights.length - 1]);
  const dayOf = (d: string) => day(new Date(`${d}T12:00`).getTime());
  // Label about six dates whatever the range, so 12 weeks doesn't crowd.
  let every = $derived(Math.max(1, Math.ceil(nights.length / 6)));

  function bar(cx: number, top: number, bottom: number): string {
    const h = Math.max(1, bottom - top);
    const r = Math.min(4, bw / 2, h);
    const l = cx - bw / 2;
    return `M${l},${bottom} V${top + r} Q${l},${top} ${l + r},${top} H${l + bw - r} Q${l + bw},${top} ${l + bw},${top + r} V${bottom} Z`;
  }
</script>

<div class="nights" bind:clientWidth={width}>
  <div class="legend" aria-hidden="true">
    <span class="key"><span class="sw"></span>Asleep</span>
    <span class="key"><span class="tick-key"></span>Needed</span>
  </div>
  {#if cur}
    <p class="read" aria-live="polite">
      <span class="mono when">{hover == null ? 'Last night' : dayOf(cur.date)}</span>
      <strong class="num">{hm(cur.asleep_s)}</strong> {cur.need_s ? `asleep · ${hm(cur.need_s)} needed` : 'asleep'}
    </p>
  {/if}
  {#if width > 0}
    <svg {width} height={H} role="img" aria-label={`Hours asleep each night for ${nights.length} nights, against need`}>
      {#each ticks(0, top, 4) as v (v)}
        <line class="grid" x1={PAD.left} x2={width - PAD.right} y1={y(v)} y2={y(v)} />
        <text class="axis" x={PAD.left - 6} y={y(v) + 4} text-anchor="end">{v}h</text>
      {/each}
      {#each nights as n, i (n.date)}
        {@const cx = PAD.left + slot * i + slot / 2}
        <g class="nt" class:dim={hover != null && hover !== i}>
          <path class="asleep" d={bar(cx, y(n.asleep_s / 3600), y(0))} />
          {#if n.need_s}
            <line class="need" x1={cx - bw / 2 - 2} x2={cx + bw / 2 + 2} y1={y(n.need_s / 3600)} y2={y(n.need_s / 3600)} />
          {/if}
        </g>
        <rect class="hit" x={PAD.left + slot * i} width={slot} y={PAD.top} height={H - PAD.top - PAD.bottom}
          role="presentation" onpointerenter={() => (hover = i)} onpointerdown={() => (hover = i)} onpointerleave={() => (hover = null)} />
        {#if (nights.length - 1 - i) % every === 0}
          <text class="axis" x={cx} y={H - 6} text-anchor="middle">{new Date(`${n.date}T12:00`).toLocaleString('en-GB', { day: 'numeric', month: 'short' })}</text>
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
    background: var(--sleep-deep);
  }
  .tick-key {
    width: 0.875rem;
    height: 2px;
    background: var(--text);
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
  .axis {
    font-family: var(--font-mono);
    font-size: 11px;
    fill: var(--text-muted);
  }
  .asleep {
    fill: var(--sleep-deep);
  }
  .need {
    stroke: var(--text);
    stroke-width: 2;
    stroke-linecap: round;
  }
  .nt {
    transition: opacity var(--dur-state) var(--ease-out);
  }
  .nt.dim {
    opacity: 0.45;
  }
  .hit {
    fill: transparent;
    cursor: pointer;
  }
</style>
