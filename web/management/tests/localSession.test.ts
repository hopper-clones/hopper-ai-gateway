import { describe, expect, test } from 'bun:test';
import {
  consumeLocalSessionFragment,
  isLocalSessionOrigin,
} from '../src/services/api/localSession';

describe('local launcher session', () => {
  test('erases launch capability before routing while retaining destination', () => {
    let cleaned = '';
    const capability = consumeLocalSessionFragment(
      {
        pathname: '/management.html',
        search: '',
        hash: '#/quota?provider=codex&local-session=synthetic-capability',
      },
      (url) => {
        cleaned = url;
      }
    );
    expect(capability).toBe('synthetic-capability');
    expect(cleaned).toBe('/management.html#/quota?provider=codex');
    expect(cleaned).not.toContain('synthetic-capability');
  });
  test('does not alter normal routing or read query-string secrets', () => {
    expect(
      consumeLocalSessionFragment(
        { pathname: '/management.html', search: '?local-session=ignored', hash: '#/quota' },
        () => {
          throw new Error('unexpected rewrite');
        }
      )
    ).toBeNull();
  });
  test('limits browser session mode to the exact current literal loopback origin', () => {
    expect(isLocalSessionOrigin('http://127.0.0.1:8317', 'http://127.0.0.1:8317')).toBe(true);
    expect(isLocalSessionOrigin('http://[::1]:8317', 'http://[::1]:8317')).toBe(true);
    for (const base of [
      'http://127.0.0.1:9999',
      'http://localhost:8317',
      'https://remote.example',
      'http://192.0.2.1:8317',
    ]) {
      expect(isLocalSessionOrigin(base, 'http://127.0.0.1:8317')).toBe(false);
    }
  });
});
