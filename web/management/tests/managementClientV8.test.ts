import { afterEach, describe, expect, test } from 'bun:test';
import type { AxiosAdapter } from 'axios';
import { apiClient } from '../src/services/api/client';

const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window');
afterEach(() => {
  apiClient.setConfig({ apiBase: '', managementKey: '' });
  if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow);
  else Reflect.deleteProperty(globalThis, 'window');
});

describe('v8 management transport', () => {
  test('uses the v8 prefix, bearer authentication and bare JSON scalar writes', async () => {
    apiClient.setConfig({
      apiBase: 'https://proxy.invalid/gateway/v8/management',
      managementKey: 'fixture-only',
    });
    const adapter: AxiosAdapter = async (config) => {
      expect(config.baseURL).toBe('https://proxy.invalid/gateway/v8/management');
      expect(config.url).toBe('/config/observability/logs/debug');
      expect(config.headers.Authorization).toBe('Bearer fixture-only');
      expect(config.data).toBe('false');
      return { data: { status: 'ok' }, status: 200, statusText: 'OK', headers: {}, config };
    };
    await apiClient.put('/config/observability/logs/debug', false, { adapter });
  });

  test('uses local cookie mode without bearer and omits it after switching to remote', async () => {
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: { location: { origin: 'http://127.0.0.1:8317' }, dispatchEvent: () => true },
    });
    apiClient.setConfig({
      apiBase: 'http://127.0.0.1:8317',
      managementKey: 'must-not-send',
      localSession: true,
    });
    await apiClient.get('/config', {
      adapter: async (config) => {
        expect(config.headers.Authorization).toBeUndefined();
        expect(config.headers['X-Hopper-Local-Session']).toBe('1');
        expect(config.withCredentials).toBe(false);
        return { data: {}, status: 200, statusText: 'OK', headers: {}, config };
      },
    });
    apiClient.setConfig({ apiBase: 'https://remote.invalid', managementKey: 'remote-fixture' });
    await apiClient.get('/config', {
      headers: { 'X-Hopper-Local-Session': '1' },
      adapter: async (config) => {
        expect(config.headers.Authorization).toBe('Bearer remote-fixture');
        expect(config.headers['X-Hopper-Local-Session']).toBeUndefined();
        expect(config.withCredentials).toBe(false);
        return { data: {}, status: 200, statusText: 'OK', headers: {}, config };
      },
    });
    expect(() =>
      apiClient.setConfig({
        apiBase: 'http://127.0.0.1:9999',
        managementKey: '',
        localSession: true,
      })
    ).toThrow();
  });

  test('retains backend version and plugin support events', async () => {
    const events: Event[] = [];
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: {
        dispatchEvent: (event: Event) => {
          events.push(event);
          return true;
        },
      },
    });
    apiClient.setConfig({ apiBase: 'http://proxy.invalid', managementKey: 'fixture-only' });
    const adapter: AxiosAdapter = async (config) => ({
      data: {},
      status: 200,
      statusText: 'OK',
      config,
      headers: { 'x-cpa-version': 'v8.0.3', 'x-cpa-support-plugin': 'true' },
    });
    await apiClient.get('/config', { adapter });
    expect(events.map((event) => event.type)).toEqual([
      'server-version-update',
      'server-plugin-support-update',
    ]);
    expect((events[0] as CustomEvent).detail.version).toBe('v8.0.3');
    expect((events[1] as CustomEvent).detail.supportsPlugin).toBe(true);
  });
});
