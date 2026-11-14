// Run with `npm test` (Node's own test runner; Node strips the types).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { addDays, formToPlan, minutesText, rideMeta, type RideForm } from '../src/lib/rides.ts';

test('the ride reads as kind, length, intensity and target', () => {
  assert.equal(rideMeta({ kind: 'indoor', duration_min: 90, intensity: 'threshold', tss: 95 }), 'Indoor · 1 h 30 · Threshold · 95 TSS');
  assert.equal(rideMeta({ kind: 'outdoor', duration_min: 45, intensity: '', tss: 0 }), 'Outdoor · 45 min');
  assert.equal(rideMeta({ kind: 'outdoor', duration_min: 120, intensity: 'vo2', tss: 0 }, false), '2 h · VO2 max');
  assert.equal(minutesText(185), '3 h 05');
});

test('days count on across months and years', () => {
  assert.equal(addDays('2026-11-30', 1), '2026-12-01');
  assert.equal(addDays('2026-12-31', 1), '2027-01-01');
  assert.equal(addDays('2026-11-13', 13), '2026-11-26');
});

const form = (o: Partial<RideForm> = {}): RideForm => ({
  date: '2026-11-14', name: ' Zwift:  Jungle Circuit ', kind: 'indoor', minutes: '60', intensity: 'tempo', tss: '', notes: ' ', ...o,
});

test('the form becomes a plan', () => {
  assert.deepEqual(formToPlan(form(), '2026-11-13'), {
    date: '2026-11-14', name: 'Zwift: Jungle Circuit', kind: 'indoor', duration_min: 60, tss: 0, intensity: 'tempo', notes: '',
  });
  assert.equal((formToPlan(form({ tss: '70' }), '2026-11-13') as { tss: number }).tss, 70);
});

test('the form says what is wrong', () => {
  assert.match(formToPlan(form({ date: '2026-11-12' }), '2026-11-13') as string, /today or a day to come/);
  assert.match(formToPlan(form({ name: '  ' }), '2026-11-13') as string, /name/);
  assert.match(formToPlan(form({ minutes: '5' }), '2026-11-13') as string, /10 to 600/);
  assert.match(formToPlan(form({ minutes: '1.5' }), '2026-11-13') as string, /10 to 600/);
  assert.match(formToPlan(form({ tss: 'lots' }), '2026-11-13') as string, /TSS/);
});
