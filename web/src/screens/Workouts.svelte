<script lang="ts">
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import ActionBar from '../components/ActionBar.svelte';
  import SyncStatus from '../components/SyncStatus.svelte';
  import { AuthError, OfflineError, localDate } from '../lib/api';
  import { activeSession } from '../lib/db';
  import { relDay, time } from '../lib/format';
  import { link, router } from '../lib/router.svelte';
  import { clearToday, fetchPlan, fetchTemplates, planToday, setCount, startTemplate, type TemplateRow } from '../lib/templates';
  import { estimateMinutes } from '../lib/workout.svelte';
  import type { LocalSession } from '../lib/types';

  const date = localDate();
  let rows = $state<TemplateRow[]>([]);
  let planId = $state<string | null>(null);
  let loading = $state(true);
  let stale = $state<number | null>(null);
  let failed = $state(false);
  let auth = $state(false);
  let active = $state<LocalSession | null>(null);
  let busy = $state<string | null>(null);
  let status = $state('');
  let statusError = $state(false);

  // A message left by the editor ("Saved Push.").
  try {
    const flash = sessionStorage.getItem('domestique:flash');
    if (flash) {
      status = flash;
      sessionStorage.removeItem('domestique:flash');
    }
  } catch {
    /* ignore */
  }

  $effect(() => {
    void load();
  });

  async function load() {
    active = (await activeSession()) ?? null;
    const [t, p] = await Promise.all([fetchTemplates(), fetchPlan(date)]);
    if (t.data) rows = t.data;
    planId = p.data?.template_id ?? null;
    stale = t.stale;
    failed = t.error;
    auth = t.auth;
    loading = false;
  }

  function say(msg: string, error = false) {
    status = msg;
    statusError = error;
  }

  function failText(err: unknown): string {
    if (err instanceof OfflineError) return "Couldn't reach the Mac Mini. Try again when you're connected.";
    if (err instanceof AuthError) return 'Your Outpost sign-in has expired. Reload to sign in again.';
    return err instanceof Error ? err.message : 'Something went wrong.';
  }

  async function start(t: TemplateRow) {
    if (busy) return;
    busy = `start:${t.id}`;
    const s = await startTemplate(t);
    router.go(`/session/${s.id}`);
  }

  async function makePlan(t: TemplateRow) {
    if (busy) return;
    busy = `plan:${t.id}`;
    try {
      await planToday(t.id);
      planId = t.id;
      say(`${t.name} is today's plan.`);
      void fetchPlan(date); // refresh the offline copy
    } catch (err) {
      say(failText(err), true);
    } finally {
      busy = null;
    }
  }

  async function clearPlan(t: TemplateRow) {
    if (busy) return;
    busy = `plan:${t.id}`;
    try {
      await clearToday();
      const p = await fetchPlan(date);
      planId = p.data?.template_id ?? null;
      say(planId ? `Today's plan is back to ${rows.find((r) => r.id === planId)?.name ?? 'the default'}.` : 'Today is a rest day now.');
    } catch (err) {
      say(failText(err), true);
    } finally {
      busy = null;
    }
  }

  function lastDone(at: number | null): string {
    if (!at) return 'Not done yet';
    const d = relDay(at);
    return `Last done ${d === 'Today' || d === 'Yesterday' ? d.toLowerCase() : d}`;
  }

  // Today's plan first, then the rest by name (the server's order).
  let sorted = $derived([...rows].sort((a, b) => Number(b.id === planId) - Number(a.id === planId)));
</script>

