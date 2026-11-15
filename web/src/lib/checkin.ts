// The check-in after a workout: the questions' words, and how a check-in
// reads back. Answers are the server's fixed values; only the words live
// here. No imports beyond types, so Node's test runner can load it.
import type { CheckIn, Question } from './types';

export interface QuestionText {
  k: Question;
  label: string;
  answers: { v: string; label: string }[];
}

const Q: Record<Question, QuestionText> = {
  effort: {
    k: 'effort',
    label: 'How hard?',
    answers: [
      { v: 'easy', label: 'Easy' },
      { v: 'moderate', label: 'Moderate' },
      { v: 'hard', label: 'Hard' },
      { v: 'very_hard', label: 'Very hard' },
      { v: 'all_out', label: 'All out' },
    ],
  },
  legs: {
    k: 'legs',
    label: 'Legs?',
    answers: [
      { v: 'fresh', label: 'Fresh' },
      { v: 'normal', label: 'Normal' },
      { v: 'heavy', label: 'Heavy' },
      { v: 'dead', label: 'Dead' },
    ],
  },
  plan: {
    k: 'plan',
    label: 'To plan?',
    answers: [
      { v: 'more', label: 'Did more' },
      { v: 'as_planned', label: 'As planned' },
      { v: 'cut_short', label: 'Cut short' },
      { v: 'couldnt', label: "Couldn't" },
    ],
  },
  pain: {
    k: 'pain',
    label: 'Anything hurting?',
    answers: [
      { v: 'none', label: 'Nothing' },
      { v: 'niggle', label: 'A niggle' },
      { v: 'pain', label: 'Pain' },
    ],
  },
  fuel: {
    k: 'fuel',
    label: 'Fuel?',
    answers: [
      { v: 'good', label: 'Good' },
      { v: 'low_energy', label: 'Low energy' },
      { v: 'fuelled_badly', label: 'Fuelled badly' },
    ],
  },
};

/** The words for these questions; a lifting session asks about the body, not the legs. */
export function questionTexts(qs: Question[], session = false): QuestionText[] {
  return qs.map((k) => (k === 'legs' && session ? { ...Q.legs, label: 'Body?' } : Q[k]));
}

export const BODY_PARTS: { v: string; label: string }[] = [
  { v: 'knee', label: 'Knee' },
  { v: 'back', label: 'Back' },
  { v: 'hip', label: 'Hip' },
  { v: 'hamstring', label: 'Hamstring' },
  { v: 'calf', label: 'Calf' },
  { v: 'quads', label: 'Quads' },
  { v: 'shoulder', label: 'Shoulder' },
  { v: 'neck', label: 'Neck' },
  { v: 'ankle', label: 'Ankle' },
  { v: 'foot', label: 'Foot' },
  { v: 'wrist', label: 'Wrist' },
  { v: 'elbow', label: 'Elbow' },
  { v: 'other', label: 'Other' },
];

const label = (k: Question, v: string | undefined) => Q[k].answers.find((a) => a.v === v)?.label ?? '';
const partLabel = (v: string) => BODY_PARTS.find((p) => p.v === v)?.label.toLowerCase() ?? v;

/** Whether anything is answered, or there's a note. */
export function answered(c: CheckIn | null | undefined): boolean {
  return !!c && !!(c.effort || c.legs || c.plan || c.pain || c.fuel || c.notes);
}

/** A check-in read back in a few words: "Hard · legs heavy · niggle: knee". */
export function feltText(c: CheckIn, session = false): string {
  const parts: string[] = [];
  if (c.effort) parts.push(label('effort', c.effort));
  if (c.legs) parts.push(`${session ? 'body' : 'legs'} ${label('legs', c.legs).toLowerCase()}`);
  if (c.plan) parts.push(label('plan', c.plan).toLowerCase());
  if (c.pain === 'niggle' || c.pain === 'pain') {
    const where = (c.where ?? []).map(partLabel).join(', ');
    parts.push(`${c.pain}${where ? `: ${where}` : ''}`);
  }
  if (c.fuel && c.fuel !== 'good') parts.push(label('fuel', c.fuel).toLowerCase());
  const s = parts.join(' · ');
  return s ? s[0]!.toUpperCase() + s.slice(1) : '';
}

const NOUN: Record<string, string> = { ride: 'ride', virtual_ride: 'ride', strength: 'session', run: 'run', walk: 'walk', hike: 'hike', swim: 'swim' };

/** "How was this morning's ride?", "How was yesterday's run?" */
export function askText(sport: string, startMs: number, now = new Date()): string {
  const at = new Date(startMs);
  const noun = NOUN[sport] ?? 'workout';
  const days = Math.round(
    (new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime() - new Date(at.getFullYear(), at.getMonth(), at.getDate()).getTime()) / 86_400_000,
  );
  const h = at.getHours();
  const part = h < 12 ? 'morning' : h < 17 ? 'afternoon' : h < 21 ? 'evening' : 'night';
  let when: string;
  if (days === 0) when = part === 'night' ? 'tonight' : `this ${part}`;
  else if (days === 1) when = part === 'night' ? 'last night' : part === 'morning' ? 'yesterday morning' : `yesterday ${part}`;
  else when = at.toLocaleDateString('en-GB', { weekday: 'long' });
  return `How was ${when}'s ${noun}?`;
}
