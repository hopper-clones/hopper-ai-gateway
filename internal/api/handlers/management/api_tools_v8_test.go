package management

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func TestAPICallV8NativeRefreshChangesResetFirstWithoutInference(t *testing.T) {
	now := time.Now()
	soon, later := now.Add(time.Hour).Unix(), now.Add(2*time.Hour).Unix()
	body := fmt.Sprintf(`{"rate_limit":{"primary_window":{"used_percent":25,"reset_at":%d},"secondary_window":{"used_percent":10,"reset_at":%d}},"additional_rate_limits":[{"limit_name":"gpt-5-codex","rate_limit":{"primary_window":{"used_percent":100,"reset_at":%d},"secondary_window":null}}]}`, soon, later, later)
	status := http.StatusOK
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/wham/usage" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	originalTransport := http.DefaultTransport
	// A real local TLS endpoint supplies the native response without provider credentials.
	http.DefaultTransport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}}
	defer func() {
		http.DefaultTransport.(*http.Transport).CloseIdleConnections()
		http.DefaultTransport = originalTransport
	}()
	manager := coreauth.NewManager(nil, nil, nil)
	for _, id := range []string{"a-unknown", "b-refreshed"} {
		if _, err := manager.Register(context.Background(), &coreauth.Auth{ID: id, Provider: "codex", Status: coreauth.StatusActive}); err != nil {
			t.Fatal(err)
		}
	}
	auth, _ := manager.GetByID("b-refreshed")
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	refresh := func() *httptest.ResponseRecorder {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"auth_index": auth.EnsureIndex(), "method": "GET", "url": "https://chatgpt.com/backend-api/wham/usage"})
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v8/management/requests/api-call", strings.NewReader(string(payload)))
		c.Request.Header.Set("Content-Type", "application/json")
		h.APICallV8(c)
		return rec
	}
	response := refresh()
	if response.Code != 200 {
		t.Fatalf("refresh %d: %s", response.Code, response.Body.String())
	}
	var envelope struct {
		RoutingObservation quotaRefreshObservation `json:"routing_observation"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.RoutingObservation.Status != "applied" {
		t.Fatalf("missing routing refresh result: %s", response.Body.String())
	}
	updated, _ := manager.GetByID(auth.ID)
	unknown, _ := manager.GetByID("a-unknown")
	picked, err := (&coreauth.ResetFirstSelector{}).Pick(context.Background(), "codex", "gpt-other", coreexecutor.Options{}, []*coreauth.Auth{unknown, updated})
	if err != nil || picked.ID != auth.ID {
		t.Fatalf("reset-first failed to use refresh: picked=%+v err=%v", picked, err)
	}
	windows := coreauth.RoutingQuotaWindows(updated, "gpt-5-codex", time.Now())
	foundNamed := false
	for _, window := range windows {
		if window.Exhausted && window.ResetAt.Unix() == later {
			foundNamed = true
		}
	}
	if !foundNamed {
		t.Fatalf("named exhausted model cap missing: %+v", windows)
	}
	before := updated.Quota.Clone()
	body = `{"rate_limit":{"primary_window":{"used_percent":0,"reset_at":1}}}`
	partialResponse := refresh() // Partial response must not erase weekly or model caps.
	if err := json.Unmarshal(partialResponse.Body.Bytes(), &envelope); err != nil || envelope.RoutingObservation.Status != "unsupported" {
		t.Fatalf("partial snapshot not visibly unsupported: %s", partialResponse.Body.String())
	}
	status = http.StatusUnauthorized
	body = `{"error":"expired"}`
	refresh() // API-call still relays the upstream response, but it is not a quota snapshot.
	updated, _ = manager.GetByID(auth.ID)
	if !updated.Quota.ObservedAt.Equal(before.ObservedAt) || updated.Quota.Signals["X-Codex-Quota0-Primary-Used-Percent"] != "100" {
		t.Fatalf("partial/error erased snapshot: %+v", updated.Quota)
	}
}

func TestNativeQuotaSignalsPreserveClaudeFamilyScopes(t *testing.T) {
	now := time.Now()
	reset := now.Add(time.Hour).UTC().Format(time.RFC3339)
	auth := &coreauth.Auth{Provider: "claude"}
	payload := []byte(fmt.Sprintf(`{"five_hour":{"utilization":25,"resets_at":%q},"seven_day":{"utilization":30,"resets_at":%q},"seven_day_opus":{"utilization":100,"resets_at":%q},"seven_day_sonnet":null}`, reset, reset, reset))
	headers, ok := nativeQuotaSignals(auth, payload, now)
	if !ok {
		t.Fatal("native Claude quota rejected")
	}
	if headers.Get("Anthropic-Ratelimit-Unified-5h-Utilization") != "0.25" {
		t.Fatalf("percentage conversion: %+v", headers)
	}
	auth.Quota.ObserveResponseHeadersForProvider("claude", headers, now)
	for _, model := range []string{"claude-opus-4", "claude-sonnet-4"} {
		exhausted := false
		for _, window := range coreauth.RoutingQuotaWindows(auth, model, now) {
			exhausted = exhausted || window.Exhausted
		}
		if exhausted != (model == "claude-opus-4") {
			t.Fatalf("family cap leaked for %s", model)
		}
	}
	partial := []byte(fmt.Sprintf(`{"five_hour":{"utilization":20,"resets_at":%q},"seven_day":{"utilization":20,"resets_at":%q}}`, reset, reset))
	if _, ok := nativeQuotaSignals(auth, partial, now); ok {
		t.Fatal("omitted prior model family accepted as complete snapshot")
	}
}

func TestNativeQuotaRequestAllowlist(t *testing.T) {
	for _, tc := range []struct {
		provider, method, url string
		want                  bool
	}{
		{"codex", "GET", "https://chatgpt.com/backend-api/wham/usage", true},
		{"claude", "GET", "https://api.anthropic.com/api/oauth/usage", true},
		{"claude", "GET", "https://chatgpt.com/backend-api/wham/usage", false},
		{"codex", "POST", "https://chatgpt.com/backend-api/wham/usage", false},
		{"codex", "GET", "https://chatgpt.com.evil/backend-api/wham/usage", false},
		{"codex", "GET", "https://chatgpt.com/backend-api/wham/usage?other=1", false},
	} {
		if got := nativeQuotaRequest(tc.provider, tc.method, tc.url); got != tc.want {
			t.Errorf("%+v = %t", tc, got)
		}
	}
}

func TestAPICallV8RetainsGenericBehavior(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Ordinary", "retained")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ordinary":"body"}`))
	}))
	defer server.Close()
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, nil)
	payload, _ := json.Marshal(map[string]string{"method": "GET", "url": server.URL})
	call := func(v8 bool) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v8/management/requests/api-call", strings.NewReader(string(payload)))
		c.Request.Header.Set("Content-Type", "application/json")
		if v8 {
			h.APICallV8(c)
		} else {
			h.APICall(c)
		}
		return rec
	}
	before, after := call(false), call(true)
	if before.Code != after.Code || before.Body.String() != after.Body.String() {
		t.Fatalf("generic behavior changed: old %d %s, v8 %d %s", before.Code, before.Body, after.Code, after.Body)
	}
}

