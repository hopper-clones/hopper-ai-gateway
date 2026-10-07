package auth

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// QuotaObservationFreshness bounds ranking by passive allowed observations. An
// explicit exhaustion with a future reset remains authoritative until that reset.
const QuotaObservationFreshness = 5 * time.Minute

// RoutingQuotaWindow is a provider-observed window relevant to the requested
// model. A zero ResetAt is unknown, never an imminent reset.
type RoutingQuotaWindow struct {
	Scope     string    `json:"scope"`
	ResetAt   time.Time `json:"reset_at"`
	Exhausted bool      `json:"exhausted"`
	Stale     bool      `json:"stale"`
}

// ResetFirstSelector concentrates cold traffic on the available credential whose
// relevant observed quota resets first. Stable ID ordering breaks ties and is the
// fallback when no fresh reset is known. The session-affinity wrapper retains
// established bindings before invoking this selector. It deliberately uses the
// manager's model-aware selection path rather than the scheduler's cached queues.
type ResetFirstSelector struct{}

func (s *ResetFirstSelector) Pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	now := time.Now()
	auths, err := filterObservedQuotaAuths(ctx, auths, provider, model, now)
	if err != nil {
		return nil, err
	}
	available, err := getSelectorAvailableAuths(ctx, auths, provider, model, now)
	if err != nil {
		return nil, err
	}
	available = preferCodexWebsocketAuths(ctx, provider, available)
	var best *Auth
	var bestReset time.Time
	for _, candidate := range available {
		var reset time.Time
		for _, w := range RoutingQuotaWindows(candidate, quotaModelFromContext(ctx, candidate.ID, model), now) {
			if !w.Stale && !w.Exhausted && w.ResetAt.After(now) && (reset.IsZero() || w.ResetAt.Before(reset)) {
				reset = w.ResetAt
			}
		}
		if best == nil || (!reset.IsZero() && (bestReset.IsZero() || reset.Before(bestReset))) || (reset.Equal(bestReset) && candidate.ID < best.ID) {
			best, bestReset = candidate, reset
		}
	}
	return best, nil
}

// RoutingQuotaWindows interprets existing upstream observations without probing,
// inventing reset times, or inferring model identity from opaque limit IDs. Only
// exact matching named Codex limits and recognized Claude model families apply.
func RoutingQuotaWindows(a *Auth, model string, now time.Time) []RoutingQuotaWindow {
	if a == nil {
		return nil
	}
	q := a.Quota
	// Model observations identify the request that produced a named active limit.
	modelKeys := make([]string, 0, len(a.ModelStates))
	for key := range a.ModelStates {
		modelKeys = append(modelKeys, key)
	}
	sort.Strings(modelKeys)
	for _, key := range modelKeys {
		state := a.ModelStates[key]
		if state != nil && canonicalModelKey(key) == canonicalModelKey(model) && state.Quota.ObservedAt.After(q.ObservedAt) {
			q = state.Quota
		}
	}
	if q.ObservedAt.IsZero() || q.ObservedAt.After(now) {
		return nil
	}
	signals := make(map[string]string, len(q.Signals))
	for key, value := range q.Signals {
		signals[strings.ToLower(key)] = value
	}
	stale := now.Sub(q.ObservedAt) > QuotaObservationFreshness
	windows := make([]RoutingQuotaWindow, 0, 6)
	appendWindow := func(scope, prefix string, claude bool) {
		var reset time.Time
		suffix := "-reset-at"
		if claude {
			suffix = "-reset"
		}
		reset = quotaResetTime(signals[prefix+suffix])
		if reset.IsZero() && !claude {
			if seconds, err := strconv.ParseFloat(signals[prefix+"-reset-after-seconds"], 64); err == nil && seconds >= 0 && seconds <= float64((365*24*time.Hour)/time.Second) {
				reset = q.ObservedAt.Add(time.Duration(seconds * float64(time.Second)))
			}
		}
		exhausted := strings.EqualFold(signals[prefix+"-status"], "rejected")
		utilization, parseErr := strconv.ParseFloat(signals[prefix+"-used-percent"], 64)
		if claude {
			utilization, parseErr = strconv.ParseFloat(signals[prefix+"-utilization"], 64)
		}
		threshold := 100.0
		if claude {
			threshold = 1
		}
		if parseErr == nil && !math.IsNaN(utilization) && !math.IsInf(utilization, 0) && utilization >= threshold {
			exhausted = true
		}
		if !reset.IsZero() || exhausted {
			windows = append(windows, RoutingQuotaWindow{scope, reset, exhausted, stale})
		}
	}
	switch strings.ToLower(a.Provider) {
	case "claude":
		for _, scope := range []string{"5h", "7d"} {
			appendWindow(scope, "anthropic-ratelimit-unified-"+scope, true)
		}
		lowerModel := strings.ToLower(canonicalModelKey(model))
		for _, family := range []string{"sonnet", "opus"} {
			if (strings.HasPrefix(lowerModel, "claude-") && strings.Contains(lowerModel, family)) || strings.HasPrefix(lowerModel, family+"-") {
				appendWindow("7d-"+family, "anthropic-ratelimit-unified-7d-"+family, true)
			}
		}
		// Unified/overage rejection does not identify a broad subscription window.
		// The executor's classified model/account cooldown remains authoritative.
	case "codex":
		for _, slot := range []string{"primary", "secondary"} {
			appendWindow(slot, "x-codex-"+slot, false)
		}
		namespaces := make(map[string]bool)
		for key, value := range signals {
			if strings.HasPrefix(key, "x-codex-") && strings.HasSuffix(key, "-limit-name") && canonicalModelKey(strings.ToLower(value)) == canonicalModelKey(strings.ToLower(model)) {
				namespaces[strings.TrimSuffix(key, "-limit-name")] = true
			}
		}
		names := make([]string, 0, len(namespaces))
		for name := range namespaces {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			for _, slot := range []string{"primary", "secondary"} {
				appendWindow(strings.TrimPrefix(name, "x-codex-")+"-"+slot, name+"-"+slot, false)
			}
		}

	}
	return windows
}

