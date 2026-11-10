// The live session: where the athlete is, and what one tap does next.
// Every transition is persisted to IndexedDB before the UI moves on, so a
// reload, a crash or a dead battery resumes exactly where it stopped.
import { discardSession, getSession, saveCursor, saveSession, saveSet, sessionSets } from './db';
import { sync } from './sync.svelte';
import type { Exercise, LocalSession, Progress, SetRecord, TargetSet, Template } from './types';
import { correctionCarries, loggedAt, stepNext, targetWith } from './weights';

/** A run of adjacent exercises sharing a circuit key: plan[start, end). */
export interface Circuit {
  key: string;
  start: number;
  end: number;
  rounds: number;
}

export interface Slot {
  index: number;
  exerciseIndex: number;
  /** Set number within the exercise; in a circuit, also the round. */
  setIndex: number;
  exercise: Exercise;
  target: TargetSet;
  circuit?: Circuit;
}

/** Mirrors store.Circuits on the server. */
export function circuitsOf(plan: Exercise[]): Circuit[] {
  const out: Circuit[] = [];
  for (let i = 0; i < plan.length; ) {
    const key = plan[i]!.circuit;
    if (!key) {
      i++;
      continue;
    }
    let j = i + 1;
    while (j < plan.length && plan[j]!.circuit === key) j++;
    out.push({ key, start: i, end: j, rounds: plan[i]!.sets.length });
    i = j;
  }
  return out;
}

/** The planned sets in the order they're done: straight sets exercise by
 * exercise, circuits round by round (A1 B1 C1, A2 B2 C2). */
export function slotsOf(plan: Exercise[]): Slot[] {
  const out: Slot[] = [];
  const push = (exerciseIndex: number, setIndex: number, circuit?: Circuit) => {
    const exercise = plan[exerciseIndex]!;
    const target = exercise.sets[setIndex];
    if (target) out.push({ index: out.length, exerciseIndex, setIndex, exercise, target, circuit });
  };
  const circuits = circuitsOf(plan);
  for (let i = 0; i < plan.length; ) {
    const c = circuits.find((x) => x.start === i);
    if (c) {
      // Rounds from the longest member, so a plan cut short on an easy day
      // (or an older plan) still covers every set.
      const rounds = Math.max(...plan.slice(c.start, c.end).map((e) => e.sets.length));
      for (let r = 0; r < rounds; r++) for (let e = c.start; e < c.end; e++) push(e, r, c);
      i = c.end;
    } else {
      plan[i]!.sets.forEach((_, setIndex) => push(i, setIndex));
      i++;
    }
  }
  return out;
}

/** Where a logged set sits in a circuit: "A2", round 3 of 4. */
export interface CircuitPlace {
  letter: string;
  pos: string;
  round: number;
  rounds: number;
}

/** One round of a circuit as it was done: from its first set's start to its
 * last set's end. */
export interface Round {
  letter: string;
  round: number;
  rounds: number;
  from: number;
  to: number;
  ids: string[];
}

/** Places each logged set in its circuit and groups circuit sets into the
 * rounds actually done, in time order. Straight sets are left out. */
export function roundsOf(plan: Exercise[], sets: SetRecord[]): { places: Map<string, CircuitPlace>; rounds: Round[] } {
  const circuits = circuitsOf(plan);
  const places = new Map<string, CircuitPlace>();
  const byKey = new Map<string, Round>();
  const rounds: Round[] = [];
  for (const s of [...sets].sort((a, b) => a.started_at - b.started_at)) {
    const ci = circuits.findIndex((c) => s.exercise_index >= c.start && s.exercise_index < c.end);
    const c = circuits[ci];
    if (!c) continue;
    const letter = String.fromCharCode(65 + (ci % 26));
    places.set(s.id, { letter, pos: `${letter}${s.exercise_index - c.start + 1}`, round: s.set_index + 1, rounds: c.rounds });
    const key = `${ci}:${s.set_index}`;
    let r = byKey.get(key);
    if (!r) {
      r = { letter, round: s.set_index + 1, rounds: c.rounds, from: s.started_at, to: s.ended_at, ids: [] };
      byKey.set(key, r);
      rounds.push(r);
    }
    r.from = Math.min(r.from, s.started_at);
    r.to = Math.max(r.to, s.ended_at);
    r.ids.push(s.id);
  }
  return { places, rounds };
}

/** True when b follows a in the same round of the same circuit. */
const sameRound = (a: Slot, b: Slot) => !!a.circuit && b.circuit?.start === a.circuit.start && b.setIndex === a.setIndex;

