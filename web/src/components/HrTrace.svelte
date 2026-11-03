<script lang="ts">
  // The Session Summary trace: heart rate over time with every set shaded as a
  // band, like a stage profile with its climbs marked. Lifts are labelled along
  // the foot like towns on the route.
  import type { Summary } from '../lib/types';

  interface Props {
    summary: Summary;
    selected: string | null;
    onselect: (id: string) => void;
  }
  let { summary, selected, onselect }: Props = $props();

  let width = $state(0);
  let height = $derived(width >= 640 ? 280 : 220);
  const PAD = { top: 22, right: 28, bottom: 30, left: 0 };

  let sets = $derived(summary.sets);
  let start = $derived(summary.session.started_at);
  let end = $derived(summary.session.finished_at ?? sets.at(-1)?.ended_at ?? start);
  let t0 = $derived(start - 60_000);
  let t1 = $derived(end + 60_000);

  let visible = $derived(summary.hr.filter((h) => h.t >= t0 && h.t <= t1));
  let lo = $derived.by(() => {
    const min = visible.length ? Math.min(...visible.map((h) => h.bpm)) : summary.zones[0];
    return Math.floor((Math.min(min, summary.zones[0]) - 6) / 10) * 10;
  });
  let hi = $derived.by(() => {
    const max = visible.length ? Math.max(...visible.map((h) => h.bpm)) : summary.zones[3];
    return Math.max(max + 8, summary.zones[3] + 6);
  });

  let plotW = $derived(Math.max(0, width - PAD.left - PAD.right));
  let plotH = $derived(height - PAD.top - PAD.bottom);
  const x = (t: number) => PAD.left + ((t - t0) / (t1 - t0)) * plotW;
  const y = (bpm: number) => PAD.top + (1 - (bpm - lo) / (hi - lo)) * plotH;

  function zoneOf(bpm: number): number {
    for (let i = 4; i >= 0; i--) if (bpm >= summary.zones[i]!) return i + 1;
    return 0;
  }

  // Split the trace into runs of one zone, so color follows effort. Each run
  // shares its first point with the previous run's last, keeping the line whole.
  let runs = $derived.by(() => {
    const out: { zone: number; d: string }[] = [];
    let cur: { zone: number; pts: string[] } | null = null;
    for (const h of visible) {
      const z = zoneOf(h.bpm);
      const p = `${x(h.t).toFixed(1)},${y(h.bpm).toFixed(1)}`;
      if (!cur || cur.zone !== z) {
        if (cur) {
          cur.pts.push(p);
          out.push({ zone: cur.zone, d: 'M' + cur.pts.join('L') });
        }
        cur = { zone: z, pts: [p] };
      } else cur.pts.push(p);
    }
    if (cur) out.push({ zone: cur.zone, d: 'M' + cur.pts.join('L') });
    return out;
  });

  let zoneLines = $derived(
    summary.zones.map((bpm, i) => ({ zone: i + 1, bpm })).filter((z) => z.bpm > lo + 4 && z.bpm < hi - 2),
  );

  // Lift labels along the foot, truncated to the space before the next lift.
  let lifts = $derived.by(() => {
    const groups: { name: string; from: number }[] = [];
    for (const s of sets) {
      if (!groups.length || groups.at(-1)!.name !== s.exercise_name) groups.push({ name: s.exercise_name, from: s.started_at });
    }
    return groups.map((g, i) => {
      const left = x(g.from);
      const right = i + 1 < groups.length ? x(groups[i + 1]!.from) : PAD.left + plotW;
      const room = Math.max(0, right - left - 8);
      const maxChars = Math.floor(room / 6.6);
      const label = g.name.length <= maxChars ? g.name : maxChars >= 4 ? g.name.slice(0, maxChars - 1).trimEnd() + '…' : '';
      return { left, label, full: g.name };
    });
  });

  // Hit-testing: a set owns its band plus the rest after it.
  function pick(clientX: number, rect: DOMRect) {
    const px = clientX - rect.left;
    const t = t0 + ((px - PAD.left) / plotW) * (t1 - t0);
    let best: string | null = null;
    for (let i = 0; i < sets.length; i++) {
      const s = sets[i]!;
      const until = sets[i + 1]?.started_at ?? t1;
      if (t >= s.started_at - 20_000 && t < until - 20_000) best = s.id;
    }
    if (!best && sets.length) best = sets[0]!.id;
    if (best && best !== selected) onselect(best);
  }

  const onmove = (e: PointerEvent) => pick(e.clientX, (e.currentTarget as SVGElement).getBoundingClientRect());

  let label = $derived(
    summary.hr_status === 'ready'
      ? `Heart rate across the session, peaking at ${summary.peak_bpm} bpm. ${sets.length} sets shown as shaded bands. The table below lists each set.`
      : `${sets.length} sets shown as bands. Heart rate not available yet.`,
  );
