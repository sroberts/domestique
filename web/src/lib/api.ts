import { observe } from './clock';
import type { Summary, Today } from './types';

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
    try {
      msg = ((await res.json()) as { error?: string }).error ?? msg;
    } catch {
      /* not JSON */
    }
    throw new HTTPError(res.status, msg);
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
