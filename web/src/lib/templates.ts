// Saved workouts (strength templates): the list, the plan for today, and
// starting one. The list is cached on the device so any workout can be
// started with no signal; editing needs the Mac Mini.
import { HTTPError, deleteJSON, getJSON, localDate, putJSON } from './api';
import { cachedGet } from './data';
import { cachedToday } from './db';
import { beginSession } from './workout.svelte';
import type { Exercise, LocalSession, TargetSet, Template } from './types';

export interface TemplateRow extends Template {
  last_done_at: number | null;
}

/** An exercise name seen before, with a starting point for its sets. */
export interface ExerciseSuggestion {
  name: string;
  sets: TargetSet[];
  rest_seconds: number;
  last_at: number;
}

export const fetchTemplates = () => cachedGet<TemplateRow[]>('/v1/templates');
export const fetchPlan = (date: string) => cachedGet<{ date: string; template_id: string | null }>(`/v1/plans/${date}`);
export const fetchTemplate = (id: string) => getJSON<Template>(`/v1/templates/${encodeURIComponent(id)}`);
export const fetchExercises = () => getJSON<ExerciseSuggestion[]>('/v1/exercises');

export async function saveTemplate(t: Template): Promise<void> {
  await putJSON(`/v1/templates/${encodeURIComponent(t.id)}`, t);
}

export const planToday = (id: string) => putJSON(`/v1/plans/${localDate()}`, { template_id: id });
export const clearToday = () => deleteJSON(`/v1/plans/${localDate()}`);

/** Delete a workout. Resolves to the reason when the server refuses because
 * it's planned for today or later and clearPlans wasn't given. */
export async function deleteTemplate(id: string, clearPlans: boolean): Promise<{ planned: string } | null> {
  const q = new URLSearchParams({ date: localDate() });
  if (clearPlans) q.set('clear_plans', '1');
  try {
    await deleteJSON(`/v1/templates/${encodeURIComponent(id)}?${q}`);
    return null;
  } catch (err) {
    if (err instanceof HTTPError && err.status === 409) {
      if (err.message.includes("today's plan")) return { planned: "It's today's plan." };
      const dates = err.message.match(/\d{4}-\d{2}-\d{2}/g) ?? [];
      const fmt = new Intl.DateTimeFormat('en-GB', { weekday: 'short', day: 'numeric', month: 'short' });
      const days = dates.map((d) => fmt.format(new Date(`${d}T12:00:00`)).replace(',', ''));
      return { planned: `It's planned for ${days.join(', ')}.` };
    }
    throw err;
  }
}

/** Start a workout now with today's prescription (progression from history,
 * held or trimmed by recovery), exactly as Today does for the planned one.
 * With no connection: today's cached plan if it's this workout, else the
 * workout as saved. */
export async function startTemplate(t: Template): Promise<LocalSession> {
  const date = localDate();
  let plan: Template = t;
  try {
    const timeout = new Promise<never>((_, reject) => setTimeout(() => reject(new Error('timeout')), 4000));
    const r = await Promise.race([getJSON<{ plan: Template }>(`/v1/templates/${encodeURIComponent(t.id)}/prescribed?date=${date}`), timeout]);
    plan = r.plan;
  } catch {
    const today = await cachedToday(date).catch(() => undefined);
    if (today?.value.plan?.id === t.id) plan = today.value.plan;
  }
  return beginSession(plan);
}

export const setCount = (ex: Exercise[]) => ex.reduce((n, e) => n + e.sets.length, 0);