<main class="page">
  <header class="top">
    <a class="back" href="/" use:link><ChevronLeft size={20} aria-hidden="true" />Today</a>
    <SyncStatus />
  </header>
  <h1>Workouts</h1>

  {#if stale}
    <p class="notice">No connection. Showing workouts as of {time(stale)}. You can start one; editing needs the Mac Mini.</p>
  {:else if auth}
    <div class="notice">
      <p>Your Outpost sign-in has expired.</p>
      <button class="btn" onclick={() => location.reload()}>Sign in again</button>
    </div>
  {/if}

  {#if active}
    <div class="notice">
      <p>{active.name} is in progress. Finish it before starting another.</p>
      <a class="btn" href={`/session/${active.id}`} use:link>Resume</a>
    </div>
  {/if}

  <p class="status" class:error={statusError} role="status">{status}</p>

  {#if !loading && rows.length === 0}
    <section class="empty">
      {#if failed}
        <h2>No connection</h2>
        <p class="muted">Domestique can't reach the Mac Mini, and no workouts are saved on this device yet.</p>
        <button class="btn" onclick={() => load()}>Try again</button>
      {:else}
        <h2>No workouts yet</h2>
        <p class="muted">Make one here, or ask Claude to write one.</p>
      {/if}
    </section>
  {/if}

  <ul class="list">
    {#each sorted as t (t.id)}
      {@const isPlan = t.id === planId}
      <li class="row" aria-labelledby={`w-${t.id}`}>
        <div class="info">
        <div class="row-head">
          <h2 id={`w-${t.id}`}>{t.name}</h2>
          {#if isPlan}<span class="tag">Today's plan</span>{/if}
        </div>
        <p class="meta">
          <span class="mono">{t.exercises.length} {t.exercises.length === 1 ? 'lift' : 'lifts'} · {setCount(t.exercises)} sets · about {estimateMinutes(t.exercises)} min</span>
        </p>
        <p class="lifts muted">{lastDone(t.last_done_at)}</p>
        <p class="lifts muted">{t.exercises.map((e) => e.name).join(', ')}</p>
        </div>
        <div class="actions">
          <button class="btn" onclick={() => start(t)} disabled={!!busy || !!active} aria-label={`Start ${t.name} now`}>
            {busy === `start:${t.id}` ? 'Starting…' : 'Start now'}
          </button>
          {#if isPlan}
            <button class="btn btn-quiet" onclick={() => clearPlan(t)} disabled={!!busy} aria-label={`Clear ${t.name} as today's plan`}>Clear today's plan</button>
          {:else}
            <button class="btn" onclick={() => makePlan(t)} disabled={!!busy} aria-label={`Make ${t.name} today's plan`}>Make today's plan</button>
          {/if}
          <a class="btn btn-quiet" href={`/workouts/${t.id}`} use:link aria-label={`Edit ${t.name}`}>Edit</a>
        </div>
      </li>
    {/each}
  </ul>
</main>

<ActionBar>
  <a class="btn btn-primary" href="/workouts/new" use:link>New workout</a>
</ActionBar>

<style>
  .page {
    max-width: var(--page-list);
    margin: 0 auto;
    padding: calc(env(safe-area-inset-top) + var(--space-2)) var(--gutter) 0;
  }
  .top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    min-height: 2.75rem;
  }
  .back {
    display: inline-flex;
    align-items: center;
    gap: var(--space-1);
    min-height: 2.75rem;
    margin-left: calc(var(--space-2) * -1);
    padding: 0 var(--space-2);
    color: var(--text-muted);
    font-weight: 600;
    text-decoration: none;
  }
  .back:hover {
    color: var(--text);
  }
  h1 {
    font-size: var(--size-heading);
    margin-top: var(--space-2);
  }
  .notice {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    margin-top: var(--space-4);
    padding: var(--space-3) var(--space-4);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    font-size: var(--size-small);
  }
  .status {
    margin-top: var(--space-3);
    font-size: var(--size-small);
    min-height: 1.3em;
  }
  .status.error {
    color: var(--danger);
  }
  .empty {
    display: grid;
    gap: var(--space-3);
    justify-items: start;
    margin-top: var(--space-6);
  }
  .empty h2 {
    font-size: var(--size-heading);
  }
  .list {
    list-style: none;
    margin: var(--space-2) 0 0;
    padding: 0;
    border-top: 1px solid var(--line-strong);
  }
  .row {
    padding: var(--space-4) 0;
    border-bottom: 1px solid var(--line);
  }
  .row-head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-2) var(--space-3);
  }
  .row h2 {
    font-size: var(--size-lead);
  }
  .tag {
    font-size: var(--size-small);
    font-weight: 600;
    padding: 0 var(--space-2);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-s);
  }
  .meta {
    margin-top: var(--space-1);
    font-size: var(--size-small);
  }
  .meta .mono {
    color: var(--text-muted);
  }
  .lifts {
    margin-top: var(--space-1);
    font-size: var(--size-small);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin-top: var(--space-3);
  }
  @media (min-width: 900px) {
    .page {
      max-width: var(--page-wide);
    }
    .row {
      display: grid;
      grid-template-columns: minmax(0, 1fr) auto;
      align-items: center;
      column-gap: var(--space-6);
    }
    .actions {
      margin-top: 0;
      justify-content: flex-end;
    }
  }
</style>
