package auth

import (
	"context"
	"net/http"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func TestObserveQuotaHeadersPreservesExecutionAndRejectsStale(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(-time.Second)
	m := NewManager(nil, &ResetFirstSelector{}, nil)
	a := &Auth{ID: "passive-quota", Provider: "claude", Status: StatusError, Unavailable: true, Quota: QuotaState{Exceeded: true, Reason: "credential_quota", NextRecoverAt: now.Add(time.Hour), BackoffLevel: 2}}
	if _, err := m.Register(ctx, a); err != nil {
		t.Fatal(err)
	}
	before, _ := m.GetByID(a.ID)
	h := http.Header{"Anthropic-Ratelimit-Unified-5h-Reset": []string{strconv.FormatInt(now.Add(time.Hour).Unix(), 10)}, "Anthropic-Ratelimit-Unified-5h-Utilization": []string{"0.2"}}
	if err := m.ObserveQuotaHeaders(ctx, a.ID, "claude", h, now); err != nil {
		t.Fatal(err)
	}
	after, _ := m.GetByID(a.ID)
	if !reflect.DeepEqual(cooldownFieldsOf(before.Quota), cooldownFieldsOf(after.Quota)) || before.Status != after.Status || before.Unavailable != after.Unavailable || before.Success != after.Success || before.Failed != after.Failed || !before.LastRefreshedAt.Equal(after.LastRefreshedAt) {
		t.Fatal("passive refresh changed execution or cooldown state")
	}
	old := http.Header{"Anthropic-Ratelimit-Unified-5h-Utilization": []string{"1"}}
	for _, input := range []struct {
		h  http.Header
		at time.Time
	}{{old, now.Add(-time.Hour)}, {nil, now.Add(time.Millisecond)}} {
		if err := m.ObserveQuotaHeaders(ctx, a.ID, "claude", input.h, input.at); err != nil {
			t.Fatal(err)
		}
	}
	after, _ = m.GetByID(a.ID)
	if after.Quota.Signals["Anthropic-Ratelimit-Unified-5h-Utilization"] != "0.2" {
		t.Fatal("older or empty response replaced current snapshot")
	}
	if err := m.ObserveQuotaHeaders(ctx, a.ID, "codex", http.Header{"X-Codex-Primary-Used-Percent": []string{"20"}}, now); err == nil {
		t.Fatal("provider mismatch accepted")
	}
	if err := m.ObserveQuotaHeaders(ctx, a.ID, "claude", h, time.Now().Add(time.Hour)); err == nil {
		t.Fatal("future timestamp accepted")
	}
}

func TestResetFirstManagerUsesRefreshedModelScopedQuota(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(-time.Second)
	s := NewSessionAffinitySelector(&ResetFirstSelector{})
	defer s.Stop()
	m := NewManager(nil, s, nil)
	m.RegisterExecutor(&mockCustomErrorExecutor{identifier: "claude"})
	const alias = "reset-routing-alias"
	const target = "claude-sonnet-4"
	for _, id := range []string{"reset-a", "reset-b"} {
		registry.GetGlobalRegistry().RegisterClient(id, "claude", []*registry.ModelInfo{{ID: alias}, {ID: target}, {ID: "claude-opus-4"}})
		t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(id) })
		if _, err := m.Register(ctx, &Auth{ID: id, Provider: "claude", Status: StatusActive}); err != nil {
			t.Fatal(err)
		}
	}
	m.SetOAuthModelAlias(map[string][]internalconfig.OAuthModelAlias{"claude": {{Name: target, Alias: alias, Fork: true}}})
	if m.useSchedulerFastPath() {
		t.Fatal("cached scheduler must not bypass reset-first affinity")
	}
	headers := func(reset time.Time, exhausted bool) http.Header {
		status := "allowed"
		if exhausted {
			status = "rejected"
		}
		return http.Header{"Anthropic-Ratelimit-Unified-7d-Sonnet-Status": []string{status}, "Anthropic-Ratelimit-Unified-7d-Sonnet-Reset": []string{strconv.FormatInt(reset.Unix(), 10)}}
	}
	if err := m.ObserveQuotaHeaders(ctx, "reset-a", "claude", headers(now.Add(time.Hour), false), now); err != nil {
		t.Fatal(err)
	}
	if err := m.ObserveQuotaHeaders(ctx, "reset-b", "claude", headers(now.Add(2*time.Hour), false), now); err != nil {
		t.Fatal(err)
	}
	opts := func(session string) cliproxyexecutor.Options {
		return cliproxyexecutor.Options{Headers: http.Header{"X-Session-Id": []string{session}}}
	}
	selectID := func(model, session, want string) {
		a, err := m.SelectAuth(ctx, "claude", model, opts(session))
		if err != nil || a == nil || a.ID != want {
			t.Fatalf("model %s session %s got %v %v want %s", model, session, a, err, want)
		}
	}
	selectID(alias, "warm", "reset-a")
	if err := m.ObserveQuotaHeaders(ctx, "reset-b", "claude", headers(now.Add(time.Minute), false), now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	selectID(alias, "warm", "reset-a")
	selectID(alias, "cold", "reset-b")
	if err := m.ObserveQuotaHeaders(ctx, "reset-a", "claude", headers(now.Add(time.Hour), true), now.Add(2*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	selectID(alias, "warm", "reset-b")
	selectID("claude-opus-4", "opus", "reset-a")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.ObserveQuotaHeaders(ctx, "reset-b", "claude", headers(now.Add(time.Minute), false), now.Add(3*time.Millisecond)); err != nil {
				t.Error(err)
			}
			a, err := m.SelectAuth(ctx, "claude", alias, opts("warm"))
			if err != nil || a == nil || a.ID != "reset-b" {
				t.Errorf("concurrent refresh/select %v %v", a, err)
			}
		}()
	}
	wg.Wait()
}
