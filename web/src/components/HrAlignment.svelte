<script lang="ts">
  // How heart rate was lined up with the sets, and a way to nudge it. The
  // phone's clock and the strap's clock can disagree; the server estimates
  // the strap's offset from the data, and a hand correction overrides it.
  import { putJSON } from '../lib/api';
  import type { Alignment } from '../lib/types';

  interface Props {
    sessionId: string;
    alignment: Alignment;
    /** Called after a change is saved, to fetch the re-aligned summary. */
    onchange: () => Promise<void> | void;
  }
  let { sessionId, alignment, onchange }: Props = $props();

  let open = $state(false);
  let busy = $state(false);
  let error = $state('');

  const secs = (ms: number) => Math.round(Math.abs(ms) / 1000);
  let text = $derived.by(() => {
    const s = secs(alignment.hr_shift_ms);
    const dir = alignment.hr_shift_ms > 0 ? 'earlier' : 'later';
    if (alignment.source === 'manual') return s ? `Heart rate moved ${s} s ${dir} by hand.` : 'Heart rate left as recorded, set by hand.';
    if (alignment.source === 'auto') return s ? `Heart rate moved ${s} s ${dir} to line up with your sets.` : 'Heart rate lines up with your sets.';
    return "Couldn't check the timing from this session's heart rate.";
  });

  async function save(shift: number | null) {
    busy = true;
    error = '';
    try {
      await putJSON(`/v1/sessions/${encodeURIComponent(sessionId)}/hr-shift`, { shift_ms: shift });
      await onchange();
    } catch {
      error = "Couldn't save. This needs the Mac Mini.";
    } finally {
      busy = false;
    }
  }
  // Move heart rate by a step from wherever it is now.
  const nudge = (ms: number) => save(alignment.hr_shift_ms + ms);
</script>

<div class="align">
  <p class="muted small">
    {text}
    <button class="link" aria-expanded={open} onclick={() => (open = !open)}>{open ? 'Done' : 'Adjust'}</button>
  </p>
  {#if open}
    <div class="controls" role="group" aria-label="Move heart rate against the sets">
      <button class="btn" disabled={busy} onclick={() => nudge(5000)}>5 s earlier</button>
      <button class="btn" disabled={busy} onclick={() => nudge(-5000)}>5 s later</button>
      {#if alignment.source === 'manual'}
        <button class="btn btn-quiet" disabled={busy} onclick={() => save(null)}>Automatic</button>
      {/if}
    </div>
    <p class="muted small hint">Line the climbs up with the shaded sets: heart rate should rise during each set and fall in the rest after it.</p>
    {#if error}<p class="small err" role="alert">{error}</p>{/if}
  {/if}
</div>

<style>
  .align {
    margin-top: var(--space-3);
  }
  .small {
    font-size: var(--size-small);
  }
  .link {
    margin-left: var(--space-1);
    padding: 0.5rem 0.25rem;
    border: 0;
    background: none;
    color: var(--text);
    font: inherit;
    font-weight: 600;
    text-decoration: underline;
    text-underline-offset: 3px;
    cursor: pointer;
  }
  .controls {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-top: var(--space-2);
  }
  .hint {
    margin-top: var(--space-2);
    max-width: 62ch;
  }
  .err {
    margin-top: var(--space-2);
    color: var(--danger);
  }
</style>
