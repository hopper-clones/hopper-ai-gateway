package configaccess

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v8/sdk/access"
)

func laneRequest(key string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req
}

func TestLaneKeyProviderAuthenticatesLiveKeysAndRefusesExpired(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	keys := []config.LaneKey{
		{ID: "lk-live", Key: "lane-live", Lane: "lane-test", Project: "project:822b", Task: "task:a929eb4f", ExpiresAt: "2026-10-08T00:00:00Z"},
		{ID: "lk-expired", Key: "lane-expired", Lane: "lane-b", ExpiresAt: "2026-10-01T00:00:00Z"},
	}
	provider := newLaneKeyProvider(keys, func() time.Time { return now })
	if provider.Identifier() != sdkaccess.LaneKeyAccessProviderName {
		t.Fatalf("identifier = %s", provider.Identifier())
	}

	result, authErr := provider.Authenticate(context.Background(), laneRequest("lane-live"))
	if authErr != nil {
		t.Fatalf("live key refused: %v", authErr)
	}
	if result.Principal != "lane-live" || result.Provider != sdkaccess.LaneKeyAccessProviderName {
		t.Fatalf("result = %+v", result)
	}
	for key, want := range map[string]string{"lane": "lane-test", "project": "project:822b", "task": "task:a929eb4f", "lane_key_id": "lk-live", "source": "authorization"} {
		if result.Metadata[key] != want {
			t.Fatalf("metadata[%s] = %q, want %q (%+v)", key, result.Metadata[key], want, result.Metadata)
		}
	}

	_, authErr = provider.Authenticate(context.Background(), laneRequest("lane-expired"))
	if authErr == nil || !sdkaccess.IsAuthErrorCode(authErr, sdkaccess.AuthErrorCodeInvalidCredential) {
		t.Fatalf("expired key must be refused as invalid, got %v", authErr)
	}
	_, authErr = provider.Authenticate(context.Background(), laneRequest("unknown"))
	if authErr == nil || !sdkaccess.IsAuthErrorCode(authErr, sdkaccess.AuthErrorCodeInvalidCredential) {
		t.Fatalf("unknown key must be invalid, got %v", authErr)
	}
	_, authErr = provider.Authenticate(context.Background(), laneRequest(""))
	if authErr == nil || !sdkaccess.IsAuthErrorCode(authErr, sdkaccess.AuthErrorCodeNoCredentials) {
		t.Fatalf("missing credentials, got %v", authErr)
	}

	// The clock moving past the expiry refuses the previously live key.
	now = now.Add(48 * time.Hour)
	if _, authErr = provider.Authenticate(context.Background(), laneRequest("lane-live")); authErr == nil {
		t.Fatal("key must be refused once expired")
	}
}

func TestRegisterAddsAndRemovesLaneKeyProvider(t *testing.T) {
	t.Cleanup(func() { Register(nil) })
	cfg := &config.SDKConfig{APIKeys: []string{"plain"}, LaneKeys: []config.LaneKey{{ID: "lk", Key: "lane", Lane: "lane-test", ExpiresAt: "2099-01-01T00:00:00Z"}}}
	Register(cfg)
	if !hasProvider(sdkaccess.LaneKeyAccessProviderName) || !hasProvider(sdkaccess.DefaultAccessProviderName) {
		t.Fatalf("providers = %v", providerNames())
	}
	Register(&config.SDKConfig{APIKeys: []string{"plain"}})
	if hasProvider(sdkaccess.LaneKeyAccessProviderName) || !hasProvider(sdkaccess.DefaultAccessProviderName) {
		t.Fatalf("providers after removing lane keys = %v", providerNames())
	}
	Register(&config.SDKConfig{LaneKeys: cfg.LaneKeys})
	if !hasProvider(sdkaccess.LaneKeyAccessProviderName) || hasProvider(sdkaccess.DefaultAccessProviderName) {
		t.Fatalf("providers with lane keys only = %v", providerNames())
	}
	Register(nil)
	if len(providerNames()) != 0 {
		t.Fatalf("providers after nil config = %v", providerNames())
	}
}

func providerNames() []string {
	var names []string
	for _, provider := range sdkaccess.RegisteredProviders() {
		names = append(names, provider.Identifier())
	}
	return names
}

func hasProvider(name string) bool {
	for _, candidate := range providerNames() {
		if candidate == name {
			return true
		}
	}
	return false
}
