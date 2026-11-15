// Run with `npm test` (Node's own test runner; Node strips the types).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { answered, askText, feltText, questionTexts } from '../src/lib/checkin.ts';

test('a check-in reads back in a few words', () => {
  assert.equal(feltText({ effort: 'very_hard', legs: 'heavy', plan: 'cut_short', pain: 'niggle', where: ['knee', 'back'], fuel: 'low_energy' }),
    'Very hard · legs heavy · cut short · niggle: knee, back · low energy');
  assert.equal(feltText({ effort: 'easy', legs: 'fresh', pain: 'none', fuel: 'good' }), 'Easy · legs fresh');
  assert.equal(feltText({ legs: 'dead' }, true), 'Body dead');
  assert.equal(feltText({ pain: 'pain' }), 'Pain');
});

test('only answers count as answered; a skip does not', () => {
  assert.equal(answered(null), false);
  assert.equal(answered({ skipped: true }), false);
  assert.equal(answered({ notes: 'windy' }), true);
  assert.equal(answered({ effort: 'hard' }), true);
});

test('a lifting session asks about the body', () => {
  assert.deepEqual(questionTexts(['effort', 'legs'], true).map((q) => q.label), ['How hard?', 'Body?']);
  assert.deepEqual(questionTexts(['legs', 'fuel']).map((q) => q.label), ['Legs?', 'Fuel?']);
});

test('the prompt names when and what', () => {
  const now = new Date(2026, 10, 14, 15, 0);
  assert.equal(askText('ride', new Date(2026, 10, 14, 7, 30).getTime(), now), "How was this morning's ride?");
  assert.equal(askText('virtual_ride', new Date(2026, 10, 13, 18, 0).getTime(), now), "How was yesterday evening's ride?");
  assert.equal(askText('strength', new Date(2026, 10, 13, 22, 0).getTime(), now), "How was last night's session?");
  assert.equal(askText('rowing', new Date(2026, 10, 12, 9, 0).getTime(), now), "How was Thursday's workout?");
});