</script>

<div class="trace" bind:clientWidth={width}>
  {#if width > 0}
    <svg {width} {height} role="img" aria-label={label} onpointermove={onmove} onpointerdown={onmove}>
      <!-- zone guides -->
      {#each zoneLines as z (z.zone)}
        <line class="guide" x1={PAD.left} x2={PAD.left + plotW} y1={y(z.bpm)} y2={y(z.bpm)} />
        <text class="guide-label" x={PAD.left + plotW + 6} y={y(z.bpm) + 4}>Z{z.zone}</text>
      {/each}

      <!-- set bands: the climbs -->
      {#each sets as s, i (s.id)}
        {@const bx = x(s.started_at)}
        {@const bw = Math.max(2, x(s.ended_at) - bx)}
        {@const prevX = i > 0 ? x(sets[i - 1]!.started_at) : -Infinity}
        <rect class="band" class:active={s.id === selected} x={bx} y={PAD.top} width={bw} height={plotH} />
        {#if s.id === selected}
          <rect class="band-cap" x={bx} y={PAD.top - 3} width={bw} height="3" />
        {/if}
        {#if bx - prevX >= 16 || s.id === selected}
          <text class="set-n" class:active={s.id === selected} x={bx + bw / 2} y={PAD.top - 8}>{s.set_index + 1}</text>
        {/if}
      {/each}

      <!-- the trace -->
      {#each runs as r, i (i)}
        <path class="hr" d={r.d} style:stroke={`var(--zone-${Math.max(1, r.zone)})`} />
      {/each}

      <!-- the foot: baseline and lift names -->
      <line class="foot" x1={PAD.left} x2={PAD.left + plotW} y1={PAD.top + plotH} y2={PAD.top + plotH} />
      {#each lifts as l (l.left)}
        <line class="tick" x1={l.left} x2={l.left} y1={PAD.top + plotH} y2={PAD.top + plotH + 6} />
        {#if l.label}
          <text class="lift" x={l.left + 3} y={PAD.top + plotH + 20}><title>{l.full}</title>{l.label}</text>
        {/if}
      {/each}
    </svg>
  {/if}
  {#if summary.hr_status !== 'ready'}
    <div class="pending">
      <div class="pending-card">
        <p>Heart rate arrives after Whoop syncs.</p>
        <p class="muted">Checking every 10 seconds.</p>
      </div>
    </div>
  {/if}
</div>

<style>
  .trace {
    position: relative;
    touch-action: pan-y;
  }
  svg {
    display: block;
    overflow: visible;
    cursor: crosshair;
    user-select: none;
    -webkit-user-select: none;
  }
  .guide {
    stroke: var(--line);
    stroke-dasharray: 2 4;
  }
  .guide-label,
  .set-n,
  .lift {
    font-family: var(--font-mono);
    font-size: 12px;
    fill: var(--text-muted);
  }
  .lift {
    font-family: var(--font-ui);
    font-weight: 600;
  }
  .set-n {
    text-anchor: middle;
  }
  .set-n.active {
    fill: var(--text);
    font-weight: 500;
  }
  .band {
    fill: var(--band);
    transition: fill var(--dur-state) var(--ease-out);
  }
  .band.active {
    fill: var(--band-active);
  }
  .band-cap {
    fill: var(--text);
  }
  .hr {
    fill: none;
    stroke-width: 2;
    stroke-linejoin: round;
    stroke-linecap: round;
  }
  .foot,
  .tick {
    stroke: var(--line-strong);
  }
  .pending {
    position: absolute;
    inset: 0 0 30px;
    display: grid;
    place-content: center;
    gap: var(--space-1);
    text-align: center;
    padding: var(--space-4);
    font-size: var(--size-small);
  }
  .pending-card {
    display: grid;
    gap: var(--space-1);
    padding: var(--space-3) var(--space-4);
    background: var(--bg);
    border: 1px solid var(--line);
    border-radius: var(--radius-m);
  }
  .pending p:first-child {
    font-weight: 600;
    font-size: var(--size-body);
  }
</style>
