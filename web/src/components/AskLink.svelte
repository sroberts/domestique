<script lang="ts">
  // The way into Ask, for screen headers. It shows only when Outpost gives
  // Domestique an LLM: with none, there's nothing to ask.
  import MessageCircleQuestion from '@lucide/svelte/icons/message-circle-question-mark';
  import { aiAvailable } from '../lib/ask';
  import { link } from '../lib/router.svelte';

  let on = $state(false);
  $effect(() => {
    aiAvailable().then((v) => (on = v));
  });
</script>

{#if on}
  <a class="ask" href="/ask" use:link>
    <MessageCircleQuestion size={18} strokeWidth={1.75} aria-hidden="true" />
    <span>Ask</span>
  </a>
{/if}

<style>
  .ask {
    display: inline-flex;
    align-items: center;
    gap: var(--space-1);
    min-height: 2.75rem;
    margin-left: auto;
    margin-right: var(--space-3);
    padding: 0 var(--space-2);
    color: var(--text-muted);
    font-size: var(--size-small);
    font-weight: 600;
    text-decoration: none;
    border-radius: var(--radius-m);
  }
  .ask:hover {
    color: var(--text);
  }
  .ask:active {
    background: var(--band);
  }
</style>
