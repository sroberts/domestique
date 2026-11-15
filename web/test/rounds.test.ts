// Run with `npm test` (Node's own test runner; Node strips the types).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { anotherRound, endableCircuit, slotsOf, withAnotherRound, withCircuitEnded } from '../src/lib/rounds.ts';
import type { Cursor, Exercise, LocalSession, SetRecord } from '../src/lib/types.ts';

// Squat 2 × 5, then circuit a: swing and push-up for 3 rounds with 90 s
// between rounds, then a curl 2 × 10.
const plan = (): Exercise[] => [
  { name: 'Squat', rest_seconds: 120, sets: [{ reps: 5, weight_kg: 100 }, { reps: 5, weight_kg: 100 }] },
  { name: 'Swing', rest_seconds: 0, circuit: 'a', sets: [1, 2, 3].map(() => ({ reps: 15, weight_kg: 24 })) },
  { name: 'Push-up', rest_seconds: 90, circuit: 'a', sets: [1, 2, 3].map(() => ({ reps: 12, weight_kg: 0 })) },
  { name: 'Curl', rest_seconds: 60, sets: [{ reps: 10, weight_kg: 12 }, { reps: 10, weight_kg: 12 }] },
];

/** A session with the first n slots done (or skipped, when listed). */
function at(n: number, phase: Cursor['phase'] = 'rest', skipped: number[] = [], p = plan()) {
  const slots = slotsOf(p);
  const sets: SetRecord[] = slots
    .slice(0, n)
    .filter((s) => !skipped.includes(s.index))
    .map((s, i) => ({
      id: `s${s.index}`, session_id: 'x', exercise_index: s.exerciseIndex, exercise_name: s.exercise.name, set_index: s.setIndex,
      target_reps: s.target.reps, target_weight_kg: s.target.weight_kg, reps: s.target.reps, weight_kg: s.target.weight_kg,
      started_at: i * 60_000, ended_at: i * 60_000 + 30_000, updated_at: 0,
    }));
  const session: LocalSession = {
    id: 'x', template_id: 't', name: 'W', plan: p, started_at: 0, finished_at: null, updated_at: 0,
    cursor: { index: n, phase, phase_started_at: 1, rest_s: 90, weight_override: {}, skipped, last_set_id: sets.at(-1)?.id ?? null },
  };
  return { session, sets };
}

test('another round is offered only once the last planned round is done', () => {
  // Slots: squat 0-1, circuit 2-7 (A1 B1 A2 B2 A3 B3), curl 8-9.
  for (const n of [1, 3, 4, 7, 9]) {
    const { session, sets } = at(n);
    assert.equal(anotherRound(session, sets), null, `after ${n} slots`);
  }
  const { session, sets } = at(8);
  assert.equal(anotherRound(session, sets)?.start, 1);
  assert.equal(anotherRound({ ...session, cursor: { ...session.cursor, phase: 'active' } }, sets), null);
  assert.equal(anotherRound({ ...session, cursor: { ...session.cursor, ended: [1] } }, sets), null);
});

test('adding a round repeats each last set and keeps the place', () => {
  const { session, sets } = at(8, 'rest', [9]);
  const r = withAnotherRound(session, sets, 5)!;
  assert.equal(r.plan[1]!.sets.length, 4);
  assert.equal(r.plan[2]!.sets.length, 4);
  assert.deepEqual(r.plan[2]!.sets[3], { reps: 12, weight_kg: 0 });
  assert.equal(session.plan[1]!.sets.length, 3, 'the old plan is untouched');
  const slots = slotsOf(r.plan);
  // Next up is round 4's swing; the skipped curl set moved along with it.
  assert.equal(slots[r.cursor.index]!.exercise.name, 'Swing');
  assert.equal(slots[r.cursor.index]!.setIndex, 3);
  assert.deepEqual(r.cursor.skipped.map((i) => `${slots[i]!.exercise.name} ${slots[i]!.setIndex}`), ['Curl 1']);
  assert.equal(r.cursor.phase, 'rest', 'the round rest already running carries on');
  assert.equal(r.cursor.phase_started_at, 1);
});

test('a round added at the end of the session starts the round rest', () => {
  const p = plan().slice(0, 3);
  const { session, sets } = at(8, 'last', [], p);
  const r = withAnotherRound(session, sets, 999)!;
  assert.equal(r.cursor.phase, 'rest');
  assert.equal(r.cursor.rest_s, 90);
  assert.equal(r.cursor.phase_started_at, sets.at(-1)!.ended_at);
  assert.equal(r.cursor.index, 8);
});

test('an exercise skipped in the last round stays skipped in the new one', () => {
  // Push-up skipped from round 2 on (slots 5 and 7).
  const { session, sets } = at(8, 'rest', [5, 7]);
  const r = withAnotherRound(session, sets, 5)!;
  const slots = slotsOf(r.plan);
  assert.deepEqual(r.cursor.skipped.map((i) => `${slots[i]!.exercise.name} ${slots[i]!.setIndex}`), ['Push-up 1', 'Push-up 2', 'Push-up 3']);
  assert.equal(slots[r.cursor.index]!.exercise.name, 'Swing');
});

test('ending a circuit keeps the rounds started and moves on', () => {
  assert.equal(endableCircuit(at(2).session, at(2).sets), null, 'nothing of the circuit done yet');
  // Resting after round 1: end with 1 round.
  const between = at(4);
  assert.deepEqual(endableCircuit(between.session, between.sets)?.rounds, 1);
  const r = withCircuitEnded(between.session, between.sets, 5)!;
  assert.equal(r.plan[1]!.sets.length, 1);
  assert.equal(r.plan[2]!.sets.length, 1);
  const slots = slotsOf(r.plan);
  assert.equal(slots[r.cursor.index]!.exercise.name, 'Curl');
  assert.deepEqual(r.cursor.skipped, []);
  assert.deepEqual(r.cursor.ended, [1]);
  assert.equal(r.cursor.phase, 'rest');
  // Once ended, it isn't offered another round.
  const after = { ...between.session, plan: r.plan, cursor: r.cursor };
  assert.equal(anotherRound(after, between.sets), null);
});

test('ending mid-round skips what is left of that round', () => {
  // A2 done, B2 next.
  const { session, sets } = at(5, 'ready');
  const r = withCircuitEnded(session, sets, 7)!;
  assert.equal(r.plan[1]!.sets.length, 2);
  const slots = slotsOf(r.plan);
  assert.deepEqual(r.cursor.skipped.map((i) => `${slots[i]!.exercise.name} ${slots[i]!.setIndex}`), ['Push-up 1']);
  assert.equal(slots[r.cursor.index]!.exercise.name, 'Curl');
  assert.equal(r.cursor.phase_started_at, 7);
});

test('ending a circuit that closes the session leaves only Finish', () => {
  const { session, sets } = at(4, 'rest', [], plan().slice(0, 3));
  const r = withCircuitEnded(session, sets, 5)!;
  assert.equal(r.cursor.index, slotsOf(r.plan).length);
  assert.equal(r.cursor.phase, 'last');
});
