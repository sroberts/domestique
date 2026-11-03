<script lang="ts">
  import { clock } from '../lib/format';

  let { seconds }: { seconds: number[] } = $props();

  let total = $derived(seconds.reduce((a, b) => a + b, 0));
  let rows = $derived(
    seconds
      .map((s, i) => ({ zone: i, s, pct: total ? (s / total) * 100 : 0 }))
      .filter((r) => r.zone > 0 || r.s > 0),
  );
</script>

<div class="zones">
  <div class="bar" aria-hidden="true">
    {#each rows as r (r.zone)}
      {#if r.pct > 0}
        <span style:width={`${r.pct}%`} style:background={r.zone === 0 ? 'var(--line-strong)' : `var(--zone-${r.zone})`}></span>
      {/if}
    {/each}
  </div>
  <dl class="legend">
    {#each rows as r (r.zone)}
      <div class:empty={r.s === 0}>
        <dt>
          <span class="sw" style:background={r.zone === 0 ? 'var(--line-strong)' : `var(--zone-${r.zone})`} aria-hidden="true"></span>
          {#if r.zone === 0}<span aria-hidden="true">&lt;Z1</span><span class="visually-hidden">Below zone 1</span>{:else}Z{r.zone}{/if}
        </dt>
        <dd class="mono">{clock(r.s)}</dd>
      </div>
    {/each}
  </dl>
</div>

<style>
  .bar {
    display: flex;
    height: 0.75rem;
    gap: 2px;
    border-radius: 2px;
    overflow: hidden;
  }
  .legend {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: var(--space-2) var(--space-5);
    margin: var(--space-3) 0 0;
  }
  @media (min-width: 640px) {
    .legend {
      grid-template-columns: repeat(6, minmax(0, 1fr));
    }
  }
  .legend div {
    white-space: nowrap;
    display: flex;
    justify-content: space-between;
    gap: var(--space-2);
    font-size: var(--size-small);
  }
  .legend .empty {
    color: var(--text-muted);
  }
  dt {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    font-weight: 600;
  }
  dd {
    margin: 0;
  }
  .sw {
    width: 0.625rem;
    height: 0.625rem;
    border-radius: 2px;
  }
</style>
