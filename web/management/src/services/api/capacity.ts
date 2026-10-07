import { apiClient } from './client';

export interface CapacityAccount {
  id: string;
  provider: string;
  label: string;
  plan: string | null;
  observedAt: string | null;
  freshness: string;
  meters: Array<{
    id: string;
    label: string;
    model: string | null;
    remaining: number | null;
    state: string;
    resetsAt: string | null;
    kind: string;
  }>;
  resetCreditsAvailable: number | null;
  resetCredits: Array<{
    id: string;
    expiresAt: string | null;
    expiryKnown: boolean;
    status: string;
  }> | null;
  usageCredits: Array<{
    limitId: string;
    label: string;
    hasCredits: boolean;
    unlimited: boolean;
    balance: string | null;
  }> | null;
}
export interface CapacitySnapshot {
  schemaVersion: 'hopper.gateway-capacity.v1';
  readAt: string;
  source: { product: string; mode: string; version: string };
  accounts: CapacityAccount[];
  tokens: {
    local: {
      from: string;
      to: string;
      total: number;
      input: number;
      output: number;
      cachedInput: number;
      count: number;
      coverage: string;
      legacyDetailUnavailable: boolean;
      providers: Array<{
        key: string;
        count: number;
        input: number;
        output: number;
        cachedInput: number;
        total: number;
      }>;
    };
    account: {
      from: string;
      to: string;
      reportedTokens: number | null;
      lifetimeTokens: number | null;
      coverage: { requestedAccounts: number; reportedAccounts: number; freshAccounts: number };
      accounts: Array<{
        id: string;
        provider: string;
        observedAt: string | null;
        state: string;
        reportedTokens: number | null;
        lifetimeTokens: number | null;
        coverage: string;
      }>;
    };
  };
}
export const capacityApi = {
  snapshot: () => apiClient.get<CapacitySnapshot>('/capacity/snapshot'),
};
