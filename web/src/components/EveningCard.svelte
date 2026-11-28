<script lang="ts">
  // The evening log: from 18:00 until noon the next day, one tap per thing
  // that might touch the night. Every tap saves. "Nothing tonight" logs a
  // clear evening, which counts; an evening never logged doesn't.
  import Check from '@lucide/svelte/icons/check';
  import { deleteJSON, getJSON, localDate, putJSON } from '../lib/api';
  import type { Evening } from '../lib/types';

  let { now = new Date() }: { now?: Date } = $props();

  /** The evening the card is about: tonight after 18:00, last night before noon. */
  // svelte-ignore state_referenced_locally
  const hour = now.getHours();
  // svelte-ignore state_referenced_locally
  const date = hour >= 18 ? localDate(now) : hour < 12 ? localDate(new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1)) : '';
  // Up past midnight it's still tonight; by morning it's last night.
  const tonight = hour >= 18 || hour < 4;

  let ev = $state<Evening | null>(null);
  let status = $state<'idle' | 'saving' | 'saved' | 'error'>('idle');

  $effect(() => {
    if (!date) return;
    getJSON<Evening>(`/v1/evenings/${date}`).then(
      (e) => (ev = e),
      () => (ev = null),
    );
  });

  // Every tap saves. Untapping the last item doesn't make the evening clear:
  // it goes back to unknown, since only "Nothing tonight" says nothing happened.
  // Saves go one after another, so a quick untick then tick can't land out
  // of order; a failure rolls back only if no newer tap came since.
  let queue: Promise<void> = Promise.resolve();
  let seq = 0;
  function save(items: string[], clearEvening = false) {
    if (!ev) return;
    const before = $state.snapshot(ev);
    const unlog = items.length === 0 && !clearEvening;
    const mine = ++seq;
    ev = { ...ev, items, logged: !unlog };
    status = 'saving';
    queue = queue.then(async () => {
      try {
        if (unlog) await deleteJSON(`/v1/evenings/${date}`);
        else await putJSON(`/v1/evenings/${date}`, { items });
        if (mine === seq) status = 'saved';
      } catch {
        if (mine === seq) {
          ev = before;
          status = 'error';
        }
      }
    });
  }
  const toggle = (k: string) => save(ev!.items.includes(k) ? ev!.items.filter((x) => x !== k) : [...ev!.items, k]);
  /** What's on record for the evening, in words. */
  let saved = $derived(
    !ev?.logged ? '' : ev.items.length === 0 ? 'Logged: nothing' : `Logged: ${ev.items.map((k) => ev!.choices.find((c) => c.key === k)?.label.toLowerCase() ?? k).join(', ')}`,
  );
  let clear = $derived(!!ev?.logged && ev.items.length === 0);
</script>

{#if date && ev}
  <section class="evening" aria-labelledby="ev-h">
    <div class="head">
      <h2 id="ev-h" class="ev-h">{tonight ? 'Tonight' : 'Last night'}</h2>
      <span class="small status" role="status">
        {#if status === 'saving'}Saving…{:else if status === 'error'}Couldn't save. Check the connection.{:else if ev.logged}{saved}{:else}Not logged{/if}
      </span>
    </div>
    <p class="small muted">Anything that might touch your sleep. Tap what applies.</p>
    <div class="chips">
      {#each ev.choices as c (c.key)}
        <button type="button" class="chip" aria-pressed={ev.items.includes(c.key)} onclick={() => toggle(c.key)}>{#if ev.items.includes(c.key)}<Check size={16} aria-hidden="true" />{/if}{c.label}</button>
      {/each}
    </div>
    <!-- Off while anything is ticked: one stray tap mustn't wipe the evening. -->
    <button type="button" class="chip none" aria-pressed={clear} disabled={ev.items.length > 0} onclick={() => save([], true)}>{#if clear}<Check size={16} aria-hidden="true" />{/if}Nothing {tonight ? 'tonight' : 'last night'}</button>
  </section>
{/if}

<style>
  /* Ruled like the rest of Today, not boxed. */
  .evening {
    margin-top: var(--space-6);
    padding-top: var(--space-4);
    border-top: 1px solid var(--line-strong);
    display: grid;
    gap: var(--space-2);
  }
  .status {
    text-align: right;
  }
  .head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: var(--space-3);
  }
  .ev-h {
    font-size: var(--size-body);
  }
  .small {
    font-size: var(--size-small);
  }
  .status {
    color: var(--text-muted);
  }
  .chips {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-1);
    margin-top: var(--space-1);
  }
  @media (min-width: 640px) {
    .chips {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }
  }
  .chip {
    min-height: var(--tap-min);
    padding: 0 var(--space-2);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    background: var(--bg);
    color: var(--text);
    font: inherit;
    font-weight: 600;
    cursor: pointer;
    transition: background-color var(--dur-state) var(--ease-out);
  }
  /* Pressed is an outline and a check, not a bright fill: this is read in
     the dark, at night. */
  .chip {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: var(--space-1);
  }
  .chip[aria-pressed='true'] {
    background: var(--surface-raised);
    border: 2px solid var(--text);
  }
  .none:disabled {
    opacity: 0.4;
    cursor: default;
  }
  .none {
    margin-top: var(--space-2);
    justify-self: start;
    min-width: 12rem;
    font-weight: 500;
  }
</style>