func TestNativeQuotaOversizedSnapshotFailsWithoutTruncation(t *testing.T) {
	window := map[string]any{"used_percent": 100, "reset_at": time.Now().Add(time.Hour).Unix()}
	limit := map[string]any{"primary_window": window, "secondary_window": window}
	additional := []any{}
	for i := 0; i < 13; i++ {
		additional = append(additional, map[string]any{"limit_name": fmt.Sprintf("gpt-specific-%d", i), "rate_limit": limit})
	}
	body, _ := json.Marshal(map[string]any{"rate_limit": limit, "additional_rate_limits": additional})
	if _, ok := nativeQuotaSignals(&coreauth.Auth{Provider: "codex"}, body, time.Now()); ok {
		t.Fatal("oversized snapshot silently admitted partial model caps")
	}
}

func TestNativeQuotaSignalsPreserveProviderNamedFableCap(t *testing.T) {
	now := time.Now()
	reset := now.Add(time.Hour).UTC().Format(time.RFC3339)
	auth := &coreauth.Auth{Provider: "claude"}
	payload := []byte(fmt.Sprintf(`{"five_hour":{"utilization":10,"resets_at":%q},"seven_day":{"utilization":20,"resets_at":%q},"limits":[{"kind":"weekly_scoped","percent":100,"resets_at":%q,"is_active":true,"scope":{"model":{"display_name":"Fable 5"}}}]}`, reset, reset, reset))
	headers, ok := nativeQuotaSignals(auth, payload, now)
	if !ok || headers.Get("Anthropic-Ratelimit-Unified-7d-Fable-Utilization") != "1" {
		t.Fatalf("native Fable cap missing: %+v", headers)
	}
	auth.Quota.ObserveResponseHeadersForProvider("claude", headers, now)
	for _, model := range []string{"claude-fable-5", "claude-opus-4"} {
		exhausted := false
		for _, window := range coreauth.RoutingQuotaWindows(auth, model, now) {
			exhausted = exhausted || window.Exhausted
		}
		if exhausted != (model == "claude-fable-5") {
			t.Fatalf("Fable family isolation failed: %s", model)
		}
	}
	partial := []byte(fmt.Sprintf(`{"five_hour":{"utilization":10,"resets_at":%q},"seven_day":{"utilization":20,"resets_at":%q}}`, reset, reset))
	if _, ok := nativeQuotaSignals(auth, partial, now); ok {
		t.Fatal("omitted prior Fable cap replaced complete snapshot")
	}
}
