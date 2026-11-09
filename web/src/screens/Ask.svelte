<script module lang="ts">
  import type { Turn } from '../lib/ask';
  // The conversation outlives the screen: going to look at a ride and coming
  // back keeps it, until the page reloads.
  let kept: Turn[] = [];
</script>

<script lang="ts">
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import { tick } from 'svelte';
  import { AuthError, HTTPError, OfflineError } from '../lib/api';
  import { ask, aiAvailable, lookedAt } from '../lib/ask';
  import { backTarget, link } from '../lib/router.svelte';

  const back = backTarget({ href: '/', label: 'Today' });
  const examples = [
    'How has my threshold moved since June?',
    'How did I sleep this week?',
    'Which parts on my bike are due?',
  ];
  /** Turns shown; older ones scroll away but stay in the page. */
  const SHOWN = 10;

  let on = $state<boolean | null>(null);
  let turns = $state<Turn[]>(kept);
  let question = $state('');
  let asking = $state<string | null>(null);
  let error = $state('');
  let box = $state<HTMLTextAreaElement>();

  $effect(() => {
    aiAvailable().then((v) => (on = v));
  });
  $effect(() => {
    kept = turns;
  });

  async function submit(e?: Event) {
    e?.preventDefault();
    const q = question.trim();
    if (!q || asking) return;
    asking = q;
    error = '';
    question = '';
    try {
      const r = await ask(q, turns);
      turns = [...turns, { question: q, answer: r.answer, tools: r.tools, checked: r.checked }].slice(-SHOWN);
    } catch (err) {
      question = q; // give it back to edit or retry
      if (err instanceof OfflineError) error = 'No connection. Your question is still here; ask again when you’re back online.';
      else if (err instanceof AuthError) error = 'Signed out. Reload the page to sign in, then ask again.';
      else if (err instanceof HTTPError && err.status === 503) error = 'The AI model isn’t available right now. Try again later.';
      else if (err instanceof HTTPError && err.status === 504) error = 'That took too long. Try a narrower question, like one week or one ride.';
      else error = 'Something went wrong getting an answer. Try again.';
    } finally {
      asking = null;
      await tick();
      box?.focus();
    }
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) submit(e);
  }

  function useExample(q: string) {
    question = q;
    box?.focus();
  }

  function clear() {
    turns = [];
    error = '';
    box?.focus();
  }
</script>

