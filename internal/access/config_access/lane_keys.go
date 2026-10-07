package configaccess

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v8/sdk/access"
)

// laneKeyProvider authenticates expiring lane keys and tags the request with the
// lane, project and task the key was issued for. Expiry is checked at every
// request so a key refused here never needs a config reload.
type laneKeyProvider struct {
	keys map[string]config.LaneKey
	now  func() time.Time
}

func newLaneKeyProvider(keys []config.LaneKey, now func() time.Time) *laneKeyProvider {
	if now == nil {
		now = time.Now
	}
	byKey := make(map[string]config.LaneKey, len(keys))
	for _, key := range keys {
		secret := strings.TrimSpace(key.Key)
		if secret == "" {
			continue
		}
		byKey[secret] = key
	}
	return &laneKeyProvider{keys: byKey, now: now}
}

func (p *laneKeyProvider) Identifier() string {
	return sdkaccess.LaneKeyAccessProviderName
}

func (p *laneKeyProvider) Authenticate(_ context.Context, r *http.Request) (*sdkaccess.Result, *sdkaccess.AuthError) {
	if p == nil || len(p.keys) == 0 {
		return nil, sdkaccess.NewNotHandledError()
	}
	candidates := requestCredentials(r)
	if len(candidates) == 0 {
		return nil, sdkaccess.NewNoCredentialsError()
	}
	now := p.now()
	for _, candidate := range candidates {
		key, ok := p.keys[candidate.value]
		if !ok {
			continue
		}
		if key.Expired(now) {
			return nil, sdkaccess.NewInvalidCredentialError()
		}
		return &sdkaccess.Result{
			Provider:  p.Identifier(),
			Principal: candidate.value,
			Metadata: map[string]string{
				"source":      candidate.source,
				"lane_key_id": key.ID,
				"lane":        key.Lane,
				"project":     key.Project,
				"task":        key.Task,
			},
		}, nil
	}
	return nil, sdkaccess.NewInvalidCredentialError()
}
