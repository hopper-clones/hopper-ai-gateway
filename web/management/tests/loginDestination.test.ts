import { describe, expect, test } from 'bun:test';
import { loginDestination } from '../src/utils/loginDestination';

describe('console sign-in destination', () => {
  test('preserves a requested page and its query', () => {
    expect(
      loginDestination({ from: { pathname: '/auth-files', search: '?provider=claude' } })
    ).toBe('/auth-files?provider=claude');
    expect(loginDestination({ from: { pathname: '/quota' } })).toBe('/quota');
  });
  test('opens the ledger for a new session and rejects external or login redirects', () => {
    for (const state of [
      null,
      {},
      { from: { pathname: '//external.test' } },
      { from: { pathname: '/login' } },
    ]) {
      expect(loginDestination(state)).toBe('/quota');
    }
  });
});
