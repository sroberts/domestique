<script lang="ts">
  // Time in zones as labelled horizontal bars: label, bar, duration.
  import { clock } from '../lib/format';
  interface Row {
    label: string;
    hint?: string;
    seconds: number;
    color: string;
  }
  let { rows }: { rows: Row[] } = $props();
  let max = $derived(Math.max(1, ...rows.map((r) => r.seconds)));
  let total = $derived(rows.reduce((a, r) => a + r.seconds, 0));
</script>

<ul class="zones">
  {#each rows as r (r.label)}
    <li class:empty={r.seconds === 0}>
      <span class="z">{r.label}{#if r.hint}<span class="hint muted">{r.hint}</span>{/if}</span>
      <span class="track">{#if r.seconds > 0}<span class="bar" style:width={`${(r.seconds / max) * 100}%`} style:background={r.color}></span>{/if}</span>
      <span class="mono t">{clock(r.seconds)}</span>
      <span class="mono pct muted">{total ? Math.round((r.seconds / total) * 100) : 0}%</span>
    </li>
  {/each}
</ul>

<style>
  .zones {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: var(--space-2);
  }
  li {
    display: grid;
    grid-template-columns: 7.5rem minmax(0, 1fr) 3.75rem 2.5rem;
    align-items: center;
    gap: var(--space-2);
    font-size: var(--size-small);
  }
  .z {
    font-weight: 600;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .hint {
    font-weight: 400;
    margin-left: 0.375rem;
  }
  .track {
    height: 0.625rem;
  }
  .bar {
    display: block;
    height: 100%;
    min-width: 2px;
    border-radius: 0 3px 3px 0;
  }
  li.empty .t,
  li.empty .pct {
    color: var(--text-muted);
  }
  .t,
  .pct {
    text-align: right;
    font-size: var(--size-label);
  }
</style>
