package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ObserveQuotaHeaders applies an authoritative provider quota refresh in memory.
// It never reports a successful execution, resets cooldowns, writes credentials,
// or combines snapshots. Empty and older responses leave the last observation
// intact. Callers must supply provider-owned headers, not guessed display labels.
func (m *Manager) ObserveQuotaHeaders(ctx context.Context, authID, provider string, headers http.Header, observedAt time.Time) error {
	if m == nil {
		return fmt.Errorf("quota observation manager is unavailable")
	}
	if !ProviderSupportsQuotaObservation(provider) {
		return fmt.Errorf("provider does not support quota observations")
	}
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	if observedAt.After(time.Now()) {
		return fmt.Errorf("quota observation is in the future")
	}
	if len(collectQuotaSignals(provider, headers)) == 0 {
		return nil
	}
	release, err := m.lockAuthMutationContext(ctx, authID)
	if err != nil {
		return err
	}
	defer release()
	m.mu.Lock()
	a := m.auths[authID]
	if a == nil {
		m.mu.Unlock()
		return fmt.Errorf("quota observation auth not found")
	}
	if !strings.EqualFold(strings.TrimSpace(provider), strings.TrimSpace(a.Provider)) {
		m.mu.Unlock()
		return fmt.Errorf("quota observation provider mismatch")
	}
	if observedAt.Before(a.Quota.ObservedAt) {
		m.mu.Unlock()
		return nil
	}
	a.Quota.ObserveResponseHeadersForProvider(provider, headers, observedAt)
	a.Generation++
	snapshot := a.Clone()
	m.notifyAuthChangeLocked(authID)
	m.mu.Unlock()
	if m.scheduler != nil {
		m.scheduler.upsertAuth(snapshot)
	}
	return nil
}
