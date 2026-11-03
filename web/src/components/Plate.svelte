<script lang="ts">
  // A painted race-number plate: the current set number, the one thing you
  // look for from across the gym.
  interface Props {
    n: number;
    of: number;
    active?: boolean;
  }
  let { n, of, active = false }: Props = $props();
</script>

<p class="plate-row">
  <span class="visually-hidden">Set {n} of {of}</span>
  <span class="plate num" class:active aria-hidden="true">{n}</span>
  <span class="num total" aria-hidden="true">of {of}</span>
</p>

<style>
  .plate-row {
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }
  .plate {
    display: grid;
    place-items: center;
    min-width: 4.5rem;
    height: 4.5rem;
    padding: 0 var(--space-3);
    background: var(--plate);
    color: var(--on-plate);
    font-size: 3.5rem;
    font-weight: 700;
    border-radius: var(--radius-s);
    /* safety-pin holes, like a number pinned to a jersey */
    background-image:
      radial-gradient(circle at 7px 7px, var(--bg) 2px, transparent 2.5px),
      radial-gradient(circle at calc(100% - 7px) 7px, var(--bg) 2px, transparent 2.5px),
      radial-gradient(circle at 7px calc(100% - 7px), var(--bg) 2px, transparent 2.5px),
      radial-gradient(circle at calc(100% - 7px) calc(100% - 7px), var(--bg) 2px, transparent 2.5px);
    transition: box-shadow var(--dur-state) var(--ease-out);
  }
  .plate.active {
    box-shadow: 0 0 0 3px var(--bg), 0 0 0 5px var(--signal);
  }
  .total {
    font-size: var(--size-target);
    font-weight: 600;
    color: var(--text-muted);
  }
</style>
