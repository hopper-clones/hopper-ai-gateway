package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	configaccess "github.com/router-for-me/CLIProxyAPI/v8/internal/access/config_access"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v8/sdk/access"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage/feed"
)

func laneKeyRequest(t *testing.T, h *Handler, method, target, body string, params gin.Params) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	c.Request = httptest.NewRequest(method, target, reader)
	c.Params = params
	c.Set(ConfigV8ContextKey, true)
	switch {
	case method == http.MethodPost:
		h.CreateLaneKey(c)
	case method == http.MethodDelete:
		h.DeleteLaneKey(c)
	default:
		h.ListLaneKeys(c)
	}
	return rec
}

func TestLaneKeysLifecycle(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	previous := config.LaneKeyNow
	config.LaneKeyNow = func() time.Time { return now }
	t.Cleanup(func() { config.LaneKeyNow = previous })

	path := writeTestConfigFile(t)
	h := &Handler{cfg: &config.Config{}, configFilePath: path}
	h.cfg.LaneKeys = []config.LaneKey{{ID: "lk-old", Key: "old-secret", Lane: "lane-old", ExpiresAt: "2026-10-01T00:00:00Z"}}

	rec := laneKeyRequest(t, h, http.MethodPost, "/v8/management/lane-keys", `{"lane":"lane-test","project":"project:822b","task":"task:a929eb4f","ttl_seconds":3600}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID        string `json:"id"`
		Key       string `json:"key"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.ID, "lk-") || !strings.HasPrefix(created.Key, "lk_") || len(created.Key) < 40 {
		t.Fatalf("created = %+v", created)
	}
	if created.ExpiresAt != now.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("expires_at = %s", created.ExpiresAt)
	}

	saved, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.LaneKeys) != 1 || saved.LaneKeys[0].ID != created.ID || saved.LaneKeys[0].Key != created.Key || saved.LaneKeys[0].Lane != "lane-test" {
		t.Fatalf("persisted lane keys = %+v (expired key must be pruned on save)", saved.LaneKeys)
	}

	rec = laneKeyRequest(t, h, http.MethodGet, "/v8/management/lane-keys", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), created.Key) {
		t.Fatalf("list leaked the key: %s", rec.Body.String())
	}
	var listed struct {
		LaneKeys []struct {
			ID        string `json:"id"`
			KeyID     string `json:"key_id"`
			Lane      string `json:"lane"`
			Project   string `json:"project"`
			Task      string `json:"task"`
			ExpiresAt string `json:"expires_at"`
		} `json:"lane_keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.LaneKeys) != 1 || listed.LaneKeys[0].ID != created.ID || listed.LaneKeys[0].KeyID != feed.KeyID(created.Key) || listed.LaneKeys[0].Task != "task:a929eb4f" {
		t.Fatalf("listed = %+v", listed)
	}

	rec = laneKeyRequest(t, h, http.MethodDelete, "/v8/management/lane-keys/nope", "", gin.Params{{Key: "id", Value: "nope"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete unknown status = %d", rec.Code)
	}
	rec = laneKeyRequest(t, h, http.MethodDelete, "/v8/management/lane-keys/"+created.ID, "", gin.Params{{Key: "id", Value: created.ID}})
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}
	saved, err = config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.LaneKeys) != 0 {
		t.Fatalf("lane keys after delete = %+v", saved.LaneKeys)
	}
}

func TestCreateLaneKeyValidatesInput(t *testing.T) {
	h := &Handler{cfg: &config.Config{}, configFilePath: writeTestConfigFile(t)}
	for _, body := range []string{``, `{"project":"p"}`, `{"lane":"lane-test","ttl_seconds":-5}`, `{"lane":"lane-test","ttl_seconds":99999999}`} {
		rec := laneKeyRequest(t, h, http.MethodPost, "/v8/management/lane-keys", body, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q status = %d, want 400", body, rec.Code)
		}
	}
	if len(h.cfg.LaneKeys) != 0 {
		t.Fatalf("invalid requests must not add keys: %+v", h.cfg.LaneKeys)
	}
}

func TestCreateLaneKeyReloadsBeforeResponding(t *testing.T) {
	t.Cleanup(func() { configaccess.Register(nil) })
	path := writeTestConfigFile(t)
	h := &Handler{cfg: &config.Config{}, configFilePath: path}
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	reloadedBeforeResponse := false
	h.SetConfigReloadHook(func(_ context.Context, cfg *config.Config) {
		reloadedBeforeResponse = rec.Body.Len() == 0
		configaccess.Register(&cfg.SDKConfig)
	})
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v8/management/lane-keys", strings.NewReader(`{"lane":"lane-test","ttl_seconds":600}`))
	c.Set(ConfigV8ContextKey, true)
	h.CreateLaneKey(c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !reloadedBeforeResponse {
		t.Fatal("the config reload must complete before the key is returned")
	}
	var created struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer "+created.Key)
	manager := sdkaccess.NewManager()
	manager.SetProviders(sdkaccess.RegisteredProviders())
	result, authErr := manager.Authenticate(context.Background(), req)
	if authErr != nil || result == nil || result.Metadata["lane"] != "lane-test" {
		t.Fatalf("freshly issued key must authenticate immediately: result=%+v err=%v", result, authErr)
	}
}
