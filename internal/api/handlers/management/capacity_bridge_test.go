package management

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
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

func fakeCapacityExec(t *testing.T, run func(ctx context.Context) ([]byte, []byte, error)) {
	t.Helper()
	previous := capacityExec
	capacityExec = func(ctx context.Context, _, _ string) ([]byte, []byte, error) { return run(ctx) }
	t.Cleanup(func() { capacityExec = previous })
}

func configureCapacityEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOPPER_AI_CAPACITY_BUN", filepath.Join(t.TempDir(), "bun"))
	t.Setenv("HOPPER_AI_CAPACITY_READER", filepath.Join(t.TempDir(), "reader.mjs"))
	t.Setenv("HOPPER_AI_CAPACITY_STATE", "/state")
	t.Setenv("HOPPER_AI_CAPACITY_PACKAGE", "/pkg")
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
	configureCapacityEnv(t)
	release := make(chan struct{})
	var runs atomic.Int32
	fakeCapacityExec(t, func(context.Context) ([]byte, []byte, error) {
		runs.Add(1)
		<-release
		return []byte(`{"schemaVersion":"hopper.gateway-capacity.v1","accounts":[]}`), nil, nil
	})
	const requests = 4
	joined := make(chan struct{}, requests)
	capacityFlightJoined = func() { joined <- struct{}{} }
	t.Cleanup(func() { capacityFlightJoined = nil })

	results := make(chan int, requests)
	for i := 0; i < requests; i++ {
		go func() { results <- capacitySnapshot(t).Code }()
	}
	// Every request has registered with the single in-flight run before it may finish.
	for i := 0; i < requests; i++ {
		<-joined
	}
	close(release)
	for i := 0; i < requests; i++ {
		if code := <-results; code != 200 {
			t.Fatalf("status = %d, want 200", code)
		}
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("reader ran %d times for %d concurrent requests, want 1", got, requests)
	}
}

func TestCapacityBridgeTimesOut(t *testing.T) {
	gin.SetMode(gin.TestMode)
	configureCapacityEnv(t)
	previous := capacityBridgeTimeout
	capacityBridgeTimeout = time.Millisecond
	t.Cleanup(func() { capacityBridgeTimeout = previous })
	fakeCapacityExec(t, func(ctx context.Context) ([]byte, []byte, error) {
		<-ctx.Done() // a reader that never finishes on its own
		return nil, nil, ctx.Err()
	})
	w := capacitySnapshot(t)
	if w.Code != 504 || !strings.Contains(w.Body.String(), "CAPACITY_TIMEOUT") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCapacityBridgeRunsRealReader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	configureCapacityEnv(t)
	script := filepath.Join(t.TempDir(), "bun.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho '{\"schemaVersion\":\"hopper.gateway-capacity.v1\",\"accounts\":[]}'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOPPER_AI_CAPACITY_BUN", script)
	w := capacitySnapshot(t)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "hopper.gateway-capacity.v1") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestConfigureCapacityProcessIsolatesTheProcessGroup(t *testing.T) {
	cmd := exec.Command("true")
	configureCapacityProcess(cmd)
	if cmd.Cancel == nil {
		t.Fatal("Cancel must kill the whole process group on timeout")
	}
	assertCapacityProcessGroup(t, cmd)
}
