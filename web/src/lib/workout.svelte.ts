// The live session: where the athlete is, and what one tap does next.
// Every transition is persisted to IndexedDB before the UI moves on, so a
// reload, a crash or a dead battery resumes exactly where it stopped.
import { discardSession, getSession, saveCursor, saveSession, saveSet, sessionSets } from './db';
import { sync } from './sync.svelte';
import type { Exercise, LocalSession, Progress, SetRecord, TargetSet, Template } from './types';

export interface Slot {
  index: number;
  exerciseIndex: number;
  setIndex: number;
  exercise: Exercise;
  target: TargetSet;
}

export function slotsOf(plan: Exercise[]): Slot[] {
  const out: Slot[] = [];
  plan.forEach((exercise, exerciseIndex) =>
    exercise.sets.forEach((target, setIndex) => out.push({ index: out.length, exerciseIndex, setIndex, exercise, target })),
  );
  return out;
}

export function newId(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  const b = crypto.getRandomValues(new Uint8Array(16));
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
}

/** Estimated minutes for a plan: ~40s per set plus planned rest. */
export function estimateMinutes(plan: Exercise[]): number {
  let s = 0;
  for (const e of plan) s += e.sets.length * 40 + Math.max(0, e.sets.length - 1) * e.rest_seconds + 90;
  return Math.round(s / 60 / 5) * 5;
}

export async function beginSession(t: Template): Promise<LocalSession> {
  const now = Date.now();
  const s: LocalSession = {
    id: newId(),
    template_id: t.id,
    name: t.name,
    plan: structuredClone($state.snapshot(t.exercises)) as Exercise[],
    started_at: now,
    finished_at: null,
    updated_at: now,
    cursor: { index: 0, phase: 'ready', phase_started_at: now, rest_s: 0, weight_override: {}, skipped: [], last_set_id: null },
  };
  await saveSession(s);
  sync.kick();
  return s;
}

export class Workout {
  session = $state() as LocalSession;
  sets = $state<SetRecord[]>([]);
  slots = $derived(slotsOf(this.session.plan));

  current = $derived(this.slots[this.session.cursor.index] ?? null);
  lastSet = $derived(this.sets.find((s) => s.id === this.session.cursor.last_set_id) ?? null);
  /** Planned sets that are neither done nor skipped. */
  remaining = $derived(this.slots.length - this.session.cursor.index);

  constructor(session: LocalSession, sets: SetRecord[]) {
    this.session = session;
    this.sets = sets;
  }

  static async load(id: string): Promise<Workout | null> {
    const s = await getSession(id);
    if (!s) return null;
    return new Workout(s, await sessionSets(id));
  }

  /** Target for a slot, with any carried-forward weight applied. */
  target(slot: Slot): TargetSet {
    const w = this.session.cursor.weight_override[slot.exerciseIndex];
    return w === undefined ? slot.target : { reps: slot.target.reps, weight_kg: w };
  }

  /** Sets done in the current exercise, for the set ladder. */
  doneIn(exerciseIndex: number): SetRecord[] {
    return this.sets.filter((s) => s.exercise_index === exerciseIndex);
  }

  async startSet(): Promise<void> {
    const c = this.session.cursor;
    if (!this.current || c.phase === 'active' || c.phase === 'last') return;
    c.phase = 'active';
    c.phase_started_at = Date.now();
    await saveCursor($state.snapshot(this.session) as LocalSession);
  }

  /** Finish the active set: it's logged at target and can be adjusted during rest. */
  async finishSet(): Promise<void> {
    const c = this.session.cursor;
    const slot = this.current;
    if (!slot || c.phase !== 'active') return;
    const now = Date.now();
    const t = this.target(slot);
    const rec: SetRecord = {
      id: newId(),
      session_id: this.session.id,
      exercise_index: slot.exerciseIndex,
      exercise_name: slot.exercise.name,
      set_index: slot.setIndex,
      target_reps: slot.target.reps,
      target_weight_kg: slot.target.weight_kg,
      reps: t.reps,
      weight_kg: t.weight_kg,
      started_at: c.phase_started_at,
      ended_at: now,
      updated_at: now,
    };
    await saveSet(rec);
    this.sets.push(rec);
    c.last_set_id = rec.id;
    c.index += 1;
    c.phase_started_at = now;
    if (c.index < this.slots.length) {
      c.phase = 'rest';
      c.rest_s = slot.exercise.rest_seconds;
    } else {
      c.phase = 'last';
    }
    await saveCursor($state.snapshot(this.session) as LocalSession);
    sync.kick();
  }

