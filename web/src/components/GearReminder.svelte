<script lang="ts">
  // One compact line on Today when a part or a battery is due.
  import { fetchGear, type Reminder } from '../lib/gear';
  import { link } from '../lib/router.svelte';

  let due = $state<Reminder[]>([]);
  $effect(() => {
    fetchGear().then((r) => (due = r.data?.reminders.filter((x) => x.status === 'due') ?? []));
  });
  let first = $derived(due[0]);
  // "Chain on Tarmac: 3,120 of 3,000 km. Replace": the action becomes the link.
  let text = $derived(first ? first.text.replace(/\s*Replace( soon)?$/, '') : '');
</script>

{#if first}
  <p class="gear-due" role="status">
    <span>{text}</span>
    <a href="/gear" use:link>Replace{#if due.length > 1}<span class="muted">{' · '}{due.length - 1} more</span>{/if}</a>
  </p>
{/if}

<style>
  .gear-due {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0 var(--space-2);
    margin-top: var(--space-5);
    padding: var(--space-2) 0;
    border-top: 1px solid var(--line);
    border-bottom: 1px solid var(--line);
    font-size: var(--size-small);
  }
  a {
    display: inline-flex;
    align-items: center;
    min-height: 2.75rem;
    font-weight: 600;
    text-underline-offset: 0.2em;
  }
</style>
