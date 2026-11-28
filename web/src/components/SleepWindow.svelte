<script lang="ts">
  // When each night was spent in bed: a bar from bedtime down to waking, so a
  // regular week reads as a level band and a late night sticks out. Times are
  // counted from noon, so a bar can cross midnight. Hover to read a night.
  import { day } from '../lib/format';
  import type { SleepNight } from '../lib/types';

  let { nights, bed, wake }: { nights: SleepNight[]; bed: string; wake: string } = $props();
  let width = $state(0);
  const H = 176;
  const PAD = { top: 10, right: 4, bottom: 22, left: 40 };

  /** Minutes after noon: bedtime before noon is the small hours. */
  function fromNoon(t: string, isWake: boolean): number {
    const [h, m] = t.split(':').map(Number) as [number, number];
    const min = h * 60 + m;
    if (isWake) return h < 18 ? min + 12 * 60 : min - 12 * 60;
    return h < 12 ? min + 12 * 60 : min - 12 * 60;
  }
  let rows = $derived(nights.map((n) => ({ n, from: fromNoon(n.bed, false), to: fromNoon(n.wake, true) })));
  let lo = $derived(Math.floor((Math.min(...rows.map((r) => r.from)) - 30) / 60) * 60);
  let hi = $derived(Math.ceil((Math.max(...rows.map((r) => r.to)) + 30) / 60) * 60);
  let slot = $derived((width - PAD.left - PAD.right) / Math.max(1, nights.length));
  let bw = $derived(Math.min(14, Math.max(2, slot - 2)));
  const y = (m: number) => PAD.top + ((m - lo) / Math.max(60, hi - lo)) * (H - PAD.top - PAD.bottom);
  let hours = $derived.by(() => {
    const step = hi - lo > 10 * 60 ? 180 : 120;
    const out: number[] = [];
    for (let m = Math.ceil(lo / step) * step; m <= hi; m += step) out.push(m);
    return out;
  });
  const clockOf = (m: number) => `${String((Math.floor(m / 60) + 12) % 24).padStart(2, '0')}:00`;
  let hover = $state<number | null>(null);
  let cur = $derived(nights[hover ?? nights.length - 1]);
  const dayOf = (d: string) => day(new Date(`${d}T12:00`).getTime());
  let every = $derived(Math.max(1, Math.ceil(nights.length / 6)));
  let typical = $derived(bed && wake ? { from: fromNoon(bed, false), to: fromNoon(wake, true) } : null);
</script>

<div class="window" bind:clientWidth={width}>
  {#if cur}
    <p class="read" aria-live="polite">
      <span class="mono when">{hover == null ? 'Last night' : dayOf(cur.date)}</span>
      <strong class="num">{cur.bed}</strong> to <strong class="num">{cur.wake}</strong>
    </p>
  {/if}
  {#if width > 0 && rows.length}
    <svg {width} height={H} role="img" aria-label={`Bed and wake times for ${nights.length} nights; usually ${bed} to ${wake}`}>
      {#each hours as m (m)}
        <line class="grid" x1={PAD.left} x2={width - PAD.right} y1={y(m)} y2={y(m)} />
        <text class="axis" x={PAD.left - 6} y={y(m) + 4} text-anchor="end">{clockOf(m)}</text>
      {/each}
      {#if typical}
        <!-- The usual window, behind the nights: what "regular" means here. -->
        <rect class="usual" x={PAD.left} width={width - PAD.left - PAD.right} y={y(typical.from)} height={Math.max(1, y(typical.to) - y(typical.from))} />
      {/if}
      {#each rows as r, i (r.n.date)}
        {@const cx = PAD.left + slot * i + slot / 2}
        <rect class="inbed" class:dim={hover != null && hover !== i} x={cx - bw / 2} width={bw} y={y(r.from)}
          height={Math.max(2, y(r.to) - y(r.from))} rx={Math.min(3, bw / 2)} />
        <rect class="hit" x={PAD.left + slot * i} width={slot} y={PAD.top} height={H - PAD.top - PAD.bottom}
          role="presentation" onpointerenter={() => (hover = i)} onpointerdown={() => (hover = i)} onpointerleave={() => (hover = null)} />
        {#if (nights.length - 1 - i) % every === 0}
          <text class="axis" x={cx} y={H - 6} text-anchor="middle">{new Date(`${r.n.date}T12:00`).toLocaleString('en-GB', { day: 'numeric', month: 'short' })}</text>
        {/if}
      {/each}
    </svg>
  {/if}
</div>

<style>
  .read {
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
  .usual {
    fill: var(--band);
  }
  .inbed {
    fill: var(--sleep-deep);
    transition: opacity var(--dur-state) var(--ease-out);
  }
  .inbed.dim {
    opacity: 0.45;
  }
  .hit {
    fill: transparent;
    cursor: pointer;
  }
</style>