<main class="page">
  <header class="top">
    <a class="btn btn-quiet back" href={back.href} use:link><ChevronLeft size={20} aria-hidden="true" /> {back.label}</a>
    {#if turns.length > 0 && !asking}<button class="btn btn-quiet" onclick={clear}>Start over</button>{/if}
  </header>
  <h1>Ask</h1>

  {#if on === false}
    <p class="muted intro">Asking needs an AI model from Outpost, and none is available right now.</p>
  {:else if on}
    <p class="muted intro">Questions about your rides, lifting, recovery and gear, answered from your own data.</p>

    {#if turns.length === 0 && !asking}
      <ul class="examples" aria-label="Examples">
        {#each examples as q (q)}
          <li><button class="example" onclick={() => useExample(q)}>{q}</button></li>
        {/each}
      </ul>
    {/if}

    <ol class="turns" aria-label="Conversation">
      {#each turns as t, i (i)}
        <li class="turn">
          <p class="q">{t.question}</p>
          <div class="a" class:unsure={!t.checked}>
            <p class="label who">From the team car</p>
            <p class="text">{t.answer}</p>
            {#if t.tools.length > 0}<p class="looked">Looked at {lookedAt(t.tools)}</p>{/if}
          </div>
        </li>
      {/each}
      {#if asking}
        <li class="turn">
          <p class="q">{asking}</p>
          <div class="a waiting" role="status" aria-live="polite">
            <p class="label who">From the team car</p>
            <p class="text"><span class="pulse" aria-hidden="true"></span>Looking through your data. On a local model this can take a minute or two.</p>
          </div>
        </li>
      {/if}
    </ol>

    <form class="ask-form" onsubmit={submit}>
      <label class="visually-hidden" for="ask-q">Your question</label>
      <textarea
        id="ask-q"
        class="text"
        rows="2"
        maxlength="500"
        bind:this={box}
        bind:value={question}
        onkeydown={onKey}
        placeholder={turns.length ? 'Ask a follow-up' : 'Ask about your training'}
        disabled={!!asking}
        aria-describedby={error ? 'ask-err' : undefined}
      ></textarea>
      <button class="btn btn-primary" type="submit" disabled={!!asking || !question.trim()} aria-busy={!!asking}>
        {asking ? 'Asking…' : 'Ask'}
      </button>
      {#if error}<p id="ask-err" class="err" role="alert">{error}</p>{/if}
    </form>
  {/if}
</main>

<style>
  .page {
    max-width: var(--page-narrow);
    margin: 0 auto;
    padding: calc(env(safe-area-inset-top) + var(--space-2)) var(--gutter) 0;
  }
  .top {
    display: flex;
    justify-content: space-between;
    align-items: center;
    min-height: 2.75rem;
  }
  .back {
    margin-left: calc(var(--space-2) * -1);
  }
  h1 {
    font-size: var(--size-heading);
    margin-top: var(--space-2);
  }
  .intro {
    margin-top: var(--space-1);
    max-width: 60ch;
  }
  .examples {
    list-style: none;
    margin: var(--space-5) 0 0;
    padding: 0;
    display: grid;
    gap: var(--space-2);
  }
  .example {
    width: 100%;
    min-height: 2.75rem;
    padding: var(--space-2) var(--space-3);
    text-align: left;
    font: inherit;
    color: var(--text);
    background: transparent;
    border: 1px solid var(--line);
    border-radius: var(--radius-m);
    cursor: pointer;
  }
  .example:hover {
    border-color: var(--line-strong);
  }
  .example:active {
    background: var(--band);
  }
  .turns {
    list-style: none;
    margin: var(--space-5) 0 0;
    padding: 0;
    display: grid;
    gap: var(--space-5);
  }
  .turns:empty {
    display: none;
  }
  .q {
    margin: 0;
    font-weight: 600;
    text-wrap: pretty;
  }
  .a {
    margin-top: var(--space-2);
    padding: var(--space-3) var(--space-4);
    background: var(--surface-raised);
    border: 1px solid var(--line);
    border-radius: var(--radius-m);
  }
  .a.unsure {
    background: transparent;
    border-style: dashed;
  }
  .who {
    margin: 0 0 var(--space-1);
    color: var(--text-muted);
  }
  .text {
    margin: 0;
    white-space: pre-line;
    text-wrap: pretty;
  }
  .looked {
    margin: var(--space-2) 0 0;
    color: var(--text-muted);
    font-size: var(--size-small);
  }
  .waiting .text {
    color: var(--text-muted);
  }
  .pulse {
    display: inline-block;
    width: 0.5rem;
    height: 0.5rem;
    margin-right: var(--space-2);
    border-radius: 50%;
    background: var(--signal);
    vertical-align: 0.1em;
    animation: pulse 1.4s cubic-bezier(0.25, 1, 0.5, 1) infinite alternate;
  }
  @keyframes pulse {
    from {
      opacity: 1;
    }
    to {
      opacity: 0.25;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .pulse {
      animation: none;
    }
  }
  .ask-form {
    display: grid;
    gap: var(--space-3);
    margin-top: var(--space-5);
  }
  .ask-form textarea {
    width: 100%;
    min-height: 3rem;
    padding: var(--space-2) var(--space-3);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    background: var(--surface);
    color: var(--text);
    font: inherit;
    font-size: var(--size-lead);
    resize: vertical;
  }
  .ask-form textarea::placeholder {
    color: var(--text-muted);
  }
  .ask-form textarea:disabled {
    opacity: 0.6;
  }
  .ask-form .btn {
    justify-self: start;
  }
  .err {
    margin: 0;
    color: var(--danger);
    font-size: var(--size-small);
  }
</style>
