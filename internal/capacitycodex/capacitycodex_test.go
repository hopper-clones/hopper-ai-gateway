package capacitycodex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

var baseTime = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

func jwt(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString(payload) + ".sig"
}

func idToken(t *testing.T, email, account string) string {
	return jwt(t, map[string]any{"email": email, "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": account, "chatgpt_plan_type": "pro"}})
}

func accessToken(t *testing.T, name string, exp time.Time) string {
	return jwt(t, map[string]any{"exp": exp.Unix(), "name": name})
}

// writeLogin writes an official-format auth.json with the given access token.
func writeLogin(t *testing.T, home, email, account, access, refresh string, modTime time.Time) string {
	t.Helper()
	path := filepath.Join(home, "auth.json")
	body := `{
  "auth_mode": "chatgpt",
  "OPENAI_API_KEY": null,
  "tokens": {
    "id_token": "` + idToken(t, email, account) + `",
    "access_token": "` + access + `",
    "refresh_token": "` + refresh + `",
    "account_id": "` + account + `",
    "future_token_field": {"kept": true}
  },
  "last_refresh": "2026-10-01T00:00:00Z",
  "future_key": [1, 2, 3]
}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	return path
}

func readerAccount(id, label, home, email, account string) Account {
	a := Account{ID: id, Label: label}
	a.Account = &struct {
		AccountRef        string `json:"accountRef"`
		Home              string `json:"home"`
		ExpectedEmail     string `json:"expectedEmail"`
		ProviderAccountID string `json:"providerAccountId"`
	}{AccountRef: "ref-" + id, Home: home, ExpectedEmail: email, ProviderAccountID: account}
	return a
}

type registrations struct {
	auths   map[string]*coreauth.Auth
	removed []string
}

func newTestSource(t *testing.T, accounts func() []Account, clock *testClock, refresh RefreshFunc) (*Source, *registrations) {
	t.Helper()
	reg := &registrations{auths: map[string]*coreauth.Auth{}}
	read := func(context.Context) ([]Account, error) { return accounts(), nil }
	source := NewSource(read, refresh, func(a *coreauth.Auth) { reg.auths[a.ID] = a }, func(id string) {
		reg.removed = append(reg.removed, id)
		delete(reg.auths, id)
	})
	source.now = clock.Now
	return source, reg
}

func noRefresh(t *testing.T) RefreshFunc {
	return func(context.Context, string) error {
		t.Fatal("refresh must not be called")
		return nil
	}
}

func credentialOf(t *testing.T, auth *coreauth.Auth) *Credential {
	t.Helper()
	cred, ok := auth.Runtime.(*Credential)
	if !ok {
		t.Fatalf("runtime = %T, want *Credential", auth.Runtime)
	}
	return cred
}

func TestSyncRegistersCapacityAccounts(t *testing.T) {
	clock := &testClock{now: baseTime}
	homeA, homeB, homeMissing := t.TempDir(), t.TempDir(), t.TempDir()
	tokenA := accessToken(t, "a", baseTime.Add(time.Hour))
	writeLogin(t, homeA, "a@example.test", "acct-a", tokenA, "refresh-a", baseTime)
	writeLogin(t, homeB, "b@example.test", "acct-b", accessToken(t, "b", baseTime.Add(time.Hour)), "refresh-b", baseTime)
	refused := "CAPACITY_CONNECTION_NOT_CURRENT"
	accounts := []Account{
		readerAccount("hash-a", "codex 1", homeA, "A@example.test", "acct-a"),
		{ID: "hash-r", Label: "codex 2", Refused: &refused},
		readerAccount("hash-b", "codex 3", homeB, "b@example.test", "acct-b"),
		readerAccount("hash-m", "codex 4", homeMissing, "m@example.test", "acct-m"),
	}
	source, reg := newTestSource(t, func() []Account { return accounts }, clock, noRefresh(t))
	source.Sync(context.Background())

	if len(reg.auths) != 2 {
		t.Fatalf("registered %d auths, want 2", len(reg.auths))
	}
	auth := reg.auths["capacity-codex-hash-a"]
	if auth == nil || auth.Provider != "codex" || auth.Label != "codex 1" || !IsCapacityCodex(auth) {
		t.Fatalf("auth = %+v", auth)
	}
	if auth.Attributes[AttributeAccountID] != "hash-a" || auth.Attributes[coreauth.AttributeRuntimeOnly] != "true" || auth.Attributes["plan_type"] != "pro" {
		t.Fatalf("attributes = %v", auth.Attributes)
	}
	if _, has := auth.Metadata["access_token"]; has {
		t.Fatal("token copied into auth metadata")
	}
	if got := credentialOf(t, auth).AccessToken(); got != tokenA {
		t.Fatal("access token was not read from the login file")
	}
	if state, _ := credentialOf(t, auth).CapacityStatus(); state != StateReady {
		t.Fatalf("state = %q", state)
	}

	// A second sync with nothing changed re-registers nothing; a dropped account is removed.
	before := reg.auths["capacity-codex-hash-b"]
	accounts = accounts[:3]
	accounts[0].Refused = &refused
	source.Sync(context.Background())
	if reg.auths["capacity-codex-hash-b"] != before {
		t.Fatal("unchanged account was re-registered")
	}
	if len(reg.removed) != 1 || reg.removed[0] != "capacity-codex-hash-a" {
		t.Fatalf("removed = %v", reg.removed)
	}
}

func TestSyncSkipsMismatchedIdentity(t *testing.T) {
	clock := &testClock{now: baseTime}
	home := t.TempDir()
	writeLogin(t, home, "someone-else@example.test", "acct-a", accessToken(t, "a", baseTime.Add(time.Hour)), "refresh-a", baseTime)
	homeAcct := t.TempDir()
	writeLogin(t, homeAcct, "a@example.test", "acct-other", accessToken(t, "a", baseTime.Add(time.Hour)), "refresh-a", baseTime)
	accounts := []Account{
		readerAccount("hash-a", "codex 1", home, "a@example.test", "acct-a"),
		readerAccount("hash-b", "codex 2", homeAcct, "a@example.test", "acct-a"),
	}
	source, reg := newTestSource(t, func() []Account { return accounts }, clock, noRefresh(t))
	source.Sync(context.Background())
	if len(reg.auths) != 0 {
		t.Fatalf("registered %d auths for mismatched logins, want 0", len(reg.auths))
	}
}

func TestSyncKeepsRegistrationsWhenReaderFails(t *testing.T) {
	clock := &testClock{now: baseTime}
	home := t.TempDir()
	writeLogin(t, home, "a@example.test", "acct-a", accessToken(t, "a", baseTime.Add(time.Hour)), "refresh-a", baseTime)
	fail := false
	reg := &registrations{auths: map[string]*coreauth.Auth{}}
	source := NewSource(func(context.Context) ([]Account, error) {
		if fail {
			return nil, errors.New("CAPACITY_OWNER_UNAVAILABLE")
		}
		return []Account{readerAccount("hash-a", "codex 1", home, "a@example.test", "acct-a")}, nil
	}, noRefresh(t), func(a *coreauth.Auth) { reg.auths[a.ID] = a }, func(id string) { reg.removed = append(reg.removed, id) })
	source.now = clock.Now
	source.Sync(context.Background())
	fail = true
	source.Sync(context.Background())
	if len(reg.auths) != 1 || len(reg.removed) != 0 {
		t.Fatalf("auths=%d removed=%v", len(reg.auths), reg.removed)
	}
}

func registeredCredential(t *testing.T, home string, clock *testClock, refresh RefreshFunc) *Credential {
	t.Helper()
	accounts := []Account{readerAccount("hash-a", "codex 1", home, "a@example.test", "acct-a")}
	source, reg := newTestSource(t, func() []Account { return accounts }, clock, refresh)
	source.Sync(context.Background())
	auth := reg.auths["capacity-codex-hash-a"]
	if auth == nil {
		t.Fatal("account not registered")
	}
	return credentialOf(t, auth)
}

func TestPrepareRereadsFileAfterAppRewritesIt(t *testing.T) {
	clock := &testClock{now: baseTime}
	home := t.TempDir()
	first := accessToken(t, "first", baseTime.Add(time.Hour))
	writeLogin(t, home, "a@example.test", "acct-a", first, "refresh-a", baseTime)
	cred := registeredCredential(t, home, clock, noRefresh(t))
	if err := cred.PrepareAccess(context.Background()); err != nil || cred.AccessToken() != first {
		t.Fatalf("prepare = %v", err)
	}
	// The Codex app refreshes the login and rewrites the file.
	second := accessToken(t, "second", baseTime.Add(2*time.Hour))
	writeLogin(t, home, "a@example.test", "acct-a", second, "refresh-b", baseTime.Add(time.Minute))
	if err := cred.PrepareAccess(context.Background()); err != nil {
		t.Fatalf("prepare = %v", err)
	}
	if cred.AccessToken() != second {
		t.Fatal("rewritten login file was not re-read")
	}
}

func TestExpiredTokenRereadsFileBeforeRefreshing(t *testing.T) {
	clock := &testClock{now: baseTime}
	home := t.TempDir()
	path := writeLogin(t, home, "a@example.test", "acct-a", accessToken(t, "old", baseTime.Add(time.Minute)), "refresh-a", baseTime)
	cred := registeredCredential(t, home, clock, noRefresh(t))
	// The app refreshed the file with the same timestamp and size; the expired
	// token still sends the gateway back to the file before any refresh.
	fresh := accessToken(t, "new", baseTime.Add(time.Hour))
	writeLogin(t, home, "a@example.test", "acct-a", fresh, "refresh-a", baseTime)
	if err := os.Chtimes(path, baseTime, baseTime); err != nil {
		t.Fatal(err)
	}
	clock.now = baseTime.Add(2 * time.Minute)
	if err := cred.PrepareAccess(context.Background()); err != nil {
		t.Fatalf("prepare = %v", err)
	}
	if cred.AccessToken() != fresh {
		t.Fatal("expired token was not re-read from the file")
	}
}

func TestExpiredTokenDelegatesToOwnerAndReloadsItsDurableResult(t *testing.T) {
	clock := &testClock{now: baseTime}
	home := t.TempDir()
	path := writeLogin(t, home, "a@example.test", "acct-a", accessToken(t, "old", baseTime.Add(time.Minute)), "refresh-old", baseTime)
	newAccess := accessToken(t, "new", baseTime.Add(10*time.Hour))
	newID := idToken(t, "a@example.test", "acct-a")
	var calls atomic.Int32
	refresh := func(_ context.Context, accountRef string) error {
		calls.Add(1)
		if accountRef != "ref-hash-a" {
			t.Errorf("selected reference = %q", accountRef)
		}
		writeLogin(t, home, "a@example.test", "acct-a", newAccess, "refresh-new", clock.now)
		return nil
	}
	cred := registeredCredential(t, home, clock, refresh)
	if err := cred.PrepareAccess(context.Background()); err != nil || calls.Load() != 0 {
		t.Fatalf("unexpired token refreshed: err=%v calls=%d", err, calls.Load())
	}
	clock.now = baseTime.Add(5 * time.Minute)
	if err := cred.PrepareAccess(context.Background()); err != nil {
		t.Fatalf("prepare = %v", err)
	}
	if calls.Load() != 1 || cred.AccessToken() != newAccess {
		t.Fatalf("calls=%d", calls.Load())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]any
	if err = json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	tokens := file["tokens"].(map[string]any)
	if tokens["access_token"] != newAccess || tokens["refresh_token"] != "refresh-new" || tokens["id_token"] != newID || tokens["account_id"] != "acct-a" {
		t.Fatal("tokens were not written back")
	}
	if file["auth_mode"] != "chatgpt" || file["OPENAI_API_KEY"] != nil || file["future_key"] == nil || tokens["future_token_field"] == nil {
		t.Fatalf("other keys not preserved: %v", file)
	}
	if file["last_refresh"] != "2026-10-01T00:00:00Z" {
		t.Fatalf("last_refresh = %v", file["last_refresh"])
	}
	text := string(data)
	order := []string{`"auth_mode"`, `"OPENAI_API_KEY"`, `"tokens"`, `"last_refresh"`, `"future_key"`}
	for i := 1; i < len(order); i++ {
		if strings.Index(text, order[i-1]) > strings.Index(text, order[i]) {
			t.Fatalf("key order changed: %s", text)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v err=%v", info.Mode().Perm(), err)
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 1 {
		t.Fatalf("temporary files left: %d entries", len(entries))
	}
	// The next request uses the written token without another refresh.
	if err = cred.PrepareAccess(context.Background()); err != nil || calls.Load() != 1 {
		t.Fatalf("second prepare err=%v calls=%d", err, calls.Load())
	}
}

func TestRefreshRejectedWaitsForOwnerReplacement(t *testing.T) {
	clock := &testClock{now: baseTime}
	home := t.TempDir()
	writeLogin(t, home, "a@example.test", "acct-a", accessToken(t, "a", baseTime.Add(time.Hour)), "refresh-a", baseTime)
	cred := registeredCredential(t, home, clock, func(context.Context, string) error { return nil })
	var unavailableErr *UnavailableError
	if err := cred.RefreshRejected(context.Background()); !errors.As(err, &unavailableErr) || unavailableErr.Reason != ReasonTokenRejected || unavailableErr.StatusCode() != 401 {
		t.Fatalf("err = %v", err)
	}
	if err := cred.PrepareAccess(context.Background()); err == nil {
		t.Fatal("reused a rejected token during cooldown")
	}
	if err := cred.RefreshRejected(context.Background()); err == nil {
		t.Fatal("repeated rejected renewal escaped cooldown")
	}
	if err := cred.PrepareAccess(context.Background()); err == nil {
		t.Fatal("repeated rejection cleared the cooldown")
	}
	// After the app rewrites the login, the new token is used.
	next := accessToken(t, "b", baseTime.Add(2*time.Hour))
	writeLogin(t, home, "a@example.test", "acct-a", next, "refresh-b", baseTime.Add(time.Minute))
	if err := cred.RefreshRejected(context.Background()); err != nil || cred.AccessToken() != next {
		t.Fatalf("err = %v", err)
	}
}

func TestOwnerRefreshRequiresDurableResultAndTransientFailureCanRecover(t *testing.T) {
	clock := &testClock{now: baseTime}
	home := t.TempDir()
	path := writeLogin(t, home, "a@example.test", "acct-a", accessToken(t, "expired", baseTime.Add(-time.Hour)), "held-refresh", baseTime)
	calls := 0
	cred := newCredential(path, "codex 1", "a@example.test", "acct-a", clock.Now, func(context.Context, string) error {
		calls++
		if calls == 1 {
			return errors.New("owner temporarily unavailable")
		}
		if calls == 2 {
			return nil
		} // An ACK without a durable replacement is not success.
		writeLogin(t, home, "a@example.test", "acct-a", accessToken(t, "renewed", clock.now.Add(time.Hour)), "owner-only", clock.now)
		return nil
	})
	cred.accountRef = "selected"
	for attempt := 1; attempt <= 2; attempt++ {
		if err := cred.PrepareAccess(context.Background()); err == nil {
			t.Fatal("accepted missing durable renewal")
		}
		if err := cred.PrepareAccess(context.Background()); err == nil {
			t.Fatal("cooldown accepted expired token")
		}
		if calls != attempt {
			t.Fatalf("calls=%d want %d", calls, attempt)
		}
		clock.now = clock.now.Add(time.Minute)
	}
	if err := cred.PrepareAccess(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("calls=%d", calls)
	}
}
