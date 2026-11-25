<script lang="ts">
  import type { Snippet } from 'svelte';
  import { router } from '../lib/router.svelte';
  /** column: on wide screens, sit under the main column of a two-column page
   * (Today) instead of straddling the gap between them. */
  /** session: match the live session's tablet scale. */
  let { children, column = false, session = false }: { children: Snippet; column?: boolean; session?: boolean } = $props();
</script>

<!-- The one action for this state, in the bottom thumb zone. -->
<div class="spacer" class:tabs={router.chrome} class:session aria-hidden="true"></div>
<div class="bar" class:tabs={router.chrome}>
  <div class="inner" class:column class:session><div class="stack">{@render children()}</div></div>
</div>

<style>
  .spacer {
    height: calc(var(--action-height) + var(--space-6) + env(safe-area-inset-bottom));
  }
  .spacer.tabs {
    height: calc(var(--action-height) + var(--space-6) + var(--tabbar-h) + env(safe-area-inset-bottom));
  }
  .bar.tabs {
    bottom: calc(var(--tabbar-h) + env(safe-area-inset-bottom));
    padding-bottom: var(--space-3);
  }
  .bar {
    position: fixed;
    inset: auto 0 0 0;
    z-index: var(--z-action);
    padding: var(--space-3) var(--gutter) calc(var(--space-3) + env(safe-area-inset-bottom));
    background: linear-gradient(to top, var(--bg) 70%, transparent);
  }
  /* The screens pad their column by the gutter; the bar pads itself, so its
     column is the page width less both gutters, or it overhangs the page. */
  .inner {
    max-width: calc(var(--page-narrow) - 2 * var(--gutter));
    margin: 0 auto;
  }
  .stack {
    display: grid;
    gap: var(--space-2);
  }
  @media (min-width: 700px) and (min-height: 700px) {
    .inner.session {
      max-width: calc(46rem - 2 * var(--gutter));
    }
    .spacer.session {
      height: calc(var(--action-height) * 1.25 + var(--space-6) + env(safe-area-inset-bottom));
    }
    .inner.session :global(.btn-primary) {
      min-height: calc(var(--action-height) * 1.25);
      font-size: 1.5rem;
    }
  }
  @media (min-width: 900px) {
    .inner.column {
      max-width: calc(var(--page-wide) - 2 * var(--gutter));
      display: grid;
      grid-template-columns: minmax(0, 1.1fr) minmax(0, 1fr);
      column-gap: var(--space-7);
    }
  }
</style>
