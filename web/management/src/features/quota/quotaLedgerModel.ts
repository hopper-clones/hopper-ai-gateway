import type { AuthFileItem } from '@/types';
import type { QuotaCardState } from './providers';
import type { QuotaProviderType } from './providers/types';

export interface LedgerWindow {
  id: string;
  label: string;
  labelKey?: string;
  labelParams?: Record<string, string | number>;
  remaining: number | null;
  resetAtMs: number | null;
  periodHours: number | null;
  muted?: boolean;
}
interface WindowInput {
  id: string;
  label?: string;
  labelKey?: string;
  labelParams?: Record<string, string | number>;
  usedPercent?: number | null;
  remainingPercent?: number | null;
  resetAtMs?: number | null;
  resetAt?: number;
  periodHours?: number | null;
  durationMinutes?: number;
}
interface LedgerState extends QuotaCardState {
  windows?: WindowInput[];
  planType?: string | null;
  plan?: string | null;
  data?: { windows?: WindowInput[]; planName?: string };
  groups?: {
    buckets: {
      id: string;
      label: string;
      remainingFraction: number;
      resetAtMs?: number | null;
      periodHours?: number | null;
    }[];
  }[];
  subscription?: { plan?: string | null; tierName?: string | null } | null;
  rows?: {
    id: string;
    label?: string;
    labelKey?: string;
    used: number;
    limit: number;
    resetAtMs?: number | null;
    periodHours?: number | null;
  }[];
  billing?: {
    periodType: string;
    usagePercent: number | null;
    resetAtMs?: number | null;
    periodHours?: number | null;
    planLabel?: string;
  } | null;
}
const percent = (value: unknown): number | null =>
  typeof value === 'number' && Number.isFinite(value) ? Math.max(0, Math.min(100, value)) : null;

/** Filenames can contain email identities too; mask those before any DOM attribute is set. */
export function maskQuotaIdentity(name: string): string {
  return name.replace(
    /([\w.+-]+)@([\w.-]+?\.[A-Za-z]{2,}?)(?=\.json|$|\s)/g,
    (_all, local: string, domain: string) => {
      const parts = domain.split('.');
      const suffix = parts.pop();
      const prefix = local.match(/^(claude|codex|antigravity|kimi|xai|devin|meta)-/i)?.[0] ?? '';
      return `${prefix}${local.slice(prefix.length, prefix.length + 1)}•••@${parts.map((part) => `${part.slice(0, 1)}•••`).join('.')}.${suffix}`;
    }
  );
}

export function ledgerWindows(provider: QuotaProviderType, quota?: QuotaCardState): LedgerWindow[] {
  if (quota?.status !== 'success') return [];
  const state = quota as LedgerState;
  const windows = (state.windows ?? state.data?.windows ?? []).map((window): LedgerWindow => ({
    id: window.id,
    label: window.label ?? '',
    labelKey: window.labelKey,
    labelParams: window.labelParams,
    remaining:
      window.remainingPercent !== undefined
        ? percent(window.remainingPercent)
        : window.usedPercent == null
          ? null
          : percent(100 - window.usedPercent),
    resetAtMs: window.resetAtMs ?? (window.resetAt ? window.resetAt * 1000 : null),
    periodHours:
      window.periodHours ?? (window.durationMinutes ? window.durationMinutes / 60 : null),
  }));
  if (provider === 'claude') {
    const primary =
      windows.find((window) => window.id === 'seven-day-fable') ??
      windows.find(
        (window) => window.periodHours === 168 && !['seven-day', 'weekly'].includes(window.id)
      );
    const session = windows.find((window) => window.periodHours === 5);
    const broad = windows.find((window) => ['seven-day', 'weekly'].includes(window.id));
    const preferred = [primary, session, broad && { ...broad, muted: true }].filter(
      (window): window is LedgerWindow => Boolean(window)
    );
    return [
      ...preferred,
      ...windows.filter((window) => !preferred.some((item) => item.id === window.id)),
    ];
  }
  if (windows.length) {
    return provider === 'codex'
      ? [...windows].sort((a, b) => (a.id === 'weekly' ? -1 : b.id === 'weekly' ? 1 : 0))
      : windows;
  }
  if (state.groups)
    return state.groups.flatMap((group) =>
      group.buckets.map((bucket) => ({
        id: bucket.id,
        label: bucket.label,
        remaining: percent(bucket.remainingFraction * 100),
        resetAtMs: bucket.resetAtMs ?? null,
        periodHours: bucket.periodHours ?? null,
      }))
    );
  if (state.rows)
    return state.rows.map((row) => ({
      id: row.id,
      label: row.label ?? '',
      labelKey: row.labelKey,
      remaining: row.limit > 0 ? percent(100 * (1 - row.used / row.limit)) : null,
      resetAtMs: row.resetAtMs ?? null,
      periodHours: row.periodHours ?? null,
    }));
  if (state.billing)
    return [
      {
        id: state.billing.periodType,
        label: '',
        labelKey:
          state.billing.periodType === 'weekly'
            ? 'quota_management.ledger_weekly'
            : 'quota_management.ledger_billing',
        remaining:
          state.billing.usagePercent == null ? null : percent(100 - state.billing.usagePercent),
        resetAtMs: state.billing.resetAtMs ?? null,
        periodHours: state.billing.periodHours ?? null,
      },
    ];
  return [];
}

