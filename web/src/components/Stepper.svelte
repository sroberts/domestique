<script lang="ts">
  import Minus from '@lucide/svelte/icons/minus';
  import Plus from '@lucide/svelte/icons/plus';

  interface Props {
    label: string;
    value: string;
    unit?: string;
    onstep: (dir: -1 | 1) => void;
    canDecrease?: boolean;
  }
  let { label, value, unit = '', onstep, canDecrease = true }: Props = $props();
</script>

<div class="wrap" role="group" aria-label={label}>
  <span class="label head" aria-hidden="true">{unit || label}</span>
  <div class="stepper">
  <button class="step" aria-label={`Fewer ${label.toLowerCase()}`} disabled={!canDecrease} onclick={() => onstep(-1)}>
    <Minus size={24} strokeWidth={2.25} aria-hidden="true" />
  </button>
  <output class="value" aria-live="polite"><span class="num">{value}</span><span class="visually-hidden"> {unit || label}</span></output>
  <button class="step" aria-label={`More ${label.toLowerCase()}`} onclick={() => onstep(1)}>
    <Plus size={24} strokeWidth={2.25} aria-hidden="true" />
  </button>
  </div>
</div>

<style>
  .wrap {
    display: grid;
    gap: var(--space-1);
    min-width: 0;
  }
  .head {
    padding-left: var(--space-1);
  }
  .stepper {
    display: grid;
    grid-template-columns: var(--tap-min) minmax(0, 1fr) var(--tap-min);
    align-items: stretch;
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    min-height: var(--tap-min);
    background: var(--surface);
  }
  .step {
    display: grid;
    place-items: center;
    border: 0;
    background: transparent;
    color: var(--text);
    cursor: pointer;
    touch-action: manipulation;
    transition: background-color var(--dur-state) var(--ease-out);
  }
  .step:active {
    background: var(--band-active);
  }
  .step:disabled {
    color: var(--line-strong);
    cursor: default;
  }
  .step:first-child {
    border-right: 1px solid var(--line);
  }
  .step:last-child {
    border-left: 1px solid var(--line);
  }
  .value {
    display: grid;
    place-items: center;
    min-width: 0;
    padding: var(--space-2) 0;
  }
  .value .num {
    font-size: var(--size-stat);
  }
</style>