func quotaResetTime(value string) time.Time {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds > 0 && seconds <= 253402300799 {
		return time.Unix(seconds, 0)
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed
	}
	if parsed, err := http.ParseTime(value); err == nil {
		return parsed
	}
	return time.Time{}
}

func observedQuotaBlock(a *Auth, model string, now time.Time) (bool, blockReason, time.Time) {
	blocked := false
	var recoverAt time.Time
	for _, w := range RoutingQuotaWindows(a, model, now) {
		if !w.Exhausted {
			continue
		}
		if w.ResetAt.IsZero() {
			if !w.Stale {
				return true, blockReasonOther, time.Time{}
			}
			continue
		}
		if w.ResetAt.After(now) {
			blocked = true
			if w.ResetAt.After(recoverAt) {
				recoverAt = w.ResetAt
			}
		}
	}
	if blocked {
		return true, blockReasonCooldown, recoverAt
	}
	return false, blockReasonNone, time.Time{}
}

// The resolved model is retained per credential so aliases and prefixes cannot
// cause a model-specific cap to be ranked as a different model's quota.
type resetQuotaModelsKey struct{}

func isResetFirstSelector(selector Selector) bool {
	if affinity, ok := selector.(*SessionAffinitySelector); ok {
		selector = affinity.fallback
	}
	_, ok := selector.(*ResetFirstSelector)
	return ok
}

func (m *Manager) resetQuotaSelectorContext(ctx context.Context, auths []*Auth, model string, selector Selector) context.Context {
	if !isResetFirstSelector(selector) {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	models := make(map[string]string, len(auths))
	for _, a := range auths {
		models[a.ID] = m.selectionModelForAuth(a, model)
	}
	return context.WithValue(ctx, resetQuotaModelsKey{}, models)
}

func quotaModelFromContext(ctx context.Context, id, fallback string) string {
	if ctx != nil {
		if models, ok := ctx.Value(resetQuotaModelsKey{}).(map[string]string); ok {
			if model, found := models[id]; found {
				return model
			}
		}
	}
	return fallback
}

func filterObservedQuotaAuths(ctx context.Context, auths []*Auth, provider, model string, now time.Time) ([]*Auth, error) {
	available := make([]*Auth, 0, len(auths))
	var earliest time.Time
	for _, a := range auths {
		if a == nil {
			continue
		}
		blocked, _, reset := observedQuotaBlock(a, quotaModelFromContext(ctx, a.ID, model), now)
		if !blocked {
			available = append(available, a)
			continue
		}
		if reset.After(now) && (earliest.IsZero() || reset.Before(earliest)) {
			earliest = reset
		}
	}
	if len(available) == 0 && len(auths) > 0 {
		if !earliest.IsZero() {
			return nil, newModelCooldownError(model, provider, earliest.Sub(now))
		}
		return nil, newAuthUnavailableError(time.Time{}, now)
	}
	return available, nil
}
