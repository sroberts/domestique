<script lang="ts">
  // The client ID and secret of the developer app Domestique signs in to a
  // provider with. They're saved on the Mac Mini as files only Domestique can
  // read; the secret goes up once and never comes back down.
  import { AuthError, HTTPError, OfflineError, deleteJSON, putJSON } from '../lib/api';
  import type { ProviderStatus } from '../lib/types';

  interface Props {
    p: ProviderStatus;
    base: string;
    onchange: () => Promise<void> | void;
  }
  let { p, base, onchange }: Props = $props();

  const portal: Record<ProviderStatus['provider'], { name: string; url: string; scopes: string }> = {
    whoop: {
      name: 'the WHOOP developer dashboard',
      url: 'https://developer-dashboard.whoop.com',
      scopes: 'offline, read:recovery, read:cycles, read:workout, read:sleep, read:profile, read:body_measurement',
    },
    wahoo: {
      name: 'the Wahoo developer portal',
      url: 'https://developers.wahooligan.com',
      scopes: 'user_read, workouts_read, offline_data',
    },
  };
  const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

  let editing = $state(false);
  let setup = $state(false); // the setup steps opened from "Set up"
  let clientId = $state('');
  let secret = $state('');
  let busy = $state(false);
  let error = $state('');
  let copied = $state(false);

  let name = $derived(cap(p.provider));
  let app = $derived(portal[p.provider]);
  let redirect = $derived(`${base || location.origin}/v1/oauth/${p.provider}/callback`);
  let showForm = $derived((p.credentials_from === '' && setup) || editing);

  function failText(err: unknown): string {
    if (err instanceof AuthError) return 'Sign in again, then save.';
    if (err instanceof OfflineError) return "Couldn't reach the Mac Mini. Nothing was saved.";
    if (err instanceof HTTPError) return err.message.charAt(0).toUpperCase() + err.message.slice(1) + '.';
    return "Couldn't save.";
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      await putJSON(`/v1/connections/${p.provider}/credentials`, { client_id: clientId, client_secret: secret });
      secret = '';
      editing = false;
      setup = false;
      await onchange();
    } catch (err) {
      error = failText(err);
    } finally {
      busy = false;
    }
  }

  async function remove() {
    if (!confirm(`Remove the ${name} app credentials? This also disconnects ${name}. Data already imported stays.`)) return;
    busy = true;
    error = '';
    try {
      await deleteJSON(`/v1/connections/${p.provider}/credentials`);
      await onchange();
    } catch (err) {
      error = failText(err);
    } finally {
      busy = false;
    }
  }

  function startEdit() {
    clientId = p.client_id ?? '';
    secret = '';
    error = '';
    editing = true;
  }

  async function copyRedirect() {
    try {
      await navigator.clipboard.writeText(redirect);
      copied = true;
      setTimeout(() => (copied = false), 2000);
    } catch {
      /* clipboard blocked: the address is on screen to copy by hand */
    }
  }
</script>

{#if p.credentials_from === 'manifest'}
  <p class="muted small">App credentials are set in the Outpost manifest. Change them there.</p>
{:else if showForm}
  <div class="creds">
    {#if !editing}
      <p class="small">To connect {name}, Domestique needs a {name} developer app of your own:</p>
      <ol class="steps small">
        <li>
          In <a href={app.url} target="_blank" rel="noopener noreferrer">{app.name}</a>,
          create an app with the scopes <span class="mono">{app.scopes}</span>.
        </li>
        <li>
          Set its redirect URI to this address
          <button type="button" class="link" onclick={copyRedirect}>{copied ? 'Copied' : 'Copy'}</button>
          <code class="mono uri">{redirect}</code>
        </li>
        <li>Paste the app's client ID and secret here.</li>
      </ol>
    {/if}
    <form onsubmit={save}>
      <label>
        <span class="lbl">Client ID</span>
        <input
          bind:value={clientId}
          required
          maxlength="512"
          autocomplete="off"
          autocapitalize="off"
          spellcheck="false"
          name={`${p.provider}-client-id`}
        />
      </label>
      <label>
        <span class="lbl">Client secret</span>
        <input
          type="password"
          bind:value={secret}
          required
          maxlength="512"
          autocomplete="new-password"
          autocapitalize="off"
          spellcheck="false"
          name={`${p.provider}-client-secret`}
        />
      </label>
      <p class="muted small note">
        Saved on the Mac Mini in a file only Domestique can read. The secret is never shown again.
      </p>
      <div class="act">
        <button class="btn btn-go" type="submit" disabled={busy}>{busy ? 'Saving…' : 'Save'}</button>
        <button class="btn btn-quiet" type="button" disabled={busy} onclick={() => ((editing = false), (setup = false), (error = ''))}>Cancel</button>
      </div>
      {#if error}<p class="small err" role="alert">{error}</p>{/if}
    </form>
  </div>
{:else if p.credentials_from === ''}
  <div class="act">
    <button class="btn btn-go" type="button" onclick={() => (setup = true)}>Set up {name}</button>
  </div>
{:else}
  <details class="setup">
    <summary>App credentials</summary>
    <p class="small">
      Client ID <span class="mono">{p.client_id}</span> · secret saved
    </p>
    <div class="act">
      <button class="btn" type="button" disabled={busy} onclick={startEdit}>Replace</button>
      <button class="btn btn-quiet" type="button" disabled={busy} onclick={remove}>Remove</button>
    </div>
    {#if error}<p class="small err" role="alert">{error}</p>{/if}
  </details>
{/if}

<style>
  .small {
    font-size: var(--size-small);
  }
  .creds {
    display: grid;
    gap: var(--space-3);
    max-width: 36rem;
  }
  .steps {
    display: grid;
    gap: var(--space-2);
    margin: 0;
    padding-left: 1.25rem;
  }
  .uri {
    display: block;
    margin-top: var(--space-1);
    padding: var(--space-2) var(--space-3);
    border: 1px solid var(--line);
    border-radius: var(--radius-s);
    font-size: var(--size-label);
    /* a URL: break anywhere rather than overflow the phone */
    word-break: break-all;
  }
  form {
    display: grid;
    gap: var(--space-3);
  }
  label {
    display: grid;
    gap: var(--space-1);
  }
  .lbl {
    font-size: var(--size-small);
    font-weight: 600;
  }
  input {
    min-height: 2.75rem;
    padding: 0 var(--space-3);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    background: var(--surface);
    color: var(--text);
    font: inherit;
    font-family: var(--font-mono);
  }
  .note {
    margin: 0;
  }
  .act {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  form .act {
    margin-top: var(--space-2);
  }
  .btn-go {
    background: var(--signal);
    color: var(--on-signal);
    border: 0;
    min-height: var(--tap-min);
  }
  .link {
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
  .setup {
    margin-top: var(--space-2);
    font-size: var(--size-small);
  }
  .setup summary {
    display: list-item;
    padding: 0.7rem 0;
    font-weight: 600;
    cursor: pointer;
  }
  .err {
    color: var(--danger);
  }
</style>
