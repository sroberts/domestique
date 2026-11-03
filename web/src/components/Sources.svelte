<script lang="ts">
  import { SOURCE_LABEL } from '../lib/format';
  let { sources }: { sources: { source: string }[] } = $props();
  let names = $derived([...new Set(sources.map((s) => SOURCE_LABEL[s.source] ?? s.source))]);
</script>

<span class="sources" aria-label={`Recorded by ${names.join(', ')}`}>
  {#each names as n (n)}<span class="src mono">{n}</span>{/each}
</span>

<style>
  .sources {
    display: inline-flex;
    flex-wrap: wrap;
    gap: var(--space-1);
  }
  .src {
    padding: 0 0.375rem;
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-s);
    font-size: var(--size-label);
    line-height: 1.25rem;
    color: var(--text-muted);
  }
</style>
