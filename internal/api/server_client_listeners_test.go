package api

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	proxyconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func TestClientAPIPathServesOnlyTheClientAPI(t *testing.T) {
	allowed := []string{"/healthz", "/v1/models", "/v1/responses", "/v1beta/models", "/openai/v1/chat/completions", "/backend-api/codex/responses", "/v1/models/"}
	for _, p := range allowed {
		if !clientAPIPath(p) {
			t.Errorf("clientAPIPath(%q) = false, want true", p)
		}
	}
	refused := []string{"", "/", "/v1", "/management.html", "/v0/management/config", "/v8/management/routing/lanes", "/keep-alive", "/codex/callback",
		"/v1/../v0/management/config", "/v1//models", "/v1/./models", "/v1\\..\\v0/management", "/healthz/", "/v1beta"}
	for _, p := range refused {
		if clientAPIPath(p) {
			t.Errorf("clientAPIPath(%q) = true, want false", p)
		}
	}
}

func clientTestServer(t *testing.T) *Server {
	t.Helper()
	return newTestServerWithConfig(t, &proxyconfig.Config{
		SDKConfig:        sdkconfig.SDKConfig{APIKeys: []string{"test-key"}},
		RemoteManagement: proxyconfig.RemoteManagement{SecretKey: "test-management-secret"},
	})
}

func TestClientOnlyHandlerRefusesManagementFromAPeer(t *testing.T) {
	server := clientTestServer(t)
	handler := clientOnlyHandler(server.server.Handler)
	cases := []struct {
		path string
		auth string
		want int
	}{
		{"/healthz", "", http.StatusOK},
		{"/v1/models", "Bearer test-key", http.StatusOK},
		{"/v1/models", "", http.StatusUnauthorized},
		{"/v0/management/config", "Bearer test-management-secret", http.StatusNotFound},
		{"/v8/management/routing/lanes", "Bearer test-management-secret", http.StatusNotFound},
		{"/management.html", "", http.StatusNotFound},
		{"/", "", http.StatusNotFound},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		req.RemoteAddr = "100.64.0.2:40000"
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("GET %s = %d, want %d", c.path, rec.Code, c.want)
		}
	}
	// Even unfiltered, the engine refuses management to a remote peer while allow-remote is off.
	req := httptest.NewRequest(http.MethodGet, "/v0/management/config", nil)
	req.RemoteAddr = "100.64.0.2:40000"
	req.Header.Set("Authorization", "Bearer test-management-secret")
	rec := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote management through the engine = %d, want 403", rec.Code)
	}
}

func TestClientListenersServeAndStop(t *testing.T) {
	server := clientTestServer(t)
	probe, errListen := net.Listen("tcp", "127.0.0.1:0")
	if errListen != nil {
		t.Fatalf("listen: %v", errListen)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	server.cfg.ClientHosts = []string{"127.0.0.1"}
	server.startClientListeners(port, nil)
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	client := &http.Client{Timeout: 2 * time.Second}
	var health *http.Response
	for i := 0; i < 50; i++ {
		resp, errGet := client.Get(base + "/healthz")
		if errGet == nil {
			health = resp
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if health == nil {
		t.Fatal("client listener did not answer /healthz")
	}
	_ = health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("/healthz = %d, want 200", health.StatusCode)
	}
	mgmt, errMgmt := client.Get(base + "/v0/management/config")
	if errMgmt != nil {
		t.Fatalf("management request: %v", errMgmt)
	}
	_ = mgmt.Body.Close()
	if mgmt.StatusCode != http.StatusNotFound {
		t.Fatalf("management on a client listener = %d, want 404", mgmt.StatusCode)
	}
	server.stopClientListeners()
	if _, errAfter := client.Get(base + "/healthz"); errAfter == nil {
		t.Fatal("client listener still answers after stop")
	}
}
