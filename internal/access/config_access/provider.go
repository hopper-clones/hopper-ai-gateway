package configaccess

import (
	"context"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v8/sdk/access"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

// Register ensures the config-access providers (inline API keys and lane keys)
// match the configuration: each is registered when it has keys and removed otherwise.
func Register(cfg *sdkconfig.SDKConfig) {
	if cfg == nil {
		sdkaccess.UnregisterProvider(sdkaccess.AccessProviderTypeConfigAPIKey)
		sdkaccess.UnregisterProvider(sdkaccess.AccessProviderTypeConfigLaneKey)
		return
	}

	if keys := normalizeKeys(cfg.APIKeys); len(keys) == 0 {
		sdkaccess.UnregisterProvider(sdkaccess.AccessProviderTypeConfigAPIKey)
	} else {
		sdkaccess.RegisterProvider(
			sdkaccess.AccessProviderTypeConfigAPIKey,
			newProvider(sdkaccess.DefaultAccessProviderName, keys),
		)
	}

	if len(cfg.LaneKeys) == 0 {
		sdkaccess.UnregisterProvider(sdkaccess.AccessProviderTypeConfigLaneKey)
	} else {
		sdkaccess.RegisterProvider(
			sdkaccess.AccessProviderTypeConfigLaneKey,
			newLaneKeyProvider(cfg.LaneKeys, config.LaneKeyNow),
		)
	}
}

type provider struct {
	name string
	keys map[string]struct{}
}

func newProvider(name string, keys []string) *provider {
	providerName := strings.TrimSpace(name)
	if providerName == "" {
		providerName = sdkaccess.DefaultAccessProviderName
	}
	keySet := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		keySet[key] = struct{}{}
	}
	return &provider{name: providerName, keys: keySet}
}

func (p *provider) Identifier() string {
	if p == nil || p.name == "" {
		return sdkaccess.DefaultAccessProviderName
	}
	return p.name
}

func (p *provider) Authenticate(_ context.Context, r *http.Request) (*sdkaccess.Result, *sdkaccess.AuthError) {
	if p == nil {
		return nil, sdkaccess.NewNotHandledError()
	}
	if len(p.keys) == 0 {
		return nil, sdkaccess.NewNotHandledError()
	}
	candidates := requestCredentials(r)
	if len(candidates) == 0 {
		return nil, sdkaccess.NewNoCredentialsError()
	}
	for _, candidate := range candidates {
		if _, ok := p.keys[candidate.value]; ok {
			return &sdkaccess.Result{
				Provider:  p.Identifier(),
				Principal: candidate.value,
				Metadata: map[string]string{
					"source": candidate.source,
				},
			}, nil
		}
	}

	return nil, sdkaccess.NewInvalidCredentialError()
}

// credential is one client-supplied key and the header or query it came from.
type credential struct {
	value  string
	source string
}

// requestCredentials lists the non-empty client keys in the order they are checked.
func requestCredentials(r *http.Request) []credential {
	authHeader := r.Header.Get("Authorization")
	queryKey := ""
	queryAuthToken := ""
	if r.URL != nil {
		queryKey = r.URL.Query().Get("key")
		queryAuthToken = r.URL.Query().Get("auth_token")
	}
	candidates := []credential{
		{extractBearerToken(authHeader), "authorization"},
		{r.Header.Get("X-Goog-Api-Key"), "x-goog-api-key"},
		{r.Header.Get("X-Api-Key"), "x-api-key"},
		{queryKey, "query-key"},
		{queryAuthToken, "query-auth-token"},
	}
	present := candidates[:0]
	for _, candidate := range candidates {
		if candidate.value != "" {
			present = append(present, candidate)
		}
	}
	return present
}

func extractBearerToken(header string) string {
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return header
	}
	if strings.ToLower(parts[0]) != "bearer" {
		return header
	}
	return strings.TrimSpace(parts[1])
}

func normalizeKeys(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		if _, exists := seen[trimmedKey]; exists {
			continue
		}
		seen[trimmedKey] = struct{}{}
		normalized = append(normalized, trimmedKey)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}
