import { expect, test } from 'bun:test';
import { capacityGroups, currentCapacityRemaining } from '@/features/quota/capacityModel';
import type { CapacityAccount } from '@/services/api/capacity';

const now = Date.parse('2026-10-07T12:00:00Z');
const meter = {
  id: 'm',
  label: '5h',
  model: null,
  remaining: 40,
  state: 'fresh',
  resetsAt: '2026-10-07T13:00:00Z',
  kind: 'quota',
};
const row: CapacityAccount = {
  id: 'a',
  provider: 'claude',
  label: 'Claude 1',
  plan: null,
  observedAt: '2026-10-07T11:59:00Z',
  freshness: 'fresh',
  meters: [meter],
  resetCreditsAvailable: null,
  resetCredits: null,
  usageCredits: null,
};
test('saved, expired, future and blocked observations never become current capacity', () => {
  expect(currentCapacityRemaining(row, meter, now)).toBe(40);
  for (const freshness of ['stale', 'pending', 'unavailable', 'reauth'])
    expect(currentCapacityRemaining({ ...row, freshness }, meter, now)).toBeNull();
  expect(currentCapacityRemaining(row, { ...meter, state: 'blocked' }, now)).toBeNull();
  expect(
    currentCapacityRemaining(row, { ...meter, resetsAt: '2026-10-07T11:00:00Z' }, now)
  ).toBeNull();
  expect(
    currentCapacityRemaining({ ...row, observedAt: '2026-10-07T11:00:00Z' }, meter, now)
  ).toBeNull();
  expect(
    currentCapacityRemaining({ ...row, observedAt: '2026-10-08T11:00:00Z' }, meter, now)
  ).toBeNull();
});
test('all account providers survive including providers the gateway cannot route', () => {
  const rows = Array.from({ length: 701 }, (_, i) => ({
    ...row,
    id: String(i),
    provider: ['claude', 'codex', 'cursor', 'grok', 'windsurf'][i % 5],
  }));
  expect(capacityGroups(rows).flatMap(([, accounts]) => accounts)).toHaveLength(701);
  expect(capacityGroups(rows).map(([provider]) => provider)).toContain('windsurf');
});
