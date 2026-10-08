package auth

import (
	"context"
	"strings"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// laneFromMetadata returns the lane of the lane key that authenticated the request.
func laneFromMetadata(meta map[string]any) string {
	if len(meta) == 0 {
		return ""
	}
	lane, _ := meta[cliproxyexecutor.LaneMetadataKey].(string)
	return strings.TrimSpace(lane)
}

// LanePinnedAuthID returns the credential lane is pinned to, or "".
func (m *Manager) LanePinnedAuthID(lane string) string {
	if m == nil {
		return ""
	}
	cfg, _ := m.runtimeConfig.Load().(*internalconfig.Config)
	if cfg == nil {
		return ""
	}
	pin, ok := cfg.Routing.LanePinFor(lane)
	if !ok {
		return ""
	}
	return strings.TrimSpace(pin.AuthID)
}

// lanePinnedOptions returns options locked to the lane's pinned credential, or
// false when the request carries no lane, is already locked, or the lane has no pin.
func (m *Manager) lanePinnedOptions(opts cliproxyexecutor.Options, tried map[string]struct{}) (cliproxyexecutor.Options, bool) {
	if m == nil || m.HomeEnabled() || pinnedAuthIDFromMetadata(opts.Metadata) != "" {
		return opts, false
	}
	authID := m.LanePinnedAuthID(laneFromMetadata(opts.Metadata))
	if authID == "" {
		return opts, false
	}
	if _, used := tried[authID]; used {
		return opts, false
	}
	pinned := opts
	pinned.Metadata = make(map[string]any, len(opts.Metadata)+1)
	for key, value := range opts.Metadata {
		pinned.Metadata[key] = value
	}
	pinned.Metadata[cliproxyexecutor.PinnedAuthMetadataKey] = authID
	return pinned, true
}

// pickLanePinnedMixed tries the lane's pinned credential first. Any refusal
// (cooling down, exhausted, unsupported model, gone) falls back to normal
// selection: a lane pin is a preference, never a lock.
func (m *Manager) pickLanePinnedMixed(ctx context.Context, providers []string, model string, opts cliproxyexecutor.Options, tried map[string]struct{}) (*Auth, ProviderExecutor, string, bool) {
	pinned, ok := m.lanePinnedOptions(opts, tried)
	if !ok {
		return nil, nil, "", false
	}
	auth, executor, provider, err := m.pickNextMixedLegacy(ctx, providers, model, pinned, tried)
	if err != nil || auth == nil {
		return nil, nil, "", false
	}
	return auth, executor, provider, true
}

// pickLanePinned is pickLanePinnedMixed for a single provider.
func (m *Manager) pickLanePinned(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, tried map[string]struct{}) (*Auth, ProviderExecutor, bool) {
	pinned, ok := m.lanePinnedOptions(opts, tried)
	if !ok {
		return nil, nil, false
	}
	auth, executor, err := m.pickNextLegacy(ctx, provider, model, pinned, tried)
	if err != nil || auth == nil {
		return nil, nil, false
	}
	return auth, executor, true
}
