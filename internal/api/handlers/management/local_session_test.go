package management

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func localTestHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	h := &Handler{cfg: &config.Config{}, failedAttempts: map[string]*attemptInfo{}}
	h.cfg.RemoteManagement.SecretKey = "synthetic-key-hash"
	h.localBrowser.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	launch, err := h.CreateLocalConsoleURL("http://127.0.0.1:8317")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(launch)
	_, query, _ := strings.Cut(u.Fragment, "?")
	params, _ := url.ParseQuery(query)
	return h, params.Get("local-session")
}

func localRequest(t *testing.T, h *Handler, method, capability string, cookie *http.Cookie, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	body := ""
	if method == http.MethodPost {
		body = `{"capability":"` + capability + `"}`
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:8317/v8/management/local-session", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:50123"
	r.Header.Set(localSessionHeader, "1")
	r.Header.Set("Origin", "http://127.0.0.1:8317")
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if mutate != nil {
		mutate(r)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = r
	h.LocalSession(c)
	c.Writer.WriteHeaderNow()
	return w
}

func TestLocalConsoleSessionLifecycle(t *testing.T) {
	h, capability := localTestHandler(t)
	w := localRequest(t, h, "POST", capability, nil, nil)
	if w.Code != 200 {
		t.Fatalf("exchange status %d", w.Code)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("expected one session cookie")
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/v8/management" || cookie.MaxAge != 43200 {
		t.Fatal("insecure session cookie settings")
	}
	if strings.Contains(w.Body.String(), cookie.Value) || strings.Contains(w.Body.String(), capability) || cookie.Value == capability {
		t.Fatal("credential leaked or reused")
	}
	if localRequest(t, h, "POST", capability, nil, nil).Code != 401 {
		t.Fatal("capability replay accepted")
	}
	if localRequest(t, h, "GET", "", cookie, nil).Code != 200 {
		t.Fatal("session restoration failed")
	}
	if localRequest(t, h, "DELETE", "", cookie, nil).Code != 204 {
		t.Fatal("logout failed")
	}
	if localRequest(t, h, "GET", "", cookie, nil).Code != 401 {
		t.Fatal("logged out cookie accepted")
	}
}

func TestLocalConsoleRejectsWrongRequestBoundary(t *testing.T) {
	cases := map[string]func(*http.Request){
		"origin":               func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") },
		"missing origin":       func(r *http.Request) { r.Header.Del("Origin") },
		"host":                 func(r *http.Request) { r.Host = "evil.example:8317" },
		"port":                 func(r *http.Request) { r.Host = "127.0.0.1:9999" },
		"peer":                 func(r *http.Request) { r.RemoteAddr = "192.0.2.1:50123" },
		"spoof forwarded peer": func(r *http.Request) { r.RemoteAddr = "192.0.2.1:50123"; r.Header.Set("X-Forwarded-For", "127.0.0.1") },
		"missing header":       func(r *http.Request) { r.Header.Del(localSessionHeader) },
		"fetch site":           func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h, capability := localTestHandler(t)
			if got := localRequest(t, h, "POST", capability, nil, mutate).Code; got != 403 {
				t.Fatalf("status %d", got)
			}
			w := localRequest(t, h, "POST", capability, nil, nil)
			if w.Code != 200 {
				t.Fatal("rejected request consumed capability")
			}
			cookie := w.Result().Cookies()[0]
			if name != "missing origin" && localRequest(t, h, "GET", "", cookie, mutate).Code != 401 {
				t.Fatal("cookie escaped request boundary")
			}
		})
	}
}

func TestLocalConsoleExpiryAndPolicyRevocation(t *testing.T) {
	h, capability := localTestHandler(t)
	initial := h.localBrowser.timeNow()
	h.localBrowser.now = func() time.Time { return initial.Add(5 * time.Minute) }
	if localRequest(t, h, "POST", capability, nil, nil).Code != 401 {
		t.Fatal("expired capability accepted")
	}
	h, capability = localTestHandler(t)
	w := localRequest(t, h, "POST", capability, nil, nil)
	cookie := w.Result().Cookies()[0]
	initial = h.localBrowser.timeNow()
	h.localBrowser.now = func() time.Time { return initial.Add(localSessionLifetime) }
	if localRequest(t, h, "GET", "", cookie, nil).Code != 401 {
		t.Fatal("expired session accepted")
	}
	h, capability = localTestHandler(t)
	w = localRequest(t, h, "POST", capability, nil, nil)
	cookie = w.Result().Cookies()[0]
	h.SetConfig(h.cfg.CloneForRuntime())
	if localRequest(t, h, "GET", "", cookie, nil).Code != 200 {
		t.Fatal("ordinary reload invalidated session")
	}
	// In-place changes must revoke too; the old pointer cannot detect these.
	h.cfg.RemoteManagement.SecretKey = "changed-synthetic-key-hash"
	h.SetConfig(h.cfg)
	if localRequest(t, h, "GET", "", cookie, nil).Code != 401 {
		t.Fatal("key change retained session")
	}
	fresh := &Handler{}
	if localRequest(t, fresh, "GET", "", cookie, nil).Code != 401 {
		t.Fatal("session survived process recreation")
	}
}

func TestLocalSessionDoesNotReplaceRemoteKeyPolicy(t *testing.T) {
	h, capability := localTestHandler(t)
	w := localRequest(t, h, "POST", capability, nil, nil)
	cookie := w.Result().Cookies()[0]
	for _, tc := range []struct {
		name, path, peer string
		header           bool
		want             int
	}{
		{"local v8", "/v8/management/config", "127.0.0.1:50123", true, 200},
		{"legacy excluded", "/v0/management/config", "127.0.0.1:50123", true, 401},
		{"remote excluded", "/v8/management/config", "192.0.2.1:50123", true, 403},
		{"csrf get excluded", "/v8/management/config", "127.0.0.1:50123", false, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://127.0.0.1:8317"+tc.path, nil)
			r.RemoteAddr = tc.peer
			r.AddCookie(cookie)
			if tc.header {
				r.Header.Set(localSessionHeader, "1")
			}
			rec := httptest.NewRecorder()
			engine := gin.New()
			engine.Use(h.Middleware())
			engine.GET(tc.path, func(c *gin.Context) { c.Status(200) })
			engine.ServeHTTP(rec, r)
			if rec.Code != tc.want {
				t.Fatalf("status %d want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestLocalConsoleRequiresLiteralLoopbackOrigin(t *testing.T) {
	for _, origin := range []string{"http://localhost:8317", "http://0.0.0.0:8317", "http://example.com:8317", "http://127.0.0.1", "http://127.0.0.1:8317/path"} {
		h := &Handler{}
		if _, err := h.CreateLocalConsoleURL(origin); err == nil {
			t.Errorf("accepted origin %s", origin)
		}
	}
}
