package executor

import (
	"context"
	"errors"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

type fakeFileCredential struct {
	token       string
	prepareErr  error
	refreshErr  error
	prepares    int
	refreshes   int
	nextOnRetry string
}

func (f *fakeFileCredential) PrepareAccess(context.Context) error {
	f.prepares++
	return f.prepareErr
}

func (f *fakeFileCredential) RefreshRejected(context.Context) error {
	f.refreshes++
	if f.refreshErr == nil && f.nextOnRetry != "" {
		f.token = f.nextOnRetry
	}
	return f.refreshErr
}

func (f *fakeFileCredential) AccessToken() string { return f.token }

func TestCodexExecutorReadsFileBackedCredential(t *testing.T) {
	cred := &fakeFileCredential{token: "file-token", nextOnRetry: "rewritten-token"}
	auth := &cliproxyauth.Auth{ID: "capacity-codex-x", Provider: "codex", Runtime: cred, Metadata: map[string]any{"account_id": "acct"}}
	exec := NewCodexAutoExecutor(nil)
	if !exec.ShouldPrepareRequestAuth(auth) {
		t.Fatal("file-backed auth is not prepared")
	}
	if exec.ShouldPrepareRequestAuth(&cliproxyauth.Auth{Provider: "codex", Metadata: map[string]any{"access_token": "x"}}) {
		t.Fatal("ordinary codex auth is prepared")
	}
	if updated, err := exec.PrepareRequestAuth(context.Background(), auth); err != nil || updated != nil || cred.prepares != 1 {
		t.Fatalf("prepare updated=%v err=%v prepares=%d", updated, err, cred.prepares)
	}
	if key, _ := codexCreds(auth); key != "file-token" {
		t.Fatalf("codexCreds = %q", key)
	}
	if _, err := exec.Refresh(context.Background(), auth); err != nil || cred.refreshes != 1 {
		t.Fatalf("refresh err=%v refreshes=%d", err, cred.refreshes)
	}
	if key, _ := codexCreds(auth); key != "rewritten-token" {
		t.Fatalf("codexCreds after refresh = %q", key)
	}
	if _, has := auth.Metadata["access_token"]; has {
		t.Fatal("refresh copied a token into metadata")
	}

	failure := errors.New("unavailable")
	cred.prepareErr, cred.refreshErr = failure, failure
	if _, err := exec.PrepareRequestAuth(context.Background(), auth); !errors.Is(err, failure) {
		t.Fatalf("prepare err = %v", err)
	}
	if _, err := exec.Refresh(context.Background(), auth); !errors.Is(err, failure) {
		t.Fatalf("refresh err = %v", err)
	}
}
