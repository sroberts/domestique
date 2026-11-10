// Weight changes in the middle of a session. Kept apart from the live
// session (runes, IndexedDB) so the rules can be tested without a browser.
import type { Cursor, TargetSet } from './types';

/** The part of a planned set these rules need. */
export interface PlannedSet {
  exerciseIndex: number;
  target: TargetSet;
}

/** A planned set's target, with any weight carried forward for its exercise. */
export function targetWith(slot: PlannedSet, override: Record<number, number>): TargetSet {
  const w = override[slot.exerciseIndex];
  return w === undefined ? slot.target : { reps: slot.target.reps, weight_kg: w };
}

/** One tap of the weight buttons: 1 kg under 20 kg, 2.5 kg from 20 up.
 * Going down from 20 steps by 1, so 20 → 19 and 19 → 20 both work. */
export function weightStep(kg: number, dir: -1 | 1): number {
  return (dir < 0 ? kg - 0.01 : kg) < 20 ? 1 : 2.5;
}

const clampKg = (kg: number) => Math.max(0, Math.min(1000, Math.round(kg * 100) / 100));

/** The weight for the next set after one tap, before it's started: only
 * while getting ready or resting. Null when nothing changes. */
export function stepNext(c: Pick<Cursor, 'phase' | 'weight_override'>, slot: PlannedSet | null, dir: -1 | 1): number | null {
  if (!slot || (c.phase !== 'ready' && c.phase !== 'rest')) return null;
  const now = targetWith(slot, c.weight_override).weight_kg;
  const next = clampKg(now + dir * weightStep(now, dir));
  return next === now ? null : next;
}

/** A weight correction to the set just logged carries to the rest of that
 * exercise, unless its next set's weight was already chosen separately
 * (which shows as a carried weight other than the one that was logged). */
export function correctionCarries(carried: number | undefined, loggedKg: number): boolean {
  return carried === undefined || carried === loggedKg;
}

/** What a finished set records: the plan's target, and the weight used. */
export function loggedAt(slot: PlannedSet, override: Record<number, number>) {
  const t = targetWith(slot, override);
  return { target_reps: slot.target.reps, target_weight_kg: slot.target.weight_kg, reps: t.reps, weight_kg: t.weight_kg };
}
