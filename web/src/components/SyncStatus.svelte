<script lang="ts">
  import { sync } from '../lib/sync.svelte';

  const noun = (n: number) => (n === 1 ? '1 set' : `${n} sets`);
  // Sessions and sets share the queue; "sets" is what the athlete cares about.
  let text = $derived.by(() => {
    const n = sync.pending;
    switch (sync.status) {
      case 'auth':
        return n > 0 ? `Sign in to sync ${noun(n)}` : 'Sign in to sync';
      case 'offline':
        return n > 0 ? `Offline · ${noun(n)} waiting` : 'Offline';
      case 'error':
        return n > 0 ? `Retrying · ${noun(n)} waiting` : 'Retrying';
      case 'syncing':
        return n > 0 ? `Syncing ${noun(n)}` : 'Syncing';
      default:
        return n > 0 ? `${noun(n)} waiting` : 'Synced';
    }
  });
</script>

<div class="sync" role="status" aria-live="polite">
  {#if sync.status === 'auth'}
    <!-- A full navigation lets the gateway run its sign-in and bring us back. -->
    <button class="signin" onclick={() => location.reload()}>{text}</button>
  {:else}
    <span class="dot" class:waiting={sync.pending > 0 || sync.status !== 'idle'} aria-hidden="true"></span>
    <span class="mono">{text}</span>
  {/if}
  {#if sync.rejected > 0}
    <span class="mono rejected">· {sync.rejected} refused by server</span>
  {/if}
</div>

<style>
  .sync {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    font-size: var(--size-label);
    color: var(--text-muted);
    min-height: 2.75rem;
  }
  .dot {
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 50%;
    background: var(--zone-3);
    flex: none;
  }
  .dot.waiting {
    background: transparent;
    border: 1.5px solid var(--text-muted);
  }
  .signin {
    min-height: 2.75rem;
    padding: 0 var(--space-3);
    border: 1px solid var(--signal);
    border-radius: var(--radius-m);
    background: transparent;
    color: var(--text);
    font-size: var(--size-small);
    font-weight: 600;
    cursor: pointer;
  }
  .rejected {
    color: var(--danger);
  }
</style>
