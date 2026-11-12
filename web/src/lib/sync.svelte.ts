// The offline queue. Records are written to IndexedDB first; this uploads them
// whenever a connection exists. Nothing is ever dropped: auth failures wait for
// sign-in, network failures wait for the network, and server rejections are
// kept and reported.
import { AuthError, HTTPError, OfflineError, deleteJSON, fetchMerges, fetchSummary, mergedInto, putJSON } from './api';
import { clockOffset } from './clock';
import { applyMerge, db, getSession, mergesSince, pendingFor, setMergesSince, type OutboxEntry } from './db';
import type { Summary } from './types';

export type SyncStatus = 'idle' | 'syncing' | 'offline' | 'auth' | 'error';

class Sync {
  status = $state<SyncStatus>('idle');
  pending = $state(0);
  rejected = $state(0);
  lastSynced = $state<number | null>(null);

  #running = false;
  #again = false;
  #timer: ReturnType<typeof setTimeout> | null = null;
  #backoff = 2000;
  #mergesAt = 0;

  start(): void {
    void this.refreshCount();
    void this.flush();
    if (!navigator.onLine) this.status = 'offline';
    window.addEventListener('online', () => {
      if (this.status === 'offline') this.status = 'idle';
      void this.flush();
    });
    window.addEventListener('offline', () => (this.status = 'offline'));
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') void this.flush();
    });
  }

  /** Called after every local write. */
  kick(): void {
    void this.refreshCount();
    void this.flush();
  }

  async refreshCount(): Promise<void> {
    const all = await (await db()).getAll('outbox');
    this.rejected = all.filter((e) => e.rejected).length;
    this.pending = all.length - this.rejected;
  }

  async flush(): Promise<void> {
    if (this.#running) {
      this.#again = true;
      return;
    }
    this.#running = true;
    if (this.#timer) clearTimeout(this.#timer);
    this.#timer = null;
    try {
      do {
        this.#again = false;
        await this.#drain();
      } while (this.#again);
    } finally {
      this.#running = false;
      await this.refreshCount();
    }
  }

  async #drain(): Promise<void> {
    const d = await db();
    const queue = (await d.getAllFromIndex('outbox', 'seq')).filter((e) => !e.rejected);
    if (queue.length === 0) {
      if (this.status !== 'auth') this.status = navigator.onLine ? 'idle' : 'offline';
      void this.followMerges();
      return;
    }
    this.status = 'syncing';
    for (const entry of queue) {
      try {
        await this.#upload(entry);
      } catch (err) {
        if (err instanceof AuthError) {
          this.status = 'auth';
          return;
        }
        if (err instanceof OfflineError) {
          this.status = 'offline';
          this.#retry();
          return;
        }
        if (err instanceof HTTPError && err.status >= 400 && err.status < 500) {
          // The server will never accept this as-is. Keep it, flag it, move on.
          await d.put('outbox', { ...entry, rejected: err.message });
          console.error('sync rejected', entry.key, err.message);
          continue;
        }
        this.status = 'error';
        this.#retry();
        return;
      }
    }
    this.#backoff = 2000;
    this.status = 'idle';
    this.lastSynced = Date.now();
  }

  async #upload(entry: OutboxEntry): Promise<void> {
    const d = await db();
    if (entry.kind === 'session-delete') {
      await deleteJSON(`/v1/sessions/${encodeURIComponent(entry.id)}`);
      await d.delete('outbox', entry.key);
      return;
    }
    const record = entry.kind === 'session' ? await d.get('sessions', entry.id) : await d.get('sets', entry.id);
    if (record) {
      if (entry.kind === 'session') {
        // The cursor is device state; the server gets the session itself.
        const { cursor: _cursor, ...session } = record as typeof record & { cursor?: unknown };
        // The latest clock reading goes with it, so the Mac Mini can put the
        // phone's set times on true time when it lines up heart rate.
        await putJSON(`/v1/sessions/${encodeURIComponent(entry.id)}`, { ...session, clock_offset_ms: clockOffset() });
      } else {
        await putJSON(`/v1/sets/${encodeURIComponent(entry.id)}`, record);
      }
    }
    // Remove the entry only if nothing newer was queued while we uploaded.
    const tx = d.transaction('outbox', 'readwrite');
    const current = await tx.store.get(entry.key);
    if (current && current.seq === entry.seq) await tx.store.delete(entry.key);
    await tx.done;
  }

  /** Drop sessions merged into others since this phone last looked, and
   * refresh the ones they went into. At most every 5 minutes unless forced
   * (straight after a merge from this phone). A merge whose sessions still
   * have uploads waiting is left for next time, so nothing is lost. */
  async followMerges(force = false): Promise<void> {
    if (!force && Date.now() - this.#mergesAt < 5 * 60_000) return;
    this.#mergesAt = Date.now();
    try {
      for (const m of await fetchMerges(await mergesSince())) {
        if (await pendingFor([m.from, m.into])) return;
        if ((await getSession(m.from)) || (await getSession(m.into))) {
          let into: Summary | null = null;
          try {
            into = await fetchSummary(m.into);
          } catch (err) {
            // Merged on again since: a later entry follows that one.
            if (!mergedInto(err)) return;
          }
          await applyMerge(m.from, into);
        }
        await setMergesSince(m.merged_at);
      }
    } catch {
      /* offline or signed out: next time */
    }
  }

  #retry(): void {
    if (this.#timer) return;
    this.#timer = setTimeout(() => {
      this.#timer = null;
      void this.flush();
    }, this.#backoff);
    this.#backoff = Math.min(this.#backoff * 2, 60_000);
  }
}

export const sync = new Sync();
