package management

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestConfigV8RedactsLaneKeySecretsAndPreservesThemOnWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := "config-version: 8\nserver:\n  port: 8317\naccess:\n  api-keys: [client]\n  lane-keys:\n    - id: lk-1\n      key: \"lk_secret_one\"\n      lane: lane-test\n      expires-at: \"2099-01-01T00:00:00Z\"\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{cfg: cfg, configFilePath: path}
	router := gin.New()
	router.GET("/v8/management/config", h.ConfigV8)
	router.GET("/v8/management/config/*path", h.ConfigV8)
	router.PUT("/v8/management/config/*path", h.ConfigV8)
	request := func(method, url, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, url, strings.NewReader(body)))
		return rec
	}
	for _, url := range []string{"/v8/management/config", "/v8/management/config/access", "/v8/management/config/access/lane-keys"} {
		rec := request(http.MethodGet, url, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d body=%s", url, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "lk_secret_one") {
			t.Fatalf("GET %s leaked the lane key: %s", url, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "lk-1") || !strings.Contains(rec.Body.String(), "lane-test") {
			t.Fatalf("GET %s lost lane key fields: %s", url, rec.Body.String())
		}
	}
	// Writing the redacted view back keeps the secret of the matching id.
	rec := request(http.MethodPut, "/v8/management/config/access/lane-keys", `[{"id":"lk-1","lane":"lane-renamed","expires-at":"2099-01-01T00:00:00Z"}]`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%s", rec.Code, rec.Body.String())
	}
	saved, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.LaneKeys) != 1 || saved.LaneKeys[0].Key != "lk_secret_one" || saved.LaneKeys[0].Lane != "lane-renamed" {
		t.Fatalf("saved lane keys = %+v", saved.LaneKeys)
	}
}
