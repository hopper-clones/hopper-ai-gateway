import { describe, expect, test } from 'bun:test';
import { runInNewContext } from 'node:vm';
import { installTheoQuotaReplay } from './fixtures/theoQuotaReplay.mjs';

describe('Theo browser replay boundary', () => {
  test('reference clock advances so animation and cache lifetimes can finish', async () => {
    let wallTime = 1_000_000;
    class NativeTestDate extends Date {
      static now() { return wallTime; }
    }
    const sandbox: Record<string, unknown> = { Date: NativeTestDate };
    const { fixture } = await installTheoQuotaReplay({
      addInitScript: async (script: (args: unknown) => void, args: unknown) => {
        runInNewContext(`(${script.toString()})(${JSON.stringify(args)})`, sandbox);
      },
      route: async () => {},
    });
    const ReplayDate = sandbox.Date as DateConstructor;
    const reference = Date.parse(fixture.clock);
    expect(ReplayDate.now()).toBe(reference);
    wallTime += 137;
    expect(ReplayDate.now()).toBe(reference + 137);
    expect(new ReplayDate().getTime()).toBe(reference + 137);
    expect(new ReplayDate('2026-09-13T13:00:00-07:00').getTime()).toBe(
      Date.parse('2026-09-13T13:00:00-07:00')
    );
  });

  test('native credentials endpoint supplies only synthetic identities', async () => {
    let handler: (route: unknown) => Promise<unknown> = async () => {};
    await installTheoQuotaReplay({
      addInitScript: async () => {},
      route: async (_pattern: string, callback: typeof handler) => { handler = callback; },
    });
    let response: { body?: string } = {};
    let continued = false;
    await handler({
      request: () => ({
        url: () => 'http://127.0.0.1:48733/v8/management/credentials',
        method: () => 'GET',
      }),
      fulfill: async (result: typeof response) => { response = result; },
      continue: async () => { continued = true; },
    });
    expect(continued).toBe(false);
    const payload = JSON.parse(response.body ?? '{}');
    expect(payload.files).toHaveLength(11);
    expect(payload.files.filter((file: { type: string }) => file.type === 'claude')).toHaveLength(5);
    expect(payload.files.filter((file: { type: string }) => file.type === 'codex')).toHaveLength(3);
  });
});
