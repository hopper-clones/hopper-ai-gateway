package auth

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func resetAuth(id string, now time.Time, after time.Duration) *Auth {
	return &Auth{ID: id, Provider: "claude", Quota: QuotaState{ObservedAt: now, Signals: map[string]string{
		"Anthropic-Ratelimit-Unified-5h-Status": "allowed",
		"Anthropic-Ratelimit-Unified-5h-Reset":  strconv.FormatInt(now.Add(after).Unix(), 10),
	}}}
}

func TestResetFirstRanksAvailableWindows(t *testing.T) {
	now := time.Now()
	soon := resetAuth("z-soon", now, time.Hour)
	later := resetAuth("b-later", now, 2*time.Hour)
	unknown := &Auth{ID: "a-unknown", Provider: "claude"}
	stale := resetAuth("c-stale", now.Add(-QuotaObservationFreshness-time.Second), QuotaObservationFreshness+time.Minute)
	blocked := resetAuth("d-exhausted", now, time.Minute)
	blocked.Quota.Signals["Anthropic-Ratelimit-Unified-7d-Utilization"] = "1"
	blocked.Quota.Signals["Anthropic-Ratelimit-Unified-7d-Reset"] = strconv.FormatInt(now.Add(24*time.Hour).Unix(), 10)
	disabled := resetAuth("e-disabled", now, time.Second)
	disabled.Disabled = true
	selector := &ResetFirstSelector{}
	for _, auths := range [][]*Auth{{unknown, later, stale, blocked, disabled, soon}, {soon, disabled, blocked, stale, later, unknown}} {
		got, err := selector.Pick(context.Background(), "claude", "claude-sonnet-4", cliproxyexecutor.Options{}, auths)
		if err != nil || got.ID != soon.ID {
			t.Fatalf("got %v, %v; want soon", got, err)
		}
	}
	// Reset proximity never overrides explicit credential priority.
	later.Attributes = map[string]string{"priority": "10"}
	got, err := selector.Pick(context.Background(), "claude", "claude-sonnet-4", cliproxyexecutor.Options{}, []*Auth{soon, later})
	if err != nil || got.ID != later.ID {
		t.Fatalf("priority lost: %v %v", got, err)
	}
}

func TestObservedQuotaExhaustionScopeAndExpiry(t *testing.T) {
	now := time.Unix(1800000000, 0)
	cases := []struct {
		name, model string
		signals     map[string]string
		observed    time.Time
		blocked     bool
		reset       time.Time
	}{
		{"model weekly sonnet", "claude-sonnet-4", map[string]string{"Anthropic-Ratelimit-Unified-7d-Sonnet-Status": "rejected", "Anthropic-Ratelimit-Unified-7d-Sonnet-Reset": "1800003600"}, now, true, now.Add(time.Hour)},
		{"model weekly opus unaffected", "claude-opus-4", map[string]string{"Anthropic-Ratelimit-Unified-7d-Sonnet-Status": "rejected", "Anthropic-Ratelimit-Unified-7d-Sonnet-Reset": "1800003600"}, now, false, time.Time{}},
		{"older Claude family name", "claude-3-7-sonnet", map[string]string{"Anthropic-Ratelimit-Unified-7d-Sonnet-Utilization": "1", "Anthropic-Ratelimit-Unified-7d-Sonnet-Reset": "1800003600"}, now, true, now.Add(time.Hour)},
		{"stale exhausted future", "claude-opus-4", map[string]string{"Anthropic-Ratelimit-Unified-5h-Utilization": "1", "Anthropic-Ratelimit-Unified-5h-Reset": "1800003600"}, now.Add(-time.Hour), true, now.Add(time.Hour)},
		{"expired exhaustion", "claude-opus-4", map[string]string{"Anthropic-Ratelimit-Unified-5h-Status": "rejected", "Anthropic-Ratelimit-Unified-5h-Reset": "1799999999"}, now, false, time.Time{}},
		{"fresh missing reset", "claude-opus-4", map[string]string{"Anthropic-Ratelimit-Unified-5h-Status": "rejected"}, now, true, time.Time{}},
		{"stale missing reset", "claude-opus-4", map[string]string{"Anthropic-Ratelimit-Unified-5h-Status": "rejected"}, now.Add(-time.Hour), false, time.Time{}},
		{"overage denial alone", "claude-opus-4", map[string]string{"Anthropic-Ratelimit-Unified-Overage-Status": "rejected"}, now, false, time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Auth{Provider: "claude", Quota: QuotaState{ObservedAt: tc.observed, Signals: tc.signals}}
			blocked, _, reset := observedQuotaBlock(a, tc.model, now)
			if blocked != tc.blocked || !reset.Equal(tc.reset) {
				t.Fatalf("blocked=%v reset=%v", blocked, reset)
			}
		})
	}
	// All applicable caps must recover, so report the latest exhausted reset.
	a := resetAuth("multi", now, time.Hour)
	a.Quota.Signals["Anthropic-Ratelimit-Unified-5h-Status"] = "rejected"
	a.Quota.Signals["Anthropic-Ratelimit-Unified-7d-Status"] = "rejected"
	a.Quota.Signals["Anthropic-Ratelimit-Unified-7d-Reset"] = "1800086400"
	_, _, reset := observedQuotaBlock(a, "claude-opus-4", now)
	if !reset.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("recovery=%v", reset)
	}
}

