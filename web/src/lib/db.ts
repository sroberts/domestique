// IndexedDB is the source of truth on the device: a set is durable here the
// moment it's logged, before any network is involved.
import { openDB, type DBSchema, type IDBPDatabase } from 'idb';
import type { LocalSession, SetRecord, Today } from './types';

export type OutboxKind = 'session' | 'set';

export interface OutboxEntry {
  key: string; // `${kind}:${id}`, so repeated edits coalesce into one upload
  kind: OutboxKind;
  id: string;
  rev: number; // updated_at of the record when queued
  seq: number;
  rejected?: string; // server refused it (4xx other than auth); kept, never dropped
}

interface Schema extends DBSchema {
  sessions: { key: string; value: LocalSession; indexes: { started_at: number } };
  sets: { key: string; value: SetRecord; indexes: { session_id: string } };
  outbox: { key: string; value: OutboxEntry; indexes: { seq: number } };
  cache: { key: string; value: { key: string; at: number; value: unknown } };
}

let dbp: Promise<IDBPDatabase<Schema>> | null = null;

export function db(): Promise<IDBPDatabase<Schema>> {
  dbp ??= openDB<Schema>('domestique', 1, {
    upgrade(d) {
      d.createObjectStore('sessions', { keyPath: 'id' }).createIndex('started_at', 'started_at');
      d.createObjectStore('sets', { keyPath: 'id' }).createIndex('session_id', 'session_id');
      d.createObjectStore('outbox', { keyPath: 'key' }).createIndex('seq', 'seq');
      d.createObjectStore('cache', { keyPath: 'key' });
    },
  });
  return dbp;
}

/** Ask the browser not to evict our data under storage pressure. */
export async function persist(): Promise<void> {
  try {
    if (navigator.storage?.persist && !(await navigator.storage.persisted())) {
      await navigator.storage.persist();
    }
  } catch {
    /* best effort */
  }
}

let seqCounter = Date.now();

/** Save a session and queue it for upload, in one transaction. */
export async function saveSession(s: LocalSession): Promise<void> {
  const d = await db();
  const tx = d.transaction(['sessions', 'outbox'], 'readwrite');
  await tx.objectStore('sessions').put(s);
  await tx.objectStore('outbox').put({ key: `session:${s.id}`, kind: 'session', id: s.id, rev: s.updated_at, seq: ++seqCounter });
  await tx.done;
}

/** Save cursor-only changes (position in the workout), which never sync. */
export async function saveCursor(s: LocalSession): Promise<void> {
  await (await db()).put('sessions', s);
}

export async function saveSet(x: SetRecord): Promise<void> {
  const d = await db();
  const tx = d.transaction(['sets', 'outbox'], 'readwrite');
  await tx.objectStore('sets').put(x);
  await tx.objectStore('outbox').put({ key: `set:${x.id}`, kind: 'set', id: x.id, rev: x.updated_at, seq: ++seqCounter });
  await tx.done;
}

export async function getSession(id: string): Promise<LocalSession | undefined> {
  return (await db()).get('sessions', id);
}

export async function sessionSets(id: string): Promise<SetRecord[]> {
  const sets = await (await db()).getAllFromIndex('sets', 'session_id', id);
  return sets.sort((a, b) => a.started_at - b.started_at);
}

/** The unfinished session, if one is in progress on this device. */
export async function activeSession(): Promise<LocalSession | undefined> {
  const all = await (await db()).getAllFromIndex('sessions', 'started_at');
  return all.reverse().find((s) => s.finished_at == null);
}

export async function localFinished(limit: number): Promise<LocalSession[]> {
  const all = await (await db()).getAllFromIndex('sessions', 'started_at');
  return all
    .filter((s) => s.finished_at != null)
    .reverse()
    .slice(0, limit);
}

export async function cacheToday(t: Today): Promise<void> {
  await (await db()).put('cache', { key: `today:${t.date}`, at: Date.now(), value: t });
}

export async function cachedToday(date: string): Promise<{ at: number; value: Today } | undefined> {
  const row = await (await db()).get('cache', `today:${date}`);
  return row ? { at: row.at, value: row.value as Today } : undefined;
}
