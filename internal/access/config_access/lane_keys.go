package configaccess

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
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
	// keys is indexed by the SHA-256 of the secret; the plaintext is never held
	// and the stored digest is compared in constant time.
	keys map[[sha256.Size]byte]laneKeyEntry
	now  func() time.Time
}

type laneKeyEntry struct {
	digest [sha256.Size]byte
	key    config.LaneKey
}

func newLaneKeyProvider(keys []config.LaneKey, now func() time.Time) *laneKeyProvider {
	if now == nil {
		now = time.Now
	}
	byDigest := make(map[[sha256.Size]byte]laneKeyEntry, len(keys))
	for _, key := range keys {
		secret := strings.TrimSpace(key.Key)
		if secret == "" {
			continue
		}
		digest := sha256.Sum256([]byte(secret))
		key.Key = ""
		byDigest[digest] = laneKeyEntry{digest: digest, key: key}
	}
	return &laneKeyProvider{keys: byDigest, now: now}
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
		digest := sha256.Sum256([]byte(candidate.value))
		entry, ok := p.keys[digest]
		if !ok || subtle.ConstantTimeCompare(entry.digest[:], digest[:]) != 1 {
			continue
		}
		key := entry.key
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
