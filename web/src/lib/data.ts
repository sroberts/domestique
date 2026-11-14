// Read-through cache: every screen shows the last copy it saw when the Mac
// Mini can't be reached, and says how old it is.
import { AuthError, deleteJSON, getJSON, putJSON } from './api';
import { db } from './db';
import type { ActivityDetail, ActivityRow, Athlete, LoadDay, ProviderStatus, RidePlan, Trends } from './types';

export interface Loaded<T> {
  data: T | null;
  stale: number | null; // when the cached copy was saved, if we fell back to it
  auth: boolean;
  error: boolean;
}

export async function cachedGet<T>(path: string): Promise<Loaded<T>> {
  const key = `get:${path}`;
  try {
    const data = await getJSON<T>(path);
    try {
      await (await db()).put('cache', { key, at: Date.now(), value: data });
    } catch {
      /* quota: fine, we still have fresh data */
    }
    return { data, stale: null, auth: false, error: false };
  } catch (err) {
    const row = await (await db()).get('cache', key).catch(() => undefined);
    return {
      data: (row?.value as T) ?? null,
      stale: row?.at ?? null,
      auth: err instanceof AuthError,
      error: !row,
    };
  }
}

export const fetchActivities = (q: string) =>
  cachedGet<{ activities: ActivityRow[]; next_before: number }>(`/v1/activities${q}`);
export const fetchActivity = (id: string, source?: string) =>
  cachedGet<ActivityDetail>(
    `/v1/activities/${encodeURIComponent(id)}${source ? `?source=${encodeURIComponent(source)}` : ''}`,
  );
export const fetchLoad = (days: number) => cachedGet<{ days: LoadDay[]; athlete: Athlete }>(`/v1/load?days=${days}`);
export const fetchTrends = () => cachedGet<Trends>('/v1/trends');
export const fetchConnections = () => cachedGet<{ providers: ProviderStatus[]; redirect_base: string }>('/v1/connections');

/** Rides planned from a date, two weeks on. */
export const fetchRidePlans = (from: string) => cachedGet<RidePlan[]>(`/v1/ride-plans?from=${from}`);
export const saveRidePlan = (p: RidePlan) => putJSON(`/v1/ride-plans/${p.date}`, p);
export const clearRidePlan = (date: string) => deleteJSON(`/v1/ride-plans/${date}`);
