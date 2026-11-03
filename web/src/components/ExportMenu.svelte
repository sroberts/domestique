<script lang="ts">
  import Download from '@lucide/svelte/icons/download';
  import type { ExportItem } from '../lib/export';

  let { items }: { items: ExportItem[] } = $props();
  let el = $state<HTMLDetailsElement | null>(null);

  // Close on a tap elsewhere or Escape, like any menu.
  $effect(() => {
    const onDown = (e: PointerEvent) => {
      if (el?.open && !el.contains(e.target as Node)) el.open = false;
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && el?.open) {
        el.open = false;
        el.querySelector('summary')?.focus();
      }
    };
    document.addEventListener('pointerdown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('pointerdown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  });
</script>

{#if items.length}
  <details class="export" bind:this={el}>
    <summary class="btn btn-quiet"><Download size={18} aria-hidden="true" /> Export</summary>
    <ul class="menu">
      {#each items as it (it.href)}
        <li>
          <a href={it.href} download onclick={() => el && (el.open = false)}>
            <span class="m-label">{it.label}</span>
            <span class="m-hint">{it.hint}</span>
          </a>
        </li>
      {/each}
    </ul>
  </details>
{/if}

<style>
  .export {
    position: relative;
  }
  summary {
    list-style: none;
  }
  summary::-webkit-details-marker {
    display: none;
  }
  .export[open] summary {
    color: var(--text);
  }
  .menu {
    position: absolute;
    right: 0;
    top: calc(100% + var(--space-1));
    z-index: var(--z-dialog);
    width: min(18rem, calc(100vw - 2 * var(--gutter)));
    margin: 0;
    padding: var(--space-1) 0;
    list-style: none;
    background: var(--surface-raised);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    box-shadow: 0 8px 24px rgb(0 0 0 / 0.25);
  }
  .menu a {
    display: grid;
    gap: 0.125rem;
    min-height: 2.75rem;
    padding: var(--space-2) var(--space-4);
    text-decoration: none;
  }
  .menu a:hover,
  .menu a:focus-visible {
    background: var(--band);
  }
  .menu li + li a {
    border-top: 1px solid var(--line);
  }
  .m-label {
    font-weight: 600;
  }
  .m-hint {
    font-size: var(--size-label);
    color: var(--text-muted);
  }
</style>
