package capacitycodex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// SourceName marks the credentials this package registers.
const SourceName = "capacity-codex"

// Auth attribute keys set on every registered credential.
const (
	AttributeCredentialSource = "credential_source"
	AttributeAccountID        = "capacity_account_id"
	AttributeAccountLabel     = "capacity_label"
	authIDPrefix              = "capacity-codex-"
)

// Source keeps one Codex auth registered per Capacity Codex account whose
// official login file exists and matches the account Capacity expects.
type Source struct {
	read    Reader
	refresh RefreshFunc
	upsert  func(*coreauth.Auth)
	remove  func(id string)
	now     func() time.Time

	mu      sync.Mutex
	current map[string]*Credential
}

// NewSource builds a source. upsert and remove register and unregister auths.
func NewSource(read Reader, refresh RefreshFunc, upsert func(*coreauth.Auth), remove func(string)) *Source {
	return &Source{read: read, refresh: refresh, upsert: upsert, remove: remove, now: time.Now, current: map[string]*Credential{}}
}

// Run syncs now and then every interval until ctx ends.
func (s *Source) Run(ctx context.Context, interval time.Duration) {
	s.Sync(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Sync(ctx)
		}
	}
}

// Sync reads Capacity's Codex accounts once and updates the registrations.
// A failed read keeps the current registrations.
func (s *Source) Sync(ctx context.Context) {
	accounts, err := s.read(ctx)
	if err != nil {
		log.Warnf("capacity-codex: account list unavailable (%s); keeping %d registered", err.Error(), s.count())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	desired := make(map[string]*Credential, len(accounts))
	for _, account := range accounts {
		id := authIDPrefix + account.ID
		cred := s.credentialFor(account)
		if cred == nil {
			continue
		}
		desired[id] = cred
		if s.current[id] != cred {
			s.upsert(s.authFor(id, account, cred))
		}
	}
	for id := range s.current {
		if desired[id] == nil {
			s.remove(id)
		}
	}
	s.current = desired
	log.Infof("capacity-codex: %d of %d Capacity Codex accounts registered", len(desired), len(accounts))
}

// credentialFor returns the credential for an account, reusing the current one
// when nothing about the account changed. The caller holds s.mu.
func (s *Source) credentialFor(account Account) *Credential {
	if account.Refused != nil {
		log.Infof("capacity-codex: %s skipped: %s", account.Label, *account.Refused)
		return nil
	}
	if account.Account == nil || !filepath.IsAbs(account.Account.Home) || strings.TrimSpace(account.ID) == "" {
		log.Infof("capacity-codex: %s skipped: CAPACITY_INVALID_RESPONSE", account.Label)
		return nil
	}
	path := filepath.Join(account.Account.Home, "auth.json")
	if existing := s.current[authIDPrefix+account.ID]; existing != nil && existing.sameAccount(path, account.Account.ExpectedEmail, account.Account.ProviderAccountID) {
		return existing
	}
	if _, err := os.Stat(path); err != nil {
		log.Infof("capacity-codex: %s skipped: no official login file", account.Label)
		return nil
	}
	cred := newCredential(path, account.Label, account.Account.ExpectedEmail, account.Account.ProviderAccountID, s.now, s.refresh)
	cred.accountRef = account.Account.AccountRef
	cred.mu.Lock()
	err := cred.load()
	cred.mu.Unlock()
	if err != nil {
		_, reason := cred.CapacityStatus()
		log.Warnf("capacity-codex: %s skipped: %s", account.Label, reason)
		return nil
	}
	return cred
}

func (s *Source) authFor(id string, account Account, cred *Credential) *coreauth.Auth {
	now := s.now()
	attributes := map[string]string{
		coreauth.AttributeRuntimeOnly: "true",
		coreauth.AttributeAuthKind:    coreauth.AuthKindOAuth,
		AttributeCredentialSource:     SourceName,
		AttributeAccountID:            account.ID,
		AttributeAccountLabel:         account.Label,
	}
	if plan := strings.TrimSpace(cred.PlanType()); plan != "" {
		attributes["plan_type"] = plan
	}
	return &coreauth.Auth{
		ID:         id,
		Provider:   "codex",
		Label:      account.Label,
		Status:     coreauth.StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		Attributes: attributes,
		// Identity only; the tokens stay with the Credential and its file.
		Metadata: map[string]any{
			"type":       "codex",
			"email":      account.Account.ExpectedEmail,
			"account_id": account.Account.ProviderAccountID,
		},
		Runtime: cred,
	}
}

func (s *Source) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.current)
}

// IsCapacityCodex reports whether an auth was registered by this source.
func IsCapacityCodex(auth *coreauth.Auth) bool {
	return auth != nil && auth.Attributes != nil && auth.Attributes[AttributeCredentialSource] == SourceName
}
