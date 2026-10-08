package auth

import (
	"context"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func lanePinManager(t *testing.T, model string) *Manager {
	t.Helper()
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.executors["claude"] = schedulerTestExecutor{}
	reg := registry.GetGlobalRegistry()
	for _, id := range []string{"lane-pin-a", "lane-pin-b", "lane-pin-c"} {
		reg.RegisterClient(id, "claude", []*registry.ModelInfo{{ID: model}})
		if _, err := manager.Register(context.Background(), &Auth{ID: id, Provider: "claude"}); err != nil {
			t.Fatalf("Register(%s) error = %v", id, err)
		}
	}
	t.Cleanup(func() {
		for _, id := range []string{"lane-pin-a", "lane-pin-b", "lane-pin-c"} {
			reg.UnregisterClient(id)
		}
	})
	cfg := &internalconfig.Config{}
	cfg.Routing.LanePins = []internalconfig.LanePin{{Lane: "search-index", AuthID: "lane-pin-b", PinnedAt: "2026-10-07T09:02:00Z"}}
	manager.SetConfig(cfg)
	return manager
}

func laneOptions(lane string) cliproxyexecutor.Options {
	return cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.LaneMetadataKey: lane}}
}

func TestLanePinSteersSelectionAndFallsBackWhenOut(t *testing.T) {
	const model = "lane-pin-test-model"
	manager := lanePinManager(t, model)

	for i := 0; i < 4; i++ {
		got, _, _, err := manager.pickNextMixed(context.Background(), []string{"claude"}, model, laneOptions("search-index"), nil)
		if err != nil || got == nil || got.ID != "lane-pin-b" {
			t.Fatalf("pinned pick #%d = %v, %v; want lane-pin-b", i, got, err)
		}
	}
	// Another lane keeps rotating.
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		got, _, _, err := manager.pickNextMixed(context.Background(), []string{"claude"}, model, laneOptions("docs-sweep"), nil)
		if err != nil || got == nil {
			t.Fatalf("unpinned pick #%d error = %v", i, err)
		}
		seen[got.ID] = true
	}
	if len(seen) < 2 {
		t.Fatalf("an unpinned lane must rotate, saw %v", seen)
	}

	retryAfter := time.Hour
	manager.MarkResult(context.Background(), Result{
		AuthID: "lane-pin-b", Provider: "claude", Model: model, Success: false,
		Error: &Error{HTTPStatus: 429, Message: "quota"}, RetryAfter: &retryAfter,
	})
	for i := 0; i < 3; i++ {
		got, _, _, err := manager.pickNextMixed(context.Background(), []string{"claude"}, model, laneOptions("search-index"), nil)
		if err != nil || got == nil {
			t.Fatalf("fallback pick #%d error = %v", i, err)
		}
		if got.ID == "lane-pin-b" {
			t.Fatalf("fallback pick #%d chose the exhausted pinned credential", i)
		}
	}
	// The read-only pick agrees.
	peeked, err := manager.PeekAuth(context.Background(), "claude", model, laneOptions("search-index"))
	if err != nil || peeked == nil || peeked.ID == "lane-pin-b" {
		t.Fatalf("PeekAuth with pinned-out lane = %v, %v", peeked, err)
	}
}

func TestLanePinTriedCredentialIsNotRetried(t *testing.T) {
	const model = "lane-pin-tried-model"
	manager := lanePinManager(t, model)
	tried := map[string]struct{}{"lane-pin-b": {}}
	got, _, _, err := manager.pickNextMixed(context.Background(), []string{"claude"}, model, laneOptions("search-index"), tried)
	if err != nil || got == nil || got.ID == "lane-pin-b" {
		t.Fatalf("pick after the pinned credential failed = %v, %v", got, err)
	}
	if manager.LanePinnedAuthID("search-index") != "lane-pin-b" || manager.LanePinnedAuthID("other") != "" {
		t.Fatal("LanePinnedAuthID must read the runtime config")
	}
}
