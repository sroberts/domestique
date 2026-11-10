// Run with `npm test` (Node's own test runner; Node strips the types).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { correctionCarries, loggedAt, stepNext, targetWith, weightStep } from '../src/lib/weights.ts';
import type { Cursor } from '../src/lib/types.ts';

const cursor = (phase: Cursor['phase'], weight_override: Record<number, number> = {}) => ({ phase, weight_override });
// Squat 3 × 5 at 100 kg, then a circuit: A = row 3 × 8 at 50 kg, B = press-up 3 × 10 at bodyweight.
const squat = (set: number) => ({ exerciseIndex: 0, setIndex: set, target: { reps: 5, weight_kg: 100 } });
const row = (round: number) => ({ exerciseIndex: 1, setIndex: round, target: { reps: 8, weight_kg: 50 } });
const pressUp = (round: number) => ({ exerciseIndex: 2, setIndex: round, target: { reps: 10, weight_kg: 0 } });

test('straight sets: the next set goes up before it starts, and the rest of the exercise follows', () => {
  for (const phase of ['ready', 'rest'] as const) {
    const c = cursor(phase);
    const kg = stepNext(c, squat(1), 1);
    assert.equal(kg, 102.5, phase);
    c.weight_override[0] = kg!;
    assert.deepEqual(targetWith(squat(1), c.weight_override), { reps: 5, weight_kg: 102.5 });
    assert.deepEqual(targetWith(squat(2), c.weight_override), { reps: 5, weight_kg: 102.5 });
    // The set records the plan's weight as its target, and the new weight as lifted.
    assert.deepEqual(loggedAt(squat(1), c.weight_override), { target_reps: 5, target_weight_kg: 100, reps: 5, weight_kg: 102.5 });
  }
});

test('the weight can only be changed before the set starts', () => {
  assert.equal(stepNext(cursor('active'), squat(0), 1), null);
  assert.equal(stepNext(cursor('last'), squat(0), 1), null);
  assert.equal(stepNext(cursor('ready'), null, 1), null);
});

test('circuit: resting after A, the change is for B and leaves A alone', () => {
  const c = cursor('rest', { 1: 52.5 });
  const kg = stepNext(c, pressUp(0), 1);
  assert.equal(kg, 1); // bodyweight plus 1 kg
  c.weight_override[2] = kg!;
  assert.equal(targetWith(row(1), c.weight_override).weight_kg, 52.5);
  assert.equal(targetWith(pressUp(1), c.weight_override).weight_kg, 1);
  // Back down to bodyweight, and no further.
  c.weight_override[2] = stepNext(c, pressUp(0), -1)!;
  assert.equal(c.weight_override[2], 0);
  assert.equal(stepNext(c, pressUp(0), -1), null);
});

test('the step is 1 kg under 20 kg and 2.5 kg from 20 up', () => {
  assert.equal(weightStep(19, 1), 1);
  assert.equal(weightStep(20, 1), 2.5);
  assert.equal(weightStep(20, -1), 1);
  assert.equal(weightStep(22.5, -1), 2.5);
  const at = (kg: number, dir: -1 | 1) => stepNext(cursor('ready', { 0: kg }), squat(0), dir);
  assert.equal(at(19, 1), 20);
  assert.equal(at(20, -1), 19);
  assert.equal(at(20, 1), 22.5);
  assert.equal(at(1000, 1), null);
});

test('correcting the set just logged carries forward, unless the next weight was chosen', () => {
  assert.equal(correctionCarries(undefined, 100), true); // nothing chosen yet
  assert.equal(correctionCarries(100, 100), true); // carried from an earlier change
  assert.equal(correctionCarries(105, 100), false); // the next set was set to 105 on purpose
});

test('a change survives a reload: it lives on the saved cursor', () => {
  const c = cursor('rest');
  c.weight_override[0] = stepNext(c, squat(1), 1)!;
  const back = structuredClone(JSON.parse(JSON.stringify(c))) as typeof c;
  assert.deepEqual(targetWith(squat(2), back.weight_override), { reps: 5, weight_kg: 102.5 });
  assert.equal(stepNext(back, squat(1), 1), 105);
});
