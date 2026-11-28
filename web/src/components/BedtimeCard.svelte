<script lang="ts">
  // Tonight's bedtime, from 16:00 until 04:00: when to be in bed to wake at
  // the usual time having slept what tonight is likely to need. Advice only.
  import { getJSON, localDate } from '../lib/api';
  import { hm } from '../lib/format';
  import { link } from '../lib/router.svelte';
  import type { Bedtime } from '../lib/types';

  let { now: start = new Date() }: { now?: Date } = $props();
  // The clock moves on while the card is open: "6h 44 left" mustn't freeze.
  // svelte-ignore state_referenced_locally
  let now = $state(start);
  $effect(() => {
    const t = setInterval(() => (now = new Date()), 60_000);
    return () => clearInterval(t);
  });
  // svelte-ignore state_referenced_locally
  const hour = start.getHours();
  // svelte-ignore state_referenced_locally
  const date = hour >= 16 ? localDate(start) : hour < 4 ? localDate(new Date(start.getFullYear(), start.getMonth(), start.getDate() - 1)) : '';

  let b = $state<Bedtime | null>(null);
  /** Minutes since the bedtime, once it has gone by (before 04:00). */
  let late = $derived.by(() => {
    if (!b) return 0;
    const [h, m] = b.bed_by.split(':').map(Number) as [number, number];
    const eve = new Date(`${b.date}T00:00`);
    const bed = new Date(eve.getFullYear(), eve.getMonth(), eve.getDate() + (h < 12 ? 1 : 0), h, m);
    return Math.max(0, Math.round((now.getTime() - bed.getTime()) / 60000));
  });
  /** Seconds from now until the wake time. */
  let left = $derived.by(() => {
    if (!b) return 0;
    const [h, m] = b.wake.split(':').map(Number) as [number, number];
    const eve = new Date(`${b.date}T00:00`);
    const wake = new Date(eve.getFullYear(), eve.getMonth(), eve.getDate() + 1, h, m);
    return Math.max(0, Math.round((wake.getTime() - now.getTime()) / 1000));
  });
  $effect(() => {
    if (!date) return;
    getJSON<Bedtime | null>(`/v1/bedtime?date=${date}`).then(
      (x) => (b = x),
      () => (b = null),
    );
  });
</script>

{#if b}
  <section class="bedtime" aria-labelledby="bed-h">
    <h2 id="bed-h" class="bed-h">Bedtime</h2>
    {#if late > 0}
      <!-- Past the target: what's left, said plainly, not the time that's gone. -->
      <p class="when"><span class="num">{hm(left)}</span> <span class="small muted">left before {b.wake}</span></p>
      <p class="small muted">The target was {b.bed_by}. Every minute in bed from now still counts.</p>
    {:else}
      <p class="label">Bed by</p>
      <p class="when"><span class="num">{b.bed_by}</span> <span class="small muted">to wake at {b.wake}{b.wake_from === 'usual' ? ', your usual' : ''}</span></p>
      <p class="small">{b.why} Tonight's estimate: {hm(b.need_s)} of sleep, {hm(b.in_bed_s)} in bed.</p>
    {/if}
    <a class="small muted more" href="/sleep" use:link>Open sleep</a>
  </section>
{/if}

<style>
  .bedtime {
    margin-top: var(--space-6);
    padding-top: var(--space-4);
    border-top: 1px solid var(--line-strong);
    display: grid;
    gap: var(--space-1);
  }
  .bed-h {
    font-size: var(--size-body);
    margin-bottom: var(--space-1);
  }
  .when {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    column-gap: var(--space-3);
  }
  .when .num {
    font-size: var(--size-target);
    line-height: 0.9;
  }
  .small {
    font-size: var(--size-small);
  }
  .more {
    justify-self: start;
    display: inline-flex;
    align-items: center;
    min-height: 2.75rem;
    text-decoration: underline;
    text-underline-offset: 0.2em;
  }
</style>
