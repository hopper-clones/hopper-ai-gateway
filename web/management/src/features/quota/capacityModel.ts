import type { CapacityAccount, CapacitySnapshot } from '@/services/api/capacity';

export const capacityProviderName = (provider: string): string =>
  ({
    codex: 'OpenAI Codex',
    claude: 'Claude',
    anthropic: 'Claude',
    antigravity: 'Antigravity',
    cursor: 'Cursor',
    grok: 'Grok',
    xai: 'Grok',
    windsurf: 'Windsurf',
    devin: 'Devin',
  })[provider.toLowerCase()] ?? provider;

/** A saved value can be useful history without being current routing capacity. */
export function currentCapacityRemaining(
  account: CapacityAccount,
  meter: CapacityAccount['meters'][number],
  now: number
): number | null {
  if (account.freshness !== 'fresh' || meter.state !== 'fresh' || !account.observedAt) return null;
  const observed = Date.parse(account.observedAt);
  if (!Number.isFinite(observed) || observed > now || now - observed > 15 * 60 * 1000) return null;
  if (meter.resetsAt && Date.parse(meter.resetsAt) <= now) return null;
  return typeof meter.remaining === 'number' && Number.isFinite(meter.remaining)
    ? Math.min(100, Math.max(0, meter.remaining))
    : null;
}

export function capacityGroups(accounts: CapacityAccount[]) {
  const groups = new Map<string, CapacityAccount[]>();
  for (const account of accounts) {
    const rows = groups.get(account.provider) ?? [];
    rows.push(account);
    groups.set(account.provider, rows);
  }
  const order = ['claude', 'anthropic', 'codex', 'antigravity'];
  return [...groups].sort(([a], [b]) => {
    const rank = (value: string) => (order.includes(value) ? order.indexOf(value) : order.length);
    return rank(a) - rank(b) || a.localeCompare(b);
  });
}

/** Complete account coverage is required before calling a lifetime total complete. */
export function lifetimeCoverage(snapshot: CapacitySnapshot) {
  const rows = snapshot.tokens.account.accounts;
  return { measured: rows.filter((row) => row.lifetimeTokens !== null).length, total: rows.length };
}