export function newId(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  const b = crypto.getRandomValues(new Uint8Array(16));
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
}

/** Estimated minutes for a plan: ~40s per set, the planned rest between
 * sets of an exercise or circuit, and 90s to move on to the next. */
export function estimateMinutes(plan: Exercise[]): number {
  const slots = slotsOf(plan);
  let s = slots.length * 40 + 90;
  for (let i = 1; i < slots.length; i++) {
    const a = slots[i - 1]!;
    const b = slots[i]!;
    const together = a.exerciseIndex === b.exerciseIndex || (!!a.circuit && b.circuit?.start === a.circuit.start);
    s += together ? a.exercise.rest_seconds : 90;
  }
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

  /** The next planned set after index that wasn't skipped. */
  nextAfter(index: number): Slot | null {
    const skipped = this.session.cursor.skipped;
    for (let i = index + 1; i < this.slots.length; i++) if (!skipped.includes(i)) return this.slots[i]!;
    return null;
  }

  /** Move the cursor past skipped sets. */
  private skipForward(): void {
    const c = this.session.cursor;
    while (c.index < this.slots.length && c.skipped.includes(c.index)) c.index += 1;
  }

  /** Target for a slot, with any carried-forward weight applied. */
  target(slot: Slot): TargetSet {
    return targetWith(slot, this.session.cursor.weight_override);
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
    const rec: SetRecord = {
      id: newId(),
      session_id: this.session.id,
      exercise_index: slot.exerciseIndex,
      exercise_name: slot.exercise.name,
      set_index: slot.setIndex,
      ...loggedAt(slot, c.weight_override),
      started_at: c.phase_started_at,
      ended_at: now,
      updated_at: now,
    };
    await saveSet(rec);
    this.sets.push(rec);
    c.last_set_id = rec.id;
    c.index += 1;
    this.skipForward();
    c.phase_started_at = now;
    const next = this.slots[c.index];
    if (next) {
      // In a circuit the rest is this exercise's, until the round ends (even
      // when the rest of it was skipped): then it's the circuit's last
      // exercise's, the rest between rounds.
      let rest = slot.exercise.rest_seconds;
      if (slot.circuit && !sameRound(slot, next)) rest = this.session.plan[slot.circuit.end - 1]!.rest_seconds;
      c.rest_s = rest;
      // No rest means straight on to the next exercise of the round.
      c.phase = rest > 0 ? 'rest' : 'ready';
    } else {
      c.phase = 'last';
    }
    await saveCursor($state.snapshot(this.session) as LocalSession);
    sync.kick();
  }

  /** Correct the set just logged. Weight changes carry forward to the rest
   * of that exercise, unless its next set's weight was chosen separately. */
  async adjustLast(field: 'reps' | 'weight_kg', delta: number): Promise<void> {
    const last = this.lastSet;
    if (!last) return;
    const next = field === 'reps' ? Math.max(0, Math.min(100, last.reps + delta)) : Math.max(0, Math.min(1000, last.weight_kg + delta));
    if (next === last[field]) return;
    const override = this.session.cursor.weight_override;
    if (field === 'weight_kg' && correctionCarries(override[last.exercise_index], last.weight_kg)) override[last.exercise_index] = next;
    last[field] = next;
    last.updated_at = Date.now();
    await saveSet($state.snapshot(last) as SetRecord);
    await saveCursor($state.snapshot(this.session) as LocalSession);
    sync.kick();
  }

  /** Change the next set's weight before starting it. It carries to the
   * rest of that exercise; the set just logged keeps the weight it has. */
  async adjustNext(dir: -1 | 1): Promise<void> {
    const c = this.session.cursor;
    const slot = this.current;
    const kg = stepNext(c, slot, dir);
    if (kg === null || !slot) return;
    c.weight_override[slot.exerciseIndex] = kg;
    await saveCursor($state.snapshot(this.session) as LocalSession);
  }

  /** Whether the logged weight of this set is carrying to its exercise's next sets. */
  carries(s: SetRecord): boolean {
    return this.session.cursor.weight_override[s.exercise_index] === s.weight_kg;
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

  /** Skip what's left of the current exercise. In a circuit the rounds
   * carry on without it. */
  async skipExercise(): Promise<void> {
    const c = this.session.cursor;
    const slot = this.current;
    if (!slot || c.phase === 'active') return;
    for (const s of this.slots.slice(c.index)) {
      if (s.exerciseIndex === slot.exerciseIndex && !c.skipped.includes(s.index)) c.skipped.push(s.index);
    }
    this.skipForward();
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
