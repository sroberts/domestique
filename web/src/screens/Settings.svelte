<script lang="ts">
  import Sun from '@lucide/svelte/icons/sun';
  import Moon from '@lucide/svelte/icons/moon';
  import { AuthError, getJSON, putJSON } from '../lib/api';
  import { fetchConnections } from '../lib/data';
  import { restSound, theme } from '../lib/device.svelte';
  import { relDay, time } from '../lib/format';
  import type { ProviderStatus } from '../lib/types';

  const params = new URLSearchParams(location.search);
  let flash = $state<{ ok: boolean; text: string } | null>(
    params.get('error')
      ? { ok: false, text: `${cap(params.get('provider') ?? '')} didn't connect: ${params.get('error')}` }
      : params.get('connected')
        ? { ok: true, text: `${cap(params.get('provider') ?? '')} connected. The first sync is running now.` }
        : null,
  );
  if (params.size) history.replaceState(null, '', '/settings');

  function cap(s: string) {
    return s.charAt(0).toUpperCase() + s.slice(1);
  }

  interface Me {
    gateway: boolean;
    user: { username: string; name: string; email: string } | null;
  }
  let me = $state<Me | null>(null);
  async function loadMe() {
    try {
      me = await getJSON<Me>('/v1/me');
    } catch {
      /* offline */
    }
  }

  let providers = $state<ProviderStatus[]>([]);
  let soundOn = $state(restSound.on);
  let base = $state('');
  let offline = $state(false);

  async function loadConnections() {
    const r = await fetchConnections();
    if (r.data) {
      providers = r.data.providers;
      base = r.data.redirect_base;
    }
    offline = !!r.stale || r.error;
  }

  // Thresholds
  interface AthleteResp {
    current: { ftp: number; ftp_source: string; max_hr: number; max_hr_source: string; rest_hr: number };
    ftp_override: number | null;
    ftp_detected: number | null;
    max_hr_override: number | null;
    max_hr_detected: number | null;
    max_hr_whoop: number | null;
  }
  let ath = $state<AthleteResp | null>(null);
  let ftpInput = $state('');
  let hrInput = $state('');
  let savingAth = $state(false);
  let athMsg = $state('');

  async function loadAthlete() {
    try {
      ath = await getJSON<AthleteResp>('/v1/athlete');
      ftpInput = ath.ftp_override ? String(ath.ftp_override) : '';
      hrInput = ath.max_hr_override ? String(ath.max_hr_override) : '';
    } catch {
      /* offline: section shows a note */
    }
  }

  async function saveAthlete(e: SubmitEvent) {
    e.preventDefault();
    savingAth = true;
    athMsg = '';
    try {
      await putJSON('/v1/athlete', { ftp_override: Number(ftpInput) || 0, max_hr_override: Number(hrInput) || 0 });
      await loadAthlete();
      athMsg = 'Saved. Every activity has been rescored.';
    } catch (err) {
      athMsg = err instanceof AuthError ? 'Sign in again to save.' : `Couldn't save: ${(err as Error).message}`;
    } finally {
      savingAth = false;
    }
  }

  // Imports
  interface Job {
    id: string;
    name: string;
    state: 'running' | 'done' | 'failed';
    progress: { Total: number; Done: number; Imported: number; Merged: number; Duplicates: number; Failed: number };
    error?: string;
  }
  let jobs = $state<Job[]>([]);
  let uploading = $state(false);
  let uploadMsg = $state('');
  let poll: ReturnType<typeof setInterval> | null = null;

  async function loadJobs() {
    try {
      jobs = (await getJSON<{ jobs: Job[] }>('/v1/imports')).jobs;
      if (!jobs.some((j) => j.state === 'running') && poll) {
        clearInterval(poll);
        poll = null;
      }
    } catch {
      /* ignore */
    }
  }

  async function upload(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const files = input.files;
    if (!files?.length) return;
    uploading = true;
    uploadMsg = '';
    const form = new FormData();
    for (const f of files) form.append('file', f, f.name);
    try {
      const res = await fetch('/v1/imports', { method: 'POST', body: form, credentials: 'same-origin' });
      if (res.status === 401) throw new AuthError();
      const body = (await res.json()) as { results?: { file: string; error?: string; result?: { duplicate: boolean; merged: boolean } }[]; error?: string };
      if (!res.ok) throw new Error(body.error ?? `HTTP ${res.status}`);
      const r = body.results ?? [];
      const bad = r.filter((x) => x.error);
      const fresh = r.filter((x) => x.result && !x.result.duplicate);
      const zips = r.filter((x) => !x.result && !x.error);
      uploadMsg = [
        fresh.length ? `${fresh.length} imported` : '',
        r.filter((x) => x.result?.duplicate).length ? `${r.filter((x) => x.result?.duplicate).length} already here` : '',
        zips.length ? 'Strava archive importing in the background' : '',
        bad.length ? `${bad.length} couldn't be read (${bad.map((b) => b.file).join(', ')})` : '',
      ].filter(Boolean).join(' · ');
      await loadJobs();
      if (zips.length && !poll) poll = setInterval(loadJobs, 2000);
    } catch (err) {
      uploadMsg = err instanceof AuthError ? 'Sign in again to upload.' : `Upload failed: ${(err as Error).message}`;
    } finally {
      uploading = false;
      input.value = '';
    }
  }

  async function syncNow(p: string) {
    await fetch(`/v1/connections/${p}/sync`, { method: 'POST', credentials: 'same-origin' });
    setTimeout(loadConnections, 1500);
  }

  async function disconnect(p: string) {
    if (!confirm(`Disconnect ${cap(p)}? Data already imported stays.`)) return;
    await fetch(`/v1/oauth/${p}`, { method: 'DELETE', credentials: 'same-origin' });
    await loadConnections();
  }

  $effect(() => {
    void loadMe();
    void loadConnections();
    void loadAthlete();
    void loadJobs();
    return () => poll && clearInterval(poll);
  });

  const blurb: Record<string, string> = {
    whoop: 'Recovery, HRV, resting heart rate, sleep, workout strain and max heart rate. Checked every 30 minutes.',
    wahoo: 'The original FIT file of every ride from your ELEMNT: power, heart rate, cadence, GPS. Checked every 15 minutes.',
  };
  const envName: Record<string, string> = { whoop: 'DOMESTIQUE_WHOOP', wahoo: 'DOMESTIQUE_WAHOO' };
  let hrURL = $derived(`${base || location.origin}/v1/hr`);
