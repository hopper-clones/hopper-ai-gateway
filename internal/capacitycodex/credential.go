package capacitycodex

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	codexauth "github.com/router-for-me/CLIProxyAPI/v8/internal/auth/codex"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// Credential states shown in management.
const (
	StateReady       = "ready"
	StateUnavailable = "unavailable"
)

// Typed reasons. They never carry a token, an email or a path.
const (
	ReasonAuthFileUnreadable = "CAPACITY_CODEX_AUTH_FILE_UNREADABLE"
	ReasonIdentityMismatch   = "CAPACITY_CODEX_IDENTITY_MISMATCH"
	ReasonRefreshFailed      = "CAPACITY_CODEX_REFRESH_FAILED"
	ReasonWriteFailed        = "CAPACITY_CODEX_WRITE_FAILED"
	ReasonTokenRejected      = "CAPACITY_CODEX_TOKEN_REJECTED"
)

// UnavailableError routes a request away from a Capacity Codex credential.
type UnavailableError struct {
	Reason string
	status int
}

func (e *UnavailableError) Error() string {
	return "capacity codex credential unavailable: " + e.Reason
}

// StatusCode lets the auth manager cool the credential down and try the others.
func (e *UnavailableError) StatusCode() int { return e.status }

func unavailable(reason string) *UnavailableError {
	status := http.StatusServiceUnavailable
	if reason == ReasonTokenRejected {
		status = http.StatusUnauthorized
	}
	return &UnavailableError{Reason: reason, status: status}
}

// RefreshFunc exchanges a refresh token for new tokens (CodexAuth.RefreshTokens).
type RefreshFunc func(ctx context.Context, refreshToken string) (*codexauth.CodexTokenData, error)

// Credential is the Runtime of a registered Capacity Codex auth. It holds the
// tokens of one official auth.json in memory only, re-reading the file when it
// changes and refreshing only an expired access token.
type Credential struct {
	path          string
	label         string
	expectedEmail string
	accountID     string
	now           func() time.Time
	refresh       RefreshFunc

	mu          sync.Mutex
	stamp       fileStamp
	tokens      authTokens
	claims      tokenClaims
	expiry      time.Time
	state       string
	reason      string
	failedStamp fileStamp
}

func newCredential(path, label, expectedEmail, accountID string, now func() time.Time, refresh RefreshFunc) *Credential {
	return &Credential{path: path, label: label, expectedEmail: expectedEmail, accountID: accountID, now: now, refresh: refresh}
}

// load reads the file and verifies it still belongs to the expected account.
// The caller holds c.mu.
func (c *Credential) load() error {
	stamp, errStat := statStamp(c.path)
	tokens, errRead := readAuthFile(c.path)
	if errStat != nil || errRead != nil {
		return c.fail(ReasonAuthFileUnreadable, fileStamp{})
	}
	claims, ok := c.verify(tokens)
	if !ok {
		return c.fail(ReasonIdentityMismatch, fileStamp{})
	}
	access, errAccess := parseClaims(tokens.AccessToken)
	if errAccess != nil || access.Exp <= 0 {
		return c.fail(ReasonAuthFileUnreadable, fileStamp{})
	}
	c.stamp, c.tokens, c.claims = stamp, tokens, claims
	c.expiry = time.Unix(access.Exp, 0)
	c.state, c.reason = StateReady, ""
	return nil
}

