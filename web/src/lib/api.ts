import { observe } from './clock';
import type { Merge, MergePreview, Summary, Today } from './types';

/** Thrown when the Outpost gateway wants the athlete to sign in again. */
export class AuthError extends Error {
  constructor() {
    super('sign-in required');
  }
}

/** Thrown when there's no connection to Outpost at all. */
export class OfflineError extends Error {
  constructor() {
    super('offline');
  }
}

export class HTTPError extends Error {
  constructor(
    public status: number,
    message: string,
    /** The JSON body the server sent with the error, if any. */
    public data: Record<string, unknown> = {},
  ) {
    super(message);
  }
}

async function request(path: string, init: RequestInit = {}): Promise<Response> {
  let res: Response;
  const sent = Date.now();
  try {
    // Manual redirects: an expired gateway session answers a GET with a redirect
    // to sign-in, which must not be mistaken for data.
    res = await fetch(path, { credentials: 'same-origin', redirect: 'manual', ...init });
  } catch {
    throw new OfflineError();
  }
  observe(sent, Date.now(), res.headers.get('X-Server-Time'));
  if (res.type === 'opaqueredirect' || res.status === 401) throw new AuthError();
  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    let data: Record<string, unknown> = {};
    try {
      data = (await res.json()) as Record<string, unknown>;
      if (typeof data.error === 'string') msg = data.error;
    } catch {
      /* not JSON */
    }
    throw new HTTPError(res.status, msg, data);
  }
  const type = res.headers.get('content-type') ?? '';
  if (res.status !== 204 && !type.includes('application/json')) {
    // Something answered that isn't Domestique (a sign-in page, a captive portal).
    throw new AuthError();
  }
  return res;
}

export async function getJSON<T>(path: string): Promise<T> {
  return (await (await request(path)).json()) as T;
}

export async function putJSON(path: string, body: unknown): Promise<void> {
  await request(path, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}

export async function postJSON(path: string, body?: unknown): Promise<void> {
  await request(path, {
    method: 'POST',
    ...(body === undefined ? {} : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }),
  });
}

/** POST a JSON body and read the JSON answer. */
export async function postForJSON<T>(path: string, body: unknown): Promise<T> {
  const res = await request(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return (await res.json()) as T;
}

export async function deleteJSON(path: string): Promise<void> {
  try {
    await request(path, { method: 'DELETE' });
  } catch (err) {
    if (err instanceof HTTPError && err.status === 404) return; // already gone
    throw err;
  }
}

export function localDate(d = new Date()): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

export const fetchToday = (date: string) => getJSON<Today>(`/v1/today?date=${date}`);
export const fetchSummary = (id: string) => getJSON<Summary>(`/v1/sessions/${encodeURIComponent(id)}/summary`);

/** The session a merged-away one became, when err says so. */
export function mergedInto(err: unknown): string | null {
  return err instanceof HTTPError && err.status === 410 && typeof err.data.merged_into === 'string' ? err.data.merged_into : null;
}

const mergePath = (id: string, dry: boolean) => `/v1/sessions/${encodeURIComponent(id)}/merge${dry ? '?dry_run=1' : ''}`;
/** What merging two sessions would make, without making it. */
export const previewMerge = (id: string, other: string) => postForJSON<MergePreview>(mergePath(id, true), { with: other });
/** Merge two sessions; the earlier is kept and named in the answer. */
export const mergeSessions = (id: string, other: string) => postForJSON<{ id: string; merged: string }>(mergePath(id, false), { with: other });
/** Merges made since a time (epoch ms), for dropping merged-away sessions. */
export const fetchMerges = (since: number) => getJSON<Merge[]>(`/v1/sessions/merged?since=${since}`);
