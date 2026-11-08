// Asking about your own data: POST /v1/ask, answered by Outpost's LLM
// looking things up with Domestique's read-only tools.
import { getJSON, postForJSON } from './api';
import type { AskResult } from './types';

let ai: Promise<boolean> | null = null;

/** Whether Outpost gives Domestique an LLM. Asked once per page load. */
export function aiAvailable(): Promise<boolean> {
  ai ??= getJSON<{ ai?: boolean }>('/v1/me').then(
    (m) => !!m.ai,
    () => {
      ai = null; // offline or signed out: ask again next time
      return false;
    },
  );
  return ai;
}

export interface Turn {
  question: string;
  answer: string;
  tools: string[];
  checked: boolean;
}

/** Earlier turns sent with a follow-up (the server keeps the last 4). */
export const HISTORY = 4;

export function ask(question: string, history: Turn[]): Promise<AskResult> {
  return postForJSON<AskResult>('/v1/ask', {
    question,
    history: history.slice(-HISTORY).map((t) => ({ question: t.question, answer: t.answer })),
  });
}

/** What each tool looked at, in the athlete's words. */
const toolNames: Record<string, string> = {
  get_today: 'today',
  get_recent_activities: 'recent activities',
  get_activity_detail: 'one workout in detail',
  get_week_note: 'the week note',
  get_training_load: 'training load',
  get_recovery: 'recovery and sleep',
  get_trends: 'trends',
  get_gear: 'gear',
  list_templates: 'workouts',
};

export function lookedAt(tools: string[]): string {
  return tools.map((t) => toolNames[t] ?? t.replace(/^get_/, '').replaceAll('_', ' ')).join(', ');
}
