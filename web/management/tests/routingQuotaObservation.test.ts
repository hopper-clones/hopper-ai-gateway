import { describe, expect, test } from 'bun:test';
import { normalizeRoutingObservation, apiCallApi } from '@/services/api/apiCall';
import { apiClient } from '@/services/api/client';
import { CLAUDE_CONFIG } from '@/features/quota/providers/claude/data';
import { CODEX_CONFIG } from '@/features/quota/providers/codex/data';

describe('native quota routing observation acknowledgement', () => {
  test('preserves only the declared statuses and leaves older/generic responses absent', () => {
    expect(normalizeRoutingObservation(undefined)).toBeUndefined();
    expect(normalizeRoutingObservation({ status: 'imagined' })).toBeUndefined();
    for (const status of ['applied', 'superseded', 'unsupported', 'unavailable', 'rejected']) {
      expect(
        normalizeRoutingObservation({ status, reason: 'upstream_reason', secret: 'omitted' })
      ).toEqual({ status, reason: 'upstream_reason' });
    }
  });
  test('reads the native API envelope without changing the provider response body', async () => {
    const original = apiClient.post;
    apiClient.post = (async () => ({
      status_code: 200,
      body: { seven_day: { utilization: 40 } },
      routing_observation: {
        status: 'unsupported',
        reason: 'incomplete_invalid_or_oversized_quota_snapshot',
      },
    })) as typeof apiClient.post;
    try {
      const result = await apiCallApi.request({ method: 'GET', url: 'https://example.test/quota' });
      expect(result.body).toEqual({ seven_day: { utilization: 40 } });
      expect(result.routingObservation).toEqual({
        status: 'unsupported',
        reason: 'incomplete_invalid_or_oversized_quota_snapshot',
      });
    } finally {
      apiClient.post = original;
    }
  });
  test('provider success states retain routing status independently from displayed quota', () => {
    const routingObservation = {
      status: 'rejected' as const,
      reason: 'routing_observation_not_applied',
    };
    expect(CLAUDE_CONFIG.buildSuccessState({ windows: [], routingObservation })).toMatchObject({
      status: 'success',
      routingObservation,
    });
    expect(
      CODEX_CONFIG.buildSuccessState({
        windows: [],
        planType: null,
        subscriptionActiveUntil: null,
        creditBalance: null,
        creditsUnlimited: false,
        rateLimitResetCredits: [],
        rateLimitResetCreditsAvailableCount: null,
        rateLimitResetCreditsApplicableAvailableCount: null,
        rateLimitResetCreditsError: '',
        routingObservation,
      })
    ).toMatchObject({ status: 'success', routingObservation });
  });
});
