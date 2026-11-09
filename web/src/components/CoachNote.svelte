<script lang="ts">
  // A note written by Outpost's LLM in the directeur sportif's voice. Quiet:
  // body text on a raised surface, labelled so it's never mistaken for a
  // measured number.
  let { text, label = 'From the team car' }: { text: string; label?: string } = $props();
</script>

<aside class="coach" aria-label={label}>
  <p class="label who">{label}</p>
  <p class="text">{text}</p>
</aside>

<style>
  .coach {
    margin-top: var(--space-4);
    padding: var(--space-3) var(--space-4);
    background: var(--surface-raised);
    border: 1px solid var(--line);
    border-radius: var(--radius-m);
    max-width: 68ch;
  }
  .who {
    margin: 0 0 var(--space-1);
    color: var(--text-muted);
  }
  .text {
    margin: 0;
    text-wrap: pretty;
  }
</style>
