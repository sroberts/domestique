// The order of a session's sets, and how a circuit's rounds change once it
// has started. Kept apart from the live session (runes, IndexedDB) so the
// rules can be tested without a browser.
import type { Cursor, Exercise, LocalSession, SetRecord, TargetSet } from './types';

/** Most rounds a circuit can have: store.MaxSetsPerLift. */
export const MAX_ROUNDS = 20;

/** A circuit's letter by its place in the plan: A, B, C. */
export const circuitLetter = (n: number) => String.fromCharCode(65 + (n % 26));

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

/** A changed plan, with the cursor moved to match. */
export interface Replanned {
  plan: Exercise[];
  cursor: Cursor;
}

/** A replan, and whether the cursor ended up past the last set. */
type Remapped = Replanned & { past: boolean };

const keyOf = (s: { exerciseIndex: number; setIndex: number }) => `${s.exerciseIndex}:${s.setIndex}`;
const inCircuit = (c: Circuit, exerciseIndex: number) => exerciseIndex >= c.start && exerciseIndex < c.end;

/** The exercises of a circuit not skipped in round r. */
function liveIn(slots: Slot[], skipped: number[], c: Circuit, r: number): number[] {
  return slots.filter((s) => s.circuit?.start === c.start && s.setIndex === r && !skipped.includes(s.index)).map((s) => s.exerciseIndex);
}

/** Moves the cursor's place and skips from the old slots onto a new plan's,
 * by exercise and set (slot indexes shift when a circuit gains or loses
 * rounds), skipping `skip` too and putting the cursor on `at`, or past the
 * end without one. Then steps past anything skipped. */
function remap(old: Slot[], cursor: Cursor, plan: Exercise[], skip: (s: Slot) => boolean, at: string): Remapped {
  const was = new Set(cursor.skipped.map((i) => old[i] && keyOf(old[i])));
  const next = slotsOf(plan);
  const skipped = next.filter((s) => was.has(keyOf(s)) || skip(s)).map((s) => s.index);
  let index = next.findIndex((s) => keyOf(s) === at);
  if (index < 0) index = next.length;
  while (index < next.length && skipped.includes(index)) index++;
  return { plan, cursor: { ...cursor, skipped, index }, past: index >= next.length };
}

/** The circuit whose last planned round was just done, before anything
 * after it has started: it can take another round. */
export function anotherRound(s: LocalSession, sets: SetRecord[], slots = slotsOf(s.plan)): Circuit | null {
  const c = s.cursor;
  const last = sets.find((x) => x.id === c.last_set_id);
  if (!last || c.phase === 'active') return null;
  const circ = slots.find((x) => x.exerciseIndex === last.exercise_index)?.circuit;
  if (!circ || last.set_index !== circ.rounds - 1 || circ.rounds >= MAX_ROUNDS || c.ended?.includes(circ.start)) return null;
  if (slots[c.index]?.circuit?.start === circ.start) return null;
  return liveIn(slots, c.skipped, circ, circ.rounds - 1).length > 0 ? circ : null;
}

/** The circuit under way, once a round has started: it can end, keeping
 * the rounds started. */
export function endableCircuit(s: LocalSession, sets: SetRecord[], slots = slotsOf(s.plan)): { circuit: Circuit; rounds: number } | null {
  const slot = slots[s.cursor.index];
  if (!slot?.circuit || s.cursor.phase === 'active') return null;
  const circ = slot.circuit;
  const done = sets.filter((x) => inCircuit(circ, x.exercise_index));
  if (done.length === 0) return null;
  return { circuit: circ, rounds: Math.max(...done.map((x) => x.set_index)) + 1 };
}

/** One more round of the circuit just finished: each exercise repeats its
 * last set, one skipped in the last round stays skipped, and the round rest
 * runs before it. `now` starts that rest when none was running. */
export function withAnotherRound(s: LocalSession, sets: SetRecord[], now: number): Replanned | null {
  const old = slotsOf(s.plan);
  const circ = anotherRound(s, sets, old);
  if (!circ) return null;
  const r = circ.rounds;
  const live = liveIn(old, s.cursor.skipped, circ, r - 1);
  const plan = structuredClone(s.plan);
  for (let e = circ.start; e < circ.end; e++) {
    const xs = plan[e]!.sets;
    xs.push({ ...xs[xs.length - 1]! });
  }
  const out = remap(old, s.cursor, plan, (x) => x.setIndex === r && inCircuit(circ, x.exerciseIndex) && !live.includes(x.exerciseIndex), `${live[0]}:${r}`);
  const c = out.cursor;
  if (c.phase === 'last' || c.phase === 'ready') {
    // No rest was running (the circuit ended the session, or a round rest
    // of 0): the round rest runs now, from the last set.
    c.rest_s = plan[circ.end - 1]!.rest_seconds;
    c.phase = c.rest_s > 0 ? 'rest' : 'ready';
    c.phase_started_at = c.phase === 'rest' ? (sets.find((x) => x.id === c.last_set_id)?.ended_at ?? now) : now;
  }
  return out;
}

/** End the circuit under way: the plan keeps the rounds started, what's
 * left of the last one is skipped, and the session moves on to whatever
 * follows the circuit (any rest running carries on). */
export function withCircuitEnded(s: LocalSession, sets: SetRecord[], now: number): Replanned | null {
  const old = slotsOf(s.plan);
  const end = endableCircuit(s, sets, old);
  if (!end) return null;
  const { circuit: circ, rounds } = end;
  const plan = structuredClone(s.plan);
  for (let e = circ.start; e < circ.end; e++) plan[e]!.sets.splice(rounds);
  const done = new Set(sets.map((x) => `${x.exercise_index}:${x.set_index}`));
  const after = old.find((x) => x.exerciseIndex >= circ.end);
  const out = remap(old, s.cursor, plan, (x) => inCircuit(circ, x.exerciseIndex) && !done.has(keyOf(x)), after ? keyOf(after) : '');
  const c = out.cursor;
  c.ended = [...(c.ended ?? []), circ.start];
  if (out.past) c.phase = 'last';
  else if (c.phase === 'ready') c.phase_started_at = now;
  return out;
}
