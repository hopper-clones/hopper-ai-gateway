package auth

import (
	"context"
	"net/http"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// Peeker is a Selector that can answer "which auth would Pick return" without
// advancing rotation state, spending weighted credits or binding sessions.
type Peeker interface {
	Peek(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error)
}

type peekOnlyKey struct{}

func withPeekOnly(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, peekOnlyKey{}, true)
}

func peekOnly(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	flag, _ := ctx.Value(peekOnlyKey{}).(bool)
	return flag
}

// selectWithPeek runs Pick, or Peek when the context asks for a read-only
// answer. A selector that cannot peek is refused rather than mutated.
func selectWithPeek(ctx context.Context, selector Selector, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	if !peekOnly(ctx) {
		return selector.Pick(ctx, provider, model, opts, auths)
	}
	peeker, ok := selector.(Peeker)
	if !ok {
		return nil, &Error{Code: "peek_unsupported", Message: "selector cannot answer without changing its state", HTTPStatus: http.StatusNotImplemented}
	}
	return peeker.Peek(ctx, provider, model, opts, auths)
}

// Peek returns the next round-robin auth without recording it as picked.
func (s *RoundRobinSelector) Peek(ctx context.Context, provider, model string, _ cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	available, err := getSelectorAvailableAuths(ctx, auths, provider, model, time.Now())
	if err != nil {
		return nil, err
	}
	available = preferCodexWebsocketAuths(ctx, provider, available)
	key := provider + ":" + canonicalModelKey(model)
	s.mu.Lock()
	last := s.lastPicked[key]
	s.mu.Unlock()
	return available[successorIndex(available, last)], nil
}

// Peek runs smooth weighted selection on a copy of the credit state.
func (s *WeightedRoundRobinSelector) Peek(ctx context.Context, provider, model string, _ cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	available, errAvailable := getSelectorAvailableAuths(ctx, positiveWeightAuths(auths), provider, model, time.Now())
	if errAvailable != nil {
		return nil, errAvailable
	}
	available = preferCodexWebsocketAuths(ctx, provider, available)
	key := provider + ":" + canonicalModelKey(weightedSelectorStateModel(ctx, model))
	s.mu.Lock()
	scratch := &smoothWeightedState{}
	if state := s.states[key]; state != nil {
		scratch.weights = cloneInt64Map(state.weights)
		scratch.current = cloneInt64Map(state.current)
	}
	s.mu.Unlock()
	scratch.prepare(authWeightVector(available))
	picked := pickSmoothWeightedAuth(available, scratch.current)
	if picked == nil {
		return nil, &Error{Code: "auth_unavailable", Message: "no auth available with positive weight"}
	}
	return picked, nil
}

func cloneInt64Map(src map[string]int64) map[string]int64 {
	if src == nil {
		return nil
	}
	out := make(map[string]int64, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

// Peek is Pick: fill-first keeps no state.
func (s *FillFirstSelector) Peek(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	return s.Pick(ctx, provider, model, opts, auths)
}

// Peek is Pick: reset-first keeps no state.
func (s *ResetFirstSelector) Peek(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	return s.Pick(ctx, provider, model, opts, auths)
}

// Peek answers from the fallback without creating a session binding.
func (s *SessionAffinitySelector) Peek(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	if isResetFirstSelector(s) {
		var err error
		auths, err = filterObservedQuotaAuths(ctx, auths, provider, model, time.Now())
		if err != nil {
			return nil, err
		}
	}
	peeker, ok := s.fallback.(Peeker)
	if !ok {
		return nil, &Error{Code: "peek_unsupported", Message: "selector cannot answer without changing its state", HTTPStatus: http.StatusNotImplemented}
	}
	return peeker.Peek(ctx, provider, model, opts, auths)
}

// PeekAuth reports the credential SelectAuth would choose for the model without
// executing anything and without advancing scheduler state. Plugin schedulers
// are bypassed so their state is not touched either.
func (m *Manager) PeekAuth(ctx context.Context, provider, model string, opts cliproxyexecutor.Options) (*Auth, error) {
	if m != nil && m.HomeEnabled() {
		return nil, &Error{Code: "home_unavailable", Message: "legacy auth selection is unavailable while Home is enabled", HTTPStatus: http.StatusServiceUnavailable}
	}
	peekCtx := withPeekOnly(ctx)
	if pinned, ok := m.lanePinnedOptions(opts, nil); ok {
		if selected, _, errPinned := m.pickNextLegacy(peekCtx, provider, model, pinned, nil); errPinned == nil && selected != nil {
			return selected, nil
		}
	}
	selected, _, errPick := m.pickNextLegacy(peekCtx, provider, model, opts, nil)
	if errPick != nil {
		return nil, errPick
	}
	if selected == nil {
		return nil, &Error{Code: "auth_not_found", Message: "selector returned no auth"}
	}
	return selected, nil
}
