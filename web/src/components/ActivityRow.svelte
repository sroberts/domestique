<script lang="ts">
  import SportIcon from './SportIcon.svelte';
  import { duration, km, relDay, time } from '../lib/format';
  import { link } from '../lib/router.svelte';
  import { earlyText, endedEarly } from '../lib/workout.svelte';
  import type { ActivityRow } from '../lib/types';

  let { a, showDay = true }: { a: ActivityRow; showDay?: boolean } = $props();
  let href = $derived(a.session_id ? `/session/${a.session_id}/summary` : `/activity/${a.id}`);
  let isRide = $derived(a.sport === 'ride' || a.sport === 'virtual_ride');
  // The sport's colour, painted on a solid block behind its icon.
  let kind = $derived(isRide ? 'ride' : a.sport === 'strength' ? 'strength' : 'other');
  // Effort at a glance: a bar under the TSS, full at 150 (a hard long ride).
  let effort = $derived(Math.min(1, a.tss / 150));
  let meta = $derived(
    [
      showDay ? relDay(a.start_at) : '',
      time(a.start_at),
      duration(a.moving_s || a.elapsed_s),
      a.distance_m > 0 && a.sport !== 'strength' ? `${km(a.distance_m)} km` : '',
    ]
      .filter(Boolean)
      // Each field stays whole; only the separators may break.
      .map((f) => f.replaceAll(' ', '\u00a0'))
      .join(' · '),
  );
</script>

<a class="row" {href} use:link>
  <span class="icon sport-{kind}"><SportIcon sport={a.sport} /></span>
  <span class="main">
    <span class="name">{a.name}</span>
    <span class="meta mono">{meta}</span>
    {#if endedEarly(a.progress)}
      <span class="early">{earlyText(a.progress!)}</span>
    {/if}
  </span>
  <span class="figs">
    {#if a.tss > 0}
      <span class="fig">
        <span class="num">{Math.round(a.tss)}</span><span class="label">TSS</span>
        <span class="effort sport-{kind}" style:--e={effort} aria-hidden="true"></span>
      </span>
    {/if}
    {#if isRide && a.summary.np > 0}
      <span class="fig"><span class="num">{a.summary.np}</span><span class="label">NP</span></span>
    {:else if a.summary.avg_hr > 0}
      <span class="fig"><span class="num">{a.summary.avg_hr}</span><span class="label">Avg HR</span></span>
    {:else if a.strain != null}
      <span class="fig"><span class="num">{a.strain.toFixed(1)}</span><span class="label">Strain</span></span>
    {/if}
  </span>
</a>

<style>
  .row {
    display: grid;
    grid-template-columns: 2.25rem minmax(0, 1fr) auto;
    gap: var(--space-3);
    align-items: start;
    padding: var(--space-3) 0;
    border-bottom: 1px solid var(--line);
    text-decoration: none;
  }
  .row:hover .name {
    text-decoration: underline;
    text-underline-offset: 3px;
  }
  .icon {
    display: grid;
    place-items: center;
    width: 2.25rem;
    height: 2.25rem;
    border-radius: var(--radius-s);
    color: var(--on-sport);
    background: var(--sport-other);
  }
  .icon.sport-ride {
    background: var(--sport-ride);
  }
  .icon.sport-strength {
    background: var(--sport-strength);
  }
  .effort.sport-ride {
    --c: var(--sport-ride);
  }
  .effort.sport-strength {
    --c: var(--sport-strength);
  }
  .effort {
    display: block;
    width: 100%;
    height: 3px;
    margin-top: 0.125rem;
    border-radius: 1px;
    /* the bar's length is the effort, filled from the right over a faint
       track so a small number still shows its scale */
    --c: var(--sport-other);
    background: linear-gradient(to left, var(--c) calc(var(--e) * 100%), var(--line-strong) 0);
  }
  .main {
    display: grid;
    gap: 0.25rem;
    min-width: 0;
  }
  .name {
    font-weight: 600;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .meta {
    font-size: var(--size-label);
    color: var(--text-muted);
  }
  .early {
    justify-self: start;
    padding: 0 var(--space-2);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-s);
    font-size: var(--size-label);
    font-weight: 600;
    color: var(--text-muted);
  }
  .figs {
    display: flex;
    gap: var(--space-4);
    text-align: right;
  }
  .fig {
    display: grid;
    justify-items: end;
    gap: 0.125rem;
  }
  .fig .num {
    font-size: var(--size-heading);
  }
  .fig .label {
    font-size: var(--size-label);
    letter-spacing: 0.04em;
  }
</style>
