import { describe, expect, test } from 'bun:test';
import type { ClaudeQuotaState, CodexQuotaState } from '@/types';
import {
  aggregateLedgerWindows,
  ledgerWindows,
  maskQuotaIdentity,
  passiveLedgerQuota,
} from '@/features/quota/quotaLedgerModel';

const claude = (fable: number, broad: number): ClaudeQuotaState => ({
  status: 'success',
  windows: [
    { id: 'five-hour', label: '5-hour', usedPercent: 0, resetLabel: '', periodHours: 5 },
    {
      id: 'seven-day',
      label: '7-day',
      usedPercent: 100 - broad,
      resetLabel: '',
      periodHours: 168,
      resetAtMs: 2000,
    },
    {
      id: 'seven-day-fable',
      label: '7-day Fable',
      usedPercent: 100 - fable,
      resetLabel: '',
      periodHours: 168,
      resetAtMs: 1000,
    },
  ],
});

describe('quota ledger model', () => {
  test('orders model weekly, session, and muted broad weekly without losing extra windows', () => {
    const windows = ledgerWindows('claude', claude(58, 79));
    expect(windows.map((window) => window.id)).toEqual([
      'seven-day-fable',
      'five-hour',
      'seven-day',
    ]);
    expect(windows.map((window) => window.remaining)).toEqual([58, 100, 79]);
    expect(windows[2].muted).toBe(true);
  });
  test('sums all accounts and preserves every segment independently', () => {
    const rows = [58, 100, 100, 51, 100].map((remaining) =>
      ledgerWindows('claude', claude(remaining, 90))
    );
    expect(aggregateLedgerWindows(rows)).toMatchObject({
      remaining: 409,
      capacity: 500,
      measuredCount: 5,
      resetAtMs: 1000,
    });
    expect(aggregateLedgerWindows(rows).segments.map((window) => window?.remaining)).toEqual([
      58, 100, 100, 51, 100,
    ]);
    const many = Array.from({ length: 77 }, () => ledgerWindows('claude', claude(100, 100)));
    expect(aggregateLedgerWindows(many).remaining).toBe(7700);
    expect(aggregateLedgerWindows(many).segments).toHaveLength(77);
  });
  test('unknown and missing model windows never become zero or a false complete total', () => {
    const rows = [
      ledgerWindows('claude', claude(50, 99)),
      [],
      ledgerWindows('claude', {
        ...claude(80, 100),
        windows: claude(80, 100).windows.filter((window) => window.id !== 'seven-day-fable'),
      }),
    ];
    expect(aggregateLedgerWindows(rows)).toMatchObject({
      remaining: null,
      measuredCount: 1,
      capacity: 300,
    });
  });
  test('Codex summary selects account weekly rather than a model-scoped quota', () => {
    const state: CodexQuotaState = {
      status: 'success',
      windows: [
        { id: 'spark-weekly', label: 'Spark', usedPercent: 99, resetLabel: '', periodHours: 168 },
        { id: 'weekly', label: 'Weekly', usedPercent: 80, resetLabel: '', periodHours: 168 },
      ],
    };
    expect(ledgerWindows('codex', state)[0]).toMatchObject({ id: 'weekly', remaining: 20 });
  });
  test('masking preserves provider and domain suffix but no email identity in DOM labels', () => {
    expect(maskQuotaIdentity('claude-person@example.dev.json')).toBe('claude-p•••@e•••.dev.json');
    expect(maskQuotaIdentity('person+work@sub.example.com')).toBe('p•••@s•••.e•••.com');
    expect(maskQuotaIdentity('ordinary-file.json')).toBe('ordinary-file.json');
  });
  test('passive observations normalize known contracts, relative resets, stale and future data', () => {
    const now = Date.UTC(2026, 9, 7, 12);
    const file = {
      name: 'test',
      quota: {
        observed_at: new Date(now - 60000).toISOString(),
        signals: {
          'X-Codex-Primary-Used-Percent': '75',
          'X-Codex-Primary-Window-Minutes': '10080',
          'X-Codex-Primary-Reset-After-Seconds': '3600',
        },
      },
    };
    expect(ledgerWindows('codex', passiveLedgerQuota(file, 'codex', now))[0]).toMatchObject({
      id: 'weekly',
      remaining: 25,
      resetAtMs: now - 60000 + 3600000,
    });
    expect(
      ledgerWindows('codex', passiveLedgerQuota(file, 'codex', now + 600000))[0].remaining
    ).toBeNull();
    expect(passiveLedgerQuota(file, 'codex', now - 120000)).toBeUndefined();
    expect(
      passiveLedgerQuota({ name: 'test', quota: { signals: file.quota.signals } }, 'codex', now)
    ).toBeUndefined();
    const model = {
      name: 'test',
      model_quotas: {
        'fable-5': {
          observed_at: new Date(now).toISOString(),
          signals: {
            'Anthropic-Ratelimit-Unified-7d-Fable-Utilization': '0.42',
            'Anthropic-Ratelimit-Unified-7d-Fable-Reset': String((now + 86400000) / 1000),
          },
        },
      },
    };
    expect(ledgerWindows('claude', passiveLedgerQuota(model, 'claude', now))[0]).toMatchObject({
      id: 'seven-day-fable',
      remaining: 58,
    });
  });
});