</script>

<main class="page">
  <header class="top"><h1>Settings</h1></header>

  {#if flash}
    <p class="flash" class:bad={!flash.ok} role="status">{flash.text}</p>
  {/if}

  {#if me}
    <section class="block account" aria-labelledby="acct-h">
      <h2 id="acct-h" class="visually-hidden">Account</h2>
      {#if me.gateway && me.user}
        <p>Signed in to Outpost as <strong>{me.user.name || me.user.username}</strong>{#if me.user.email}<span class="muted"> · {me.user.email}</span>{/if}</p>
        <!-- Outpost owns sign-in: a same-origin POST to its own path ends the
             whole Outpost login, not only Domestique's. -->
        <form method="post" action="/.outpost/logout">
          <button class="btn" type="submit">Sign out of Outpost</button>
        </form>
      {:else if !me.gateway}
        <p class="warn small" role="note"><strong>No sign-in in front of Domestique.</strong> Anyone who can reach this address can use it. Run it under Outpost with the login gateway on.</p>
      {/if}
    </section>
  {/if}

  <section class="block" aria-labelledby="thr-h">
    <h2 id="thr-h">Thresholds</h2>
    {#if ath}
      <form class="thr" onsubmit={saveAthlete}>
        <label>
          <span class="label">FTP</span>
          <span class="field">
            <input class="mono" inputmode="numeric" pattern="[0-9]*" placeholder={ath.ftp_detected ? `Auto · ${ath.ftp_detected}` : 'Auto'} bind:value={ftpInput} aria-describedby="ftp-help" />
            <span class="unit">W</span>
          </span>
          <span id="ftp-help" class="muted small">
            {ath.ftp_detected ? `Detected ${ath.ftp_detected} W from your best 20 minutes.` : 'Detected once you ride with power.'} Leave blank to keep detecting.
          </span>
        </label>
        <label>
          <span class="label">Max heart rate</span>
          <span class="field">
            <input class="mono" inputmode="numeric" pattern="[0-9]*" placeholder={`Auto · ${ath.max_hr_whoop ?? ath.max_hr_detected ?? 190}`} bind:value={hrInput} aria-describedby="hr-help" />
            <span class="unit">bpm</span>
          </span>
          <span id="hr-help" class="muted small">
            {ath.max_hr_whoop ? `Whoop says ${ath.max_hr_whoop}.` : ath.max_hr_detected ? `Highest seen: ${ath.max_hr_detected}.` : ''} Zones and heart-rate load use this.
          </span>
        </label>
        <div class="thr-act">
          <button class="btn" type="submit" disabled={savingAth}>{savingAth ? 'Rescoring…' : 'Save'}</button>
          {#if athMsg}<span class="small" role="status">{athMsg}</span>{/if}
        </div>
      </form>
    {:else}
      <p class="muted small">Needs a connection to the Mac Mini.</p>
    {/if}
  </section>

  <section class="block" aria-labelledby="conn-h">
    <h2 id="conn-h">Connections</h2>
    {#if offline}<p class="muted small">No connection to the Mac Mini, so this may be out of date.</p>{/if}
    <ul class="list">
      {#each providers as p (p.provider)}
        <li class="conn">
          <div class="conn-main">
            <p class="conn-name">
              {cap(p.provider)}
              {#if p.connected}<span class="state on">Connected</span>{:else if p.configured}<span class="state">Not connected</span>{:else}<span class="state">Not set up</span>{/if}
            </p>
            <p class="muted small">{blurb[p.provider]}</p>
            {#if p.connected}
              <p class="mono small muted">
                {p.syncing ? 'Syncing now…' : p.last_sync ? `Last synced ${relDay(p.last_sync)} ${time(p.last_sync)}` : 'Waiting for first sync'}
              </p>
            {/if}
            {#if p.last_error}<p class="mono small err">{p.last_error}</p>{/if}
            {#if !p.configured}
              <details class="setup">
                <summary>How to set up {cap(p.provider)}</summary>
              <p class="small">
                Register an app with {cap(p.provider)}, then give Domestique <code class="mono">{envName[p.provider]}_CLIENT_ID</code>
                and <code class="mono">{envName[p.provider]}_CLIENT_SECRET_FILE</code> (a path to a file holding the secret).
                Redirect URI: <code class="mono">{base}/v1/oauth/{p.provider}/callback</code>
              </p>
              </details>
            {/if}
          </div>
          <div class="conn-act">
            {#if p.connected}
              <button class="btn" onclick={() => syncNow(p.provider)} disabled={p.syncing}>Sync now</button>
              <button class="btn btn-quiet" onclick={() => disconnect(p.provider)}>Disconnect</button>
            {:else if p.configured}
              <!-- A full navigation: the provider's sign-in page, then back here. -->
              <a class="btn btn-connect" href={`/v1/oauth/${p.provider}/start`}>Connect {cap(p.provider)}</a>
            {/if}
          </div>
        </li>
      {/each}
    </ul>
  </section>

  <section class="block" aria-labelledby="imp-h">
    <h2 id="imp-h">Import</h2>
    <p class="muted small">
      FIT, GPX or TCX files, or your whole Strava archive (the zip from Strava's "Download your account"). Duplicates are
      recognised and merged, so importing twice is harmless.
    </p>
    <label class="btn file">
      <input type="file" multiple accept=".fit,.gpx,.tcx,.gz,.zip" onchange={upload} disabled={uploading} />
      {uploading ? 'Uploading…' : 'Choose files'}
    </label>
    {#if uploadMsg}<p class="small" role="status">{uploadMsg}</p>{/if}
    {#each jobs as j (j.id)}
      <div class="job">
        <p class="small"><strong>{j.name}</strong> · {j.state === 'running' ? 'importing' : j.state}</p>
        {#if j.progress.Total}
          <div class="meter" role="progressbar" aria-valuemin={0} aria-valuemax={j.progress.Total} aria-valuenow={j.progress.Done}>
            <span style:transform={`scaleX(${j.progress.Done / j.progress.Total})`}></span>
          </div>
          <p class="mono small muted">
            {j.progress.Done} of {j.progress.Total} · {j.progress.Imported} new · {j.progress.Merged} merged · {j.progress.Duplicates} already here{j.progress.Failed ? ` · ${j.progress.Failed} unreadable` : ''}
          </p>
        {/if}
        {#if j.error}<p class="mono small err">{j.error}</p>{/if}
      </div>
    {/each}
    <p class="muted small">
      Large archives import faster from the Mac Mini itself:
      <code class="mono">domestique import ~/Downloads/export_*.zip</code>. Folders of FIT files (Wahoo's Dropbox folder,
      Zwift's Activities folder) are watched when listed in <code class="mono">DOMESTIQUE_WATCH_DIRS</code>.
    </p>
  </section>

  <section class="block" aria-labelledby="hr-h2">
    <h2 id="hr-h2">Heart rate for lifting</h2>
    <p class="small">
      Whoop's API gives workout summaries but not second-by-second heart rate, which per-set analysis needs. If Whoop's
      Apple Health integration is on (Whoop app → Settings → Integrations → Apple Health, with heart rate allowed), an iOS
      Shortcut can forward the samples after each session:
    </p>
    <details class="setup">
      <summary>Set up the Apple Health shortcut</summary>
    <ol class="steps small">
      <li>Shortcuts → new shortcut → <strong>Find Health Samples</strong>: Heart Rate, start date in the last 3 hours.</li>
      <li><strong>Get Contents of URL</strong>: <code class="mono">{hrURL}</code>, method POST, JSON body
        <code class="mono">{'{"source":"health","samples":[{"t":<Start Date>,"bpm":<Value>}…]}'}</code>, with an
        <code class="mono">Authorization: Bearer opat_…</code> header from <code class="mono">outpost token create</code>.</li>
      <li>Add it as a Personal Automation when a Whoop workout ends, or run it from the session summary.</li>
    </ol>
    </details>
    <p class="muted small">A chest strap recorded by the Wahoo app or head unit works too: import its file and the sets pick up its heart rate.</p>
  </section>

  <section class="block" aria-labelledby="app-h">
    <h2 id="app-h">Preferences</h2>
    <div class="pref">
      <button class="btn" role="switch" aria-checked={soundOn} aria-describedby="chime-note" onclick={() => { soundOn = !soundOn; restSound.set(soundOn); }}>
        Rest timer chime <span class="sw-state" class:on={soundOn} aria-hidden="true">{soundOn ? 'On' : 'Off'}</span>
      </button>
      <p id="chime-note" class="muted small">Plays when rest is over. iPhones can't vibrate from a web app, so this is the cue that reaches a phone on the bench.</p>
    </div>
    <div class="pref">
      <button class="btn" onclick={() => theme.toggle()}>
        {#if theme.value === 'dark'}<Sun size={18} aria-hidden="true" /> Use light theme{:else}<Moon size={18} aria-hidden="true" /> Use dark theme{/if}
      </button>
    </div>
  </section>
</main>

<style>
  .page {
    max-width: var(--page-list);
    margin: 0 auto;
    padding: calc(env(safe-area-inset-top) + var(--space-2)) var(--gutter) 0;
  }
  .top {
    min-height: 2.75rem;
    display: flex;
    align-items: center;
  }
  h1 {
    font-size: var(--size-heading);
  }
  h2 {
    font-size: var(--size-lead);
    margin-bottom: var(--space-2);
  }
  .small {
    font-size: var(--size-small);
    margin-top: var(--space-2);
  }
  .err {
    color: var(--danger);
  }
  code {
    font-size: 0.875em;
    overflow-wrap: anywhere;
  }
  .warn {
    padding: var(--space-3) var(--space-4);
    border: 1px solid var(--zone-4);
    border-radius: var(--radius-m);
    background: color-mix(in srgb, var(--zone-4) 8%, transparent);
  }
  .flash {
    margin-top: var(--space-3);
    padding: var(--space-3) var(--space-4);
    border: 1px solid var(--zone-3);
    border-radius: var(--radius-m);
    font-size: var(--size-small);
  }
  .flash.bad {
    border-color: var(--danger);
  }
  .block {
    margin-top: var(--space-6);
    padding-top: var(--space-5);
    border-top: 1px solid var(--line);
  }
  .block:first-of-type {
    border-top: 0;
  }
  .account {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    margin-top: var(--space-4);
    padding-top: 0;
    border-top: 0;
  }
  .account + .block {
    border-top: 1px solid var(--line);
  }
  .list {
    list-style: none;
    margin: var(--space-2) 0 0;
    padding: 0;
  }
  .conn {
    display: grid;
    gap: var(--space-3);
    padding: var(--space-4) 0;
    border-bottom: 1px solid var(--line);
  }
  .conn:last-child {
    border-bottom: 0; /* the next section's rule is the divider */
  }
  .conn-name {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    font-weight: 700;
  }
  .state {
    font-size: var(--size-label);
    font-weight: 600;
    color: var(--text-muted);
    padding: 0 0.375rem;
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-s);
  }
  .state.on {
    color: var(--text);
    border-color: var(--zone-3);
  }
  .conn-act {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  .btn-connect {
    background: var(--signal);
    color: var(--on-signal);
    border: 0;
    min-height: var(--tap-min);
  }
  .file {
    position: relative;
    margin-top: var(--space-3);
    min-height: var(--tap-min);
  }
  .file input {
    position: absolute;
    inset: 0;
    opacity: 0;
    cursor: pointer;
  }
  .job {
    margin-top: var(--space-3);
  }
  .meter {
    height: 0.5rem;
    margin-top: var(--space-2);
    background: var(--line);
    border-radius: 4px;
    overflow: hidden;
  }
  .meter span {
    display: block;
    height: 100%;
    background: var(--text);
    transform-origin: left center;
    transition: transform 300ms linear;
  }
  .thr {
    display: grid;
    gap: var(--space-4);
    margin-top: var(--space-3);
  }
  .thr label {
    display: grid;
    gap: var(--space-1);
  }
  .field {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }
  .field input {
    width: 9rem;
    min-height: 2.75rem;
    padding: 0 var(--space-3);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-m);
    background: var(--surface);
    color: var(--text);
    font-size: var(--size-lead);
  }
  .field input::placeholder {
    color: var(--text-muted);
  }
  .unit {
    color: var(--text-muted);
    font-size: var(--size-small);
  }
  .thr-act {
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }
  .setup {
    margin-top: var(--space-2);
    font-size: var(--size-small);
  }
  .setup summary {
    /* list-item keeps the disclosure triangle; padding keeps a 44px target */
    display: list-item;
    padding: 0.7rem 0;
    font-weight: 600;
    cursor: pointer;
  }
  .pref {
    display: grid;
    justify-items: start;
    gap: var(--space-2);
    margin-top: var(--space-3);
  }
  .sw-state {
    margin-left: var(--space-2);
    padding: 0 var(--space-2);
    border-radius: var(--radius-s);
    font-size: var(--size-label);
    font-weight: 700;
    background: var(--line);
    color: var(--text-muted);
  }
  .sw-state.on {
    background: var(--signal);
    color: var(--on-signal);
  }
  .steps {
    padding-left: 1.25rem;
    display: grid;
    gap: var(--space-2);
  }
</style>
