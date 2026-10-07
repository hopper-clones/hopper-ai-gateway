package management

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage/feed"
)

type pickTestExecutor struct{ provider string }

func (e *pickTestExecutor) Identifier() string { return e.provider }
func (e *pickTestExecutor) Execute(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, errors.New("pick must not execute")
}
func (e *pickTestExecutor) ExecuteStream(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, errors.New("pick must not execute")
}
func (e *pickTestExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}
func (e *pickTestExecutor) CountTokens(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, errors.New("pick must not count")
}
func (e *pickTestExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, errors.New("pick must not request")
}

func pickRequest(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v8/management/routing/pick", strings.NewReader(body))
	h.PostRoutingPick(c)
	return rec
}

func TestRoutingPickSelectsWithoutExecuting(t *testing.T) {
	const model = "claude-fable-5-1-pick-test"
	now := time.Now()
	reset := now.Add(2 * time.Hour).Truncate(time.Second)
	manager := coreauth.NewManager(nil, &coreauth.ResetFirstSelector{}, nil)
	manager.RegisterExecutor(&pickTestExecutor{provider: "claude"})
	auth := &coreauth.Auth{
		ID:       "pick-claude-1",
		Provider: "claude",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"email": "Owner@Example.com", "type": "claude"},
		Quota: coreauth.QuotaState{ObservedAt: now, Signals: map[string]string{
			"Anthropic-Ratelimit-Unified-5h-Utilization": "0.3",
			"Anthropic-Ratelimit-Unified-5h-Reset":       reset.Format(time.RFC3339),
		}},
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, "claude", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	h := &Handler{authManager: manager}

	rec := pickRequest(t, h, `{"model":"`+model+`","lane":"lane-test"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("pick status = %d body=%s", rec.Code, rec.Body.String())
	}
	var picked struct {
		AuthID      string  `json:"auth_id"`
		AccountHash *string `json:"account_hash"`
		Provider    string  `json:"provider"`
		Lane        string  `json:"lane"`
		Reason      string  `json:"reason"`
		Windows     []struct {
			Scope     string  `json:"scope"`
			ResetsAt  *string `json:"resets_at"`
			Exhausted bool    `json:"exhausted"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &picked); err != nil {
		t.Fatal(err)
	}
	if picked.AuthID != auth.ID || picked.Provider != "claude" || picked.Lane != "lane-test" {
		t.Fatalf("picked = %+v", picked)
	}
	if picked.AccountHash == nil || *picked.AccountHash != *feed.AccountHash("owner@example.com") {
		t.Fatalf("account hash = %v", picked.AccountHash)
	}
	if picked.Reason != "earliest-reset" {
		t.Fatalf("reason = %s", picked.Reason)
	}
	if len(picked.Windows) != 1 || picked.Windows[0].Scope != "5h" || picked.Windows[0].Exhausted || picked.Windows[0].ResetsAt == nil || *picked.Windows[0].ResetsAt != reset.UTC().Format(time.RFC3339) {
		t.Fatalf("windows = %+v", picked.Windows)
	}
	if strings.Contains(rec.Body.String(), "example.com") {
		t.Fatal("pick leaked the account email")
	}
}

func TestRoutingPickRejectsBadInput(t *testing.T) {
	h := &Handler{authManager: coreauth.NewManager(nil, nil, nil)}
	if rec := pickRequest(t, h, `{"lane":"lane-test"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing model status = %d", rec.Code)
	}
	if rec := pickRequest(t, h, `{"model":"no-such-model-anywhere"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown model status = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := pickRequest(t, &Handler{}, `{"model":"x"}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no manager status = %d", rec.Code)
	}
}

func TestRoutingPickIsReadOnlyForRoundRobin(t *testing.T) {
	const model = "rr-pick-test-model"
	manager := coreauth.NewManager(nil, &coreauth.RoundRobinSelector{}, nil)
	manager.RegisterExecutor(&pickTestExecutor{provider: "claude"})
	for _, id := range []string{"rr-pick-a", "rr-pick-b"} {
		registry.GetGlobalRegistry().RegisterClient(id, "claude", []*registry.ModelInfo{{ID: model}})
		t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(id) })
		if _, err := manager.Register(context.Background(), &coreauth.Auth{ID: id, Provider: "claude", Status: coreauth.StatusActive}); err != nil {
			t.Fatal(err)
		}
	}
	h := &Handler{authManager: manager}
	var ids []string
	for i := 0; i < 2; i++ {
		rec := pickRequest(t, h, `{"model":"`+model+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("pick %d status = %d body=%s", i, rec.Code, rec.Body.String())
		}
		var picked struct {
			AuthID string `json:"auth_id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &picked); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, picked.AuthID)
	}
	if ids[0] != ids[1] {
		t.Fatalf("pick advanced the rotation: %v", ids)
	}
	served, err := manager.SelectAuth(context.Background(), "claude", model, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if served.ID != ids[0] {
		t.Fatalf("real traffic skipped %s after picks, served %s", ids[0], served.ID)
	}
}
