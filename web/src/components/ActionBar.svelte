<script lang="ts">
  import type { Snippet } from 'svelte';
  import { router } from '../lib/router.svelte';
  let { children }: { children: Snippet } = $props();
</script>

<!-- The one action for this state, in the bottom thumb zone. -->
<div class="spacer" class:tabs={router.chrome} aria-hidden="true"></div>
<div class="bar" class:tabs={router.chrome}>
  <div class="inner">{@render children()}</div>
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
  .inner {
    max-width: 35rem;
    margin: 0 auto;
    display: grid;
    gap: var(--space-2);
  }
</style>