// verify checks the id_token identity against Capacity's expected account.
func (c *Credential) verify(tokens authTokens) (tokenClaims, bool) {
	claims, err := parseClaims(tokens.IDToken)
	if err != nil {
		return tokenClaims{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(claims.Email), strings.TrimSpace(c.expectedEmail)) ||
		claims.Auth.AccountID != c.accountID ||
		(tokens.AccountID != "" && tokens.AccountID != c.accountID) {
		return tokenClaims{}, false
	}
	return claims, true
}

func (c *Credential) fail(reason string, stamp fileStamp) error {
	c.state, c.reason, c.failedStamp = StateUnavailable, reason, stamp
	return unavailable(reason)
}

func (c *Credential) expired() bool { return !c.now().Before(c.expiry) }

// PrepareAccess makes the access token current before a request: it re-reads
// the file when it changed and refreshes only an expired token.
func (c *Credential) PrepareAccess(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	stamp, err := statStamp(c.path)
	if err != nil {
		return c.fail(ReasonAuthFileUnreadable, fileStamp{})
	}
	if c.state == StateUnavailable && c.reason == ReasonRefreshFailed && stamp == c.failedStamp {
		// The same file already failed to refresh; wait for the Codex app to change it.
		return unavailable(c.reason)
	}
	if stamp != c.stamp || c.state != StateReady {
		if err = c.load(); err != nil {
			return err
		}
	}
	if !c.expired() {
		return nil
	}
	// The Codex app may have refreshed the login since the last read.
	if err = c.load(); err != nil {
		return err
	}
	if !c.expired() {
		return nil
	}
	return c.refreshLocked(ctx)
}

// refreshLocked refreshes the expired tokens and writes them back to the same file.
func (c *Credential) refreshLocked(ctx context.Context) error {
	failedStamp := c.stamp
	if c.tokens.RefreshToken == "" || c.refresh == nil {
		return c.fail(ReasonRefreshFailed, failedStamp)
	}
	data, err := c.refresh(ctx, c.tokens.RefreshToken)
	if err != nil || data == nil || data.AccessToken == "" {
		log.Warnf("capacity-codex: %s refresh failed (%s)", c.label, ReasonRefreshFailed)
		return c.fail(ReasonRefreshFailed, failedStamp)
	}
	next := authTokens{IDToken: data.IDToken, AccessToken: data.AccessToken, RefreshToken: data.RefreshToken, AccountID: data.AccountID}
	if next.IDToken == "" {
		next.IDToken = c.tokens.IDToken
	}
	if next.RefreshToken == "" {
		next.RefreshToken = c.tokens.RefreshToken
	}
	if next.AccountID == "" {
		next.AccountID = c.tokens.AccountID
	}
	claims, ok := c.verify(next)
	if !ok {
		return c.fail(ReasonIdentityMismatch, failedStamp)
	}
	access, errAccess := parseClaims(next.AccessToken)
	if errAccess != nil || access.Exp <= 0 {
		return c.fail(ReasonRefreshFailed, failedStamp)
	}
	c.tokens, c.claims, c.expiry = next, claims, time.Unix(access.Exp, 0)
	c.state, c.reason = StateReady, ""
	if errWrite := writeAuthFile(c.path, next, c.now()); errWrite != nil {
		// The new tokens stay in memory so the account keeps serving.
		log.Warnf("capacity-codex: %s refreshed but the login file was not updated (%s)", c.label, ReasonWriteFailed)
		c.reason = ReasonWriteFailed
		return nil
	}
	if stamp, errStat := statStamp(c.path); errStat == nil {
		c.stamp = stamp
	}
	log.Infof("capacity-codex: %s access token refreshed and written back", c.label)
	return nil
}

// RefreshRejected handles an upstream 401: it re-reads the file and refreshes
// only an expired token; a rejected token that is not expired is not refreshed.
func (c *Credential) RefreshRejected(ctx context.Context) error {
	c.mu.Lock()
	rejected := c.tokens.AccessToken
	if err := c.load(); err != nil {
		c.mu.Unlock()
		return err
	}
	if c.tokens.AccessToken != rejected {
		c.mu.Unlock()
		return nil
	}
	if c.expired() {
		defer c.mu.Unlock()
		return c.refreshLocked(ctx)
	}
	defer c.mu.Unlock()
	return c.fail(ReasonTokenRejected, c.stamp)
}

// AccessToken returns the current access token held in memory.
func (c *Credential) AccessToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tokens.AccessToken
}

// PlanType returns the ChatGPT plan named by the id_token.
func (c *Credential) PlanType() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.claims.Auth.PlanType
}

// CapacityStatus reports the state and typed reason for management.
func (c *Credential) CapacityStatus() (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state, c.reason
}

// ShouldRefresh keeps the background refresh loop away: these tokens are
// refreshed only when a request finds them expired.
func (c *Credential) ShouldRefresh(time.Time, *coreauth.Auth) bool { return false }

func (c *Credential) sameAccount(path, expectedEmail, accountID string) bool {
	return c.path == path && strings.EqualFold(c.expectedEmail, expectedEmail) && c.accountID == accountID
}