export function ledgerPlan(file: AuthFileItem, quota?: QuotaCardState): string | null {
  const state = quota as LedgerState | undefined;
  return (
    state?.planType ??
    state?.plan ??
    state?.data?.planName ??
    state?.subscription?.tierName ??
    state?.subscription?.plan ??
    state?.billing?.planLabel ??
    (typeof file.plan === 'string' ? file.plan : null)
  );
}

/** Sum matching remaining percentages, never a mixture of model and account windows. */
export function aggregateLedgerWindows(rows: readonly LedgerWindow[][], preferredId?: string) {
  const id = preferredId ?? rows.find((windows) => windows.length)?.[0]?.id;
  const selected = rows.map((windows) => windows.find((window) => window.id === id));
  const measured = selected.filter((window): window is LedgerWindow => window?.remaining != null);
  const resets = selected.flatMap((window) =>
    window?.resetAtMs != null ? [window.resetAtMs] : []
  );
  return {
    window: selected.find((window) => Boolean(window)),
    segments: selected,
    remaining:
      measured.length === rows.length && rows.length > 0
        ? measured.reduce((sum, window) => sum + (window.remaining ?? 0), 0)
        : null,
    measuredCount: measured.length,
    capacity: rows.length * 100,
    resetAtMs: resets.length ? Math.min(...resets) : null,
  };
}

export function quotaTone(remaining: number | null): 'unknown' | 'low' | 'mid' | 'high' {
  return remaining == null ? 'unknown' : remaining < 20 ? 'low' : remaining < 60 ? 'mid' : 'high';
}

const record = (value: unknown): Record<string, unknown> | null =>
  value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
const finiteSignal = (value: unknown) =>
  typeof value === 'string' && value.trim() !== '' && Number.isFinite(Number(value))
    ? Number(value)
    : null;
const signalInstant = (value: unknown): number | null => {
  if (typeof value !== 'string' || !value.trim()) return null;
  const seconds = finiteSignal(value);
  const ms = seconds != null ? seconds * 1000 : Date.parse(value);
  return Number.isFinite(ms) && ms > 0 ? ms : null;
};

/** Known upstream watermarks only. Passive freshness matches the backend selector's five minutes. */
export function passiveLedgerQuota(
  file: AuthFileItem,
  provider: QuotaProviderType,
  now: number
): (LedgerState & { observedAtMs: number; observationStale: boolean }) | undefined {
  if (provider !== 'claude' && provider !== 'codex') return undefined;
  const modelQuotas = record(file.model_quotas);
  const snapshots = [record(file.quota), ...Object.values(modelQuotas ?? {}).map(record)].filter(
    (item): item is Record<string, unknown> => Boolean(item)
  );
  const windows = new Map<string, WindowInput>();
  let newest = 0;
  let plan: string | null = null;
  for (const snapshot of snapshots.sort(
    (a, b) => Date.parse(String(a.observed_at)) - Date.parse(String(b.observed_at))
  )) {
    const observed =
      typeof snapshot.observed_at === 'string' ? Date.parse(snapshot.observed_at) : NaN;
    if (!Number.isFinite(observed) || observed <= 0 || observed > now) continue;
    const signals = record(snapshot.signals);
    if (!signals) continue;
    const normalized = Object.fromEntries(
      Object.entries(signals).map(([key, value]) => [key.toLowerCase(), value])
    );
    const stale = now - observed > 5 * 60 * 1000;
    const scopes =
      provider === 'claude'
        ? ['5h', '7d', '7d-fable', '7d-opus', '7d-sonnet']
        : ['primary', 'secondary'];
    for (const scope of scopes) {
      const prefix =
        provider === 'claude' ? `anthropic-ratelimit-unified-${scope}` : `x-codex-${scope}`;
      const used = finiteSignal(
        normalized[`${prefix}-${provider === 'claude' ? 'utilization' : 'used-percent'}`]
      );
      let reset = signalInstant(
        normalized[`${prefix}-${provider === 'claude' ? 'reset' : 'reset-at'}`]
      );
      if (reset == null && provider === 'codex') {
        const after = finiteSignal(normalized[`${prefix}-reset-after-seconds`]);
        if (after != null && after >= 0) reset = observed + after * 1000;
      }
      if (used == null && reset == null) continue;
      const minutes = finiteSignal(normalized[`${prefix}-window-minutes`]);
      const period =
        provider === 'claude' ? (scope === '5h' ? 5 : 168) : minutes == null ? null : minutes / 60;
      const id =
        provider === 'claude'
          ? scope === '5h'
            ? 'five-hour'
            : scope.replace('7d', 'seven-day')
          : period === 168
            ? 'weekly'
            : period === 5
              ? 'five-hour'
              : scope;
      const labelKey =
        provider === 'claude'
          ? `claude_quota.${scope === '5h' ? 'five_hour' : scope.replace('7d', 'seven_day').replace(/-/g, '_')}`
          : period === 5
            ? 'codex_quota.five_hour_limit'
            : 'quota_management.ledger_weekly';
      windows.set(id, {
        id,
        labelKey,
        label: '',
        usedPercent:
          stale || (reset != null && reset <= now)
            ? null
            : used == null
              ? null
              : provider === 'claude'
                ? used * 100
                : used,
        resetAtMs: reset,
        periodHours: period,
      });
      newest = Math.max(newest, observed);
    }
    if (provider === 'codex' && typeof normalized['x-codex-plan-type'] === 'string')
      plan = normalized['x-codex-plan-type'];
  }
  return windows.size
    ? {
        status: 'success',
        windows: [...windows.values()],
        observedAtMs: newest,
        observationStale: now - newest > 5 * 60 * 1000,
        planType: plan,
      }
    : undefined;
}
