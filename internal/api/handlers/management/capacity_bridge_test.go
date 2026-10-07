package management

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestCapacityBridgeUsesActualLoopbackPeer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		peer string
		want int
	}{{"203.0.113.7:1234", 403}, {"127.0.0.1:1234", 503}, {"[::1]:1234", 503}, {"", 403}} {
		t.Run(tc.peer, func(t *testing.T) {
			t.Setenv("HOPPER_AI_CAPACITY_READER", "")
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/capacity/snapshot", nil)
			c.Request.RemoteAddr = tc.peer
			c.Request.Header.Set("X-Forwarded-For", "127.0.0.1")
			(&Handler{}).GetCapacitySnapshot(c)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestCapacityEnvIsScrubbed(t *testing.T) {
	env := capacityEnv([]string{
		"PATH=/usr/bin",
		"HOME=/Users/op",
		"HOPPER_AI_CAPACITY_READER=/r.mjs",
		"HOPPER_AI_CAPACITY_STATE=/s",
		"AWS_SECRET_ACCESS_KEY=nope",
		"MANAGEMENT_PASSWORD=nope",
		"HOPPER_CAPACITY_ACCOUNT_DISCOVERY=on",
	})
	want := []string{"PATH=/usr/bin", "HOME=/Users/op", "HOPPER_AI_CAPACITY_READER=/r.mjs", "HOPPER_AI_CAPACITY_STATE=/s", "HOPPER_CAPACITY_ACCOUNT_DISCOVERY=off"}
	if strings.Join(env, "\n") != strings.Join(want, "\n") {
		t.Fatalf("env = %q, want %q", env, want)
	}
}

func writeCapacityScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bun.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func capacitySnapshot(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/capacity/snapshot", nil)
	c.Request.RemoteAddr = "127.0.0.1:1234"
	(&Handler{}).GetCapacitySnapshot(c)
	return w
}

func TestCapacityBridgeSharesOneRunAcrossConcurrentRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	counter := filepath.Join(t.TempDir(), "runs")
	t.Setenv("HOPPER_AI_CAPACITY_BUN", writeCapacityScript(t, "echo run >> \"$HOPPER_AI_CAPACITY_COUNTER\"\nsleep 0.3\necho '{\"schemaVersion\":\"hopper.gateway-capacity.v1\",\"accounts\":[]}'\n"))
	t.Setenv("HOPPER_AI_CAPACITY_READER", filepath.Join(t.TempDir(), "reader.mjs"))
	t.Setenv("HOPPER_AI_CAPACITY_STATE", "/state")
	t.Setenv("HOPPER_AI_CAPACITY_PACKAGE", "/pkg")
	t.Setenv("HOPPER_AI_CAPACITY_COUNTER", counter)

	const requests = 4
	results := make(chan int, requests)
	for i := 0; i < requests; i++ {
		go func() { results <- capacitySnapshot(t).Code }()
	}
	for i := 0; i < requests; i++ {
		if code := <-results; code != 200 {
			t.Fatalf("status = %d, want 200", code)
		}
	}
	runs, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(runs), "run"); got != 1 {
		t.Fatalf("reader ran %d times for %d concurrent requests, want 1", got, requests)
	}
}

func TestCapacityBridgeTimesOut(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := capacityBridgeTimeout
	capacityBridgeTimeout = 100 * time.Millisecond
	t.Cleanup(func() { capacityBridgeTimeout = previous })
	t.Setenv("HOPPER_AI_CAPACITY_BUN", writeCapacityScript(t, "exec sleep 5\n"))
	t.Setenv("HOPPER_AI_CAPACITY_READER", filepath.Join(t.TempDir(), "reader.mjs"))
	t.Setenv("HOPPER_AI_CAPACITY_STATE", "/state")
	t.Setenv("HOPPER_AI_CAPACITY_PACKAGE", "/pkg")
	w := capacitySnapshot(t)
	if w.Code != 504 || !strings.Contains(w.Body.String(), "CAPACITY_TIMEOUT") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