func TestCodexQuotaScopesAndRelativeReset(t *testing.T) {
	now := time.Unix(1800000000, 0)
	a := &Auth{Provider: "codex", Quota: QuotaState{ObservedAt: now, Signals: map[string]string{
		"X-Codex-Primary-Used-Percent": "10", "X-Codex-Primary-Reset-After-Seconds": "600",
		"X-Codex-Bengalfox-Limit-Name":             "gpt-5.3-codex-spark",
		"X-Codex-Bengalfox-Secondary-Used-Percent": "100", "X-Codex-Bengalfox-Secondary-Reset-At": "1800003600",
	}}}
	if blocked, _, _ := observedQuotaBlock(a, "gpt-5.3-codex", now); blocked {
		t.Fatal("unrelated named model blocked")
	}
	if blocked, _, _ := observedQuotaBlock(a, "gpt-5.3-codex-spark", now); !blocked {
		t.Fatal("named model exhaustion ignored")
	}
	windows := RoutingQuotaWindows(a, "gpt-5.3-codex", now)
	if len(windows) != 1 || !windows[0].ResetAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("windows=%+v", windows)
	}
	a.Quota.Signals["X-Codex-Secondary-Used-Percent"] = "100"
	a.Quota.Signals["X-Codex-Secondary-Reset-At"] = "1800086400"
	if blocked, _, _ := observedQuotaBlock(a, "gpt-5.3-codex", now); !blocked {
		t.Fatal("broad weekly exhaustion ignored")
	}
}

func TestResetFirstAffinityAndConcurrentPicks(t *testing.T) {
	now := time.Now()
	a := resetAuth("a", now, time.Hour)
	b := resetAuth("b", now, 2*time.Hour)
	s := NewSessionAffinitySelector(&ResetFirstSelector{})
	defer s.Stop()
	opts := cliproxyexecutor.Options{Headers: http.Header{"X-Session-Id": []string{"warm"}}}
	got, err := s.Pick(context.Background(), "claude", "claude-sonnet-4", opts, []*Auth{a, b})
	if err != nil || got.ID != "a" {
		t.Fatalf("initial=%v %v", got, err)
	}
	b.Quota.Signals["Anthropic-Ratelimit-Unified-5h-Reset"] = strconv.FormatInt(now.Add(time.Minute).Unix(), 10)
	got, err = s.Pick(context.Background(), "claude", "claude-sonnet-4", opts, []*Auth{a, b})
	if err != nil || got.ID != "a" {
		t.Fatalf("warm affinity moved=%v %v", got, err)
	}
	a.Quota.Signals["Anthropic-Ratelimit-Unified-5h-Status"] = "rejected"
	got, err = s.Pick(context.Background(), "claude", "claude-sonnet-4", opts, []*Auth{a, b})
	if err != nil || got.ID != "b" {
		t.Fatalf("exhausted affinity did not fail over=%v %v", got, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			o := cliproxyexecutor.Options{Headers: http.Header{"X-Session-Id": []string{fmt.Sprintf("cold-%d", i)}}}
			got, err := s.Pick(context.Background(), "claude", "claude-sonnet-4", o, []*Auth{a, b})
			if err != nil || got.ID != "b" {
				t.Errorf("concurrent pick=%v %v", got, err)
			}
		}(i)
	}
	wg.Wait()
}
