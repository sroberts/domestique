<script lang="ts">
  // Last night in stages, as one bar: awake, light, deep, REM, in the order
  // they stack. Each stage is named with its time below, so colour is never
  // the only key; deep and REM say how they compare with the usual.
  import { hm } from '../lib/format';
  import type { Sleep, SleepTrends } from '../lib/types';

  let { night, norms }: { night: Sleep; norms: SleepTrends['norms'] } = $props();

  let stages = $derived(
    [
      { key: 'awake', label: 'Awake', s: night.awake_s, norm: 0 },
      { key: 'light', label: 'Light', s: night.light_s, norm: 0 },
      { key: 'deep', label: 'Deep', s: night.deep_s, norm: norms.nights >= 7 ? norms.deep_s : 0 },
      { key: 'rem', label: 'REM', s: night.rem_s, norm: norms.nights >= 7 ? norms.rem_s : 0 },
    ].filter((x) => x.s > 0),
  );
  let total = $derived(stages.reduce((n, x) => n + x.s, 0));
  let label = $derived(stages.map((x) => `${x.label} ${hm(x.s)}`).join(', '));
</script>

{#if total > 0}
  <div class="stages">
    <div class="bar" role="img" aria-label={`Last night in stages: ${label}`}>
      {#each stages as x (x.key)}
        <span class="seg" style:flex-grow={x.s} style:background={`var(--sleep-${x.key})`}></span>
      {/each}
    </div>
    <ul class="keys">
      {#each stages as x (x.key)}
        <li>
          <span class="sw" style:background={`var(--sleep-${x.key})`} aria-hidden="true"></span>
          <span class="k">{x.label}</span>
          <span class="num v">{hm(x.s)}</span>
          {#if x.norm}<span class="mono muted norm">usual {hm(x.norm)}</span>{/if}
        </li>
      {/each}
    </ul>
  </div>
{/if}

<style>
  .stages {
    margin-top: var(--space-4);
  }
  .bar {
    display: flex;
    gap: 2px; /* a surface gap between stages */
    height: 1.25rem;
  }
  .seg {
    flex-basis: 0;
    min-width: 3px;
    border-radius: 2px;
  }
  .seg:first-child {
    border-radius: var(--radius-s) 2px 2px var(--radius-s);
  }
  .seg:last-child {
    border-radius: 2px var(--radius-s) var(--radius-s) 2px;
  }
  .keys {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-2) var(--space-4);
    margin: var(--space-3) 0 0;
    padding: 0;
    list-style: none;
  }
  @media (min-width: 640px) {
    .keys {
      grid-template-columns: repeat(4, minmax(0, 1fr));
    }
  }
  li {
    display: grid;
    align-content: start; /* a stage without a "usual" line keeps its figure level */
    grid-template-columns: auto 1fr;
    align-items: baseline;
    column-gap: var(--space-2);
  }
  .sw {
    width: 0.625rem;
    height: 0.625rem;
    border-radius: 2px;
    align-self: center;
  }
  .k {
    font-size: var(--size-small);
    color: var(--text-muted);
  }
  .v {
    grid-column: 2;
    font-size: var(--size-stat);
    line-height: 1;
  }
  .norm {
    grid-column: 2;
    font-size: var(--size-label);
  }
</style>