  /** Correct the set just logged. Weight changes carry forward to the rest of that exercise. */
  async adjustLast(field: 'reps' | 'weight_kg', delta: number): Promise<void> {
    const last = this.lastSet;
    if (!last) return;
    const next = field === 'reps' ? Math.max(0, Math.min(100, last.reps + delta)) : Math.max(0, Math.min(1000, last.weight_kg + delta));
    if (next === last[field]) return;
    last[field] = next;
    last.updated_at = Date.now();
    if (field === 'weight_kg') this.session.cursor.weight_override[last.exercise_index] = next;
    await saveSet($state.snapshot(last) as SetRecord);
    await saveCursor($state.snapshot(this.session) as LocalSession);
    sync.kick();
  }

  /** Edit any logged set after the fact (from the summary). */
  async editSet(id: string, reps: number, weightKg: number): Promise<void> {
    const s = this.sets.find((x) => x.id === id);
    if (!s) return;
    s.reps = Math.max(0, Math.min(100, reps));
    s.weight_kg = Math.max(0, Math.min(1000, weightKg));
    s.updated_at = Date.now();
    await saveSet($state.snapshot(s) as SetRecord);
    sync.kick();
  }

  async addRest(seconds: number): Promise<void> {
    const c = this.session.cursor;
    if (c.phase !== 'rest') return;
    c.rest_s = Math.max(15, c.rest_s + seconds);
    await saveCursor($state.snapshot(this.session) as LocalSession);
  }

  /** Skip what's left of the current exercise. */
  async skipExercise(): Promise<void> {
    const c = this.session.cursor;
    const slot = this.current;
    if (!slot || c.phase === 'active') return;
    while (this.current && this.current.exerciseIndex === slot.exerciseIndex) {
      c.skipped.push(c.index);
      c.index += 1;
    }
    if (c.index >= this.slots.length) {
      if (this.sets.length > 0) c.phase = 'last';
      else return this.finish();
    } else {
      c.phase = 'ready';
    }
    c.phase_started_at = Date.now();
    await saveCursor($state.snapshot(this.session) as LocalSession);
  }

  /** Throw the session away entirely: it was started by mistake. */
  async discard(): Promise<void> {
    await discardSession(this.session.id);
    sync.kick();
  }

  /** End the session. An unfinished active set is discarded, not logged. */
  async finish(): Promise<void> {
    const now = Date.now();
    this.session.finished_at = now;
    this.session.updated_at = now;
    await saveSession($state.snapshot(this.session) as LocalSession);
    sync.kick();
  }
}

/** Mirrors strength.Real on the server: a set logged faster than 5 s, or
 * 2 s a rep, was tapped through rather than lifted. */
export function isReal(s: SetRecord): boolean {
  return s.ended_at - s.started_at >= Math.max(5, 2 * s.reps) * 1000;
}

export function progressOf(plan: Exercise[], sets: SetRecord[]): Progress {
  return { done: sets.filter(isReal).length, logged: sets.length, planned: plan.reduce((n, e) => n + e.sets.length, 0) };
}

/** "Ended early · 3 of 12 sets", noting any that were too quick to count. */
export function earlyText(p: Progress): string {
  const quick = p.logged - p.done;
  return `Ended early · ${p.done} of ${p.planned} sets` + (quick > 0 ? ` (${quick} too quick to count)` : '');
}

/** True when the session lifted under the plan: shown as "Ended early". */
export const endedEarly = (p: Progress | undefined) => !!p && p.planned > 0 && p.done < p.planned;

/** Enough lifted to call it done: half the planned sets, as progression counts it. */
export const complete = (p: Progress | undefined) => !p || p.planned === 0 || p.done * 2 >= p.planned;
