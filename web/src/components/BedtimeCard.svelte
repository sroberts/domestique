<script lang="ts">
  // Tonight's bedtime, from 16:00 until 04:00: when to be in bed to wake at
  // the usual time having slept what tonight is likely to need. Advice only.
  import { getJSON, localDate } from '../lib/api';
  import { hm } from '../lib/format';
  import { link } from '../lib/router.svelte';
  import type { Bedtime } from '../lib/types';

  let { now = new Date() }: { now?: Date } = $props();
  // svelte-ignore state_referenced_locally
  const hour = now.getHours();
  // svelte-ignore state_referenced_locally
  const date = hour >= 16 ? localDate(now) : hour < 4 ? localDate(new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1)) : '';

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
    <h2 id="bed-h" class="label">Bed by</h2>
    <p class="when"><span class="num">{b.bed_by}</span> <span class="small muted">to wake at {b.wake}{b.wake_from === 'usual' ? ', your usual' : ''}</span></p>
    {#if late > 0}
      <p class="small">Gone by {hm(late * 60)}. In bed now, that's {hm(left)} before {b.wake}.</p>
    {:else}
      <p class="small">{b.why} About {hm(b.need_s)} of sleep, {hm(b.in_bed_s)} in bed.</p>
    {/if}
    <a class="small muted more" href="/sleep" use:link>Sleep</a>
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
