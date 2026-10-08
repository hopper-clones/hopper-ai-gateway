package capacitycodex

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// fileCodexExecutor prepares and reads tokens the way the codex executor does
// for file-backed credentials and records which token served each request.
type fileCodexExecutor struct {
	served []string
}

func (e *fileCodexExecutor) Identifier() string { return "codex" }

func (e *fileCodexExecutor) ShouldPrepareRequestAuth(auth *coreauth.Auth) bool {
	_, ok := auth.Runtime.(*Credential)
	return ok
}

func (e *fileCodexExecutor) PrepareRequestAuth(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return nil, auth.Runtime.(*Credential).PrepareAccess(ctx)
}

func (e *fileCodexExecutor) Execute(_ context.Context, auth *coreauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	e.served = append(e.served, auth.ID)
	return cliproxyexecutor.Response{Payload: []byte(auth.Runtime.(*Credential).AccessToken())}, nil
}

func (e *fileCodexExecutor) ExecuteStream(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, errors.New("not used")
}

func (e *fileCodexExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *fileCodexExecutor) CountTokens(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, errors.New("not used")
}

func (e *fileCodexExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, errors.New("not used")
}

func TestRefreshFailureMarksUnavailableAndOthersServe(t *testing.T) {
	clock := &testClock{now: baseTime}
	homeBad, homeGood := t.TempDir(), t.TempDir()
	writeLogin(t, homeBad, "a@example.test", "acct-a", accessToken(t, "a", baseTime.Add(time.Minute)), "refresh-a", baseTime)
	good := accessToken(t, "b", baseTime.Add(time.Hour))
	writeLogin(t, homeGood, "b@example.test", "acct-b", good, "refresh-b", baseTime)
	var refreshCalls atomic.Int32
	refresh := func(context.Context, string) error {
		refreshCalls.Add(1)
		return errors.New("owner unavailable")
	}

	manager := coreauth.NewManager(nil, &coreauth.RoundRobinSelector{}, nil)
	exec := &fileCodexExecutor{}
	manager.RegisterExecutor(exec)
	accounts := []Account{
		readerAccount("hash-bad", "codex 1", homeBad, "a@example.test", "acct-a"),
		readerAccount("hash-good", "codex 2", homeGood, "b@example.test", "acct-b"),
	}
	var bad *Credential
	source := NewSource(func(context.Context) ([]Account, error) { return accounts, nil }, refresh, func(auth *coreauth.Auth) {
		if _, err := manager.Register(coreauth.WithSkipPersist(context.Background()), auth); err != nil {
			t.Fatalf("register: %v", err)
		}
		registry.GetGlobalRegistry().RegisterClient(auth.ID, "codex", []*registry.ModelInfo{{ID: "capacity-codex-test-model"}})
		if auth.ID == "capacity-codex-hash-bad" {
			bad = auth.Runtime.(*Credential)
		}
	}, func(id string) { manager.Remove(context.Background(), id) })
	source.now = clock.Now
	source.Sync(context.Background())
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient("capacity-codex-hash-bad")
		registry.GetGlobalRegistry().UnregisterClient("capacity-codex-hash-good")
	})

	// The first account's token expires and its refresh fails.
	clock.now = baseTime.Add(5 * time.Minute)
	for i := 0; i < 4; i++ {
		resp, err := manager.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: "capacity-codex-test-model"}, cliproxyexecutor.Options{})
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		if string(resp.Payload) != good {
			t.Fatalf("request %d was not served by the healthy account", i)
		}
	}
	for _, id := range exec.served {
		if id != "capacity-codex-hash-good" {
			t.Fatalf("served by %s", id)
		}
	}
	state, reason := bad.CapacityStatus()
	if state != StateUnavailable || reason != ReasonRefreshFailed {
		t.Fatalf("state=%q reason=%q", state, reason)
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("refresh called %d times for an unchanged login, want 1", refreshCalls.Load())
	}
}
