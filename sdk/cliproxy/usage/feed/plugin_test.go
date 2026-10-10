package feed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func laneContext(t *testing.T, metadata map[string]string) context.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	if metadata != nil {
		c.Set("accessMetadata", metadata)
	}
	return context.WithValue(context.Background(), "gin", c)
}

func TestKeyIDAndAccountHash(t *testing.T) {
	sum := sha256.Sum256([]byte("secret-key"))
	if got := KeyID("secret-key"); got != hex.EncodeToString(sum[:])[:16] {
		t.Fatalf("key id = %s", got)
	}
	if KeyID("") != "" {
		t.Fatal("empty key must give empty id")
	}
	lower := sha256.Sum256([]byte("eric@example.com"))
	if got := AccountHash("  Eric@Example.com "); got == nil || *got != hex.EncodeToString(lower[:]) {
		t.Fatalf("account hash = %v", got)
	}
	if AccountHash("") != nil {
		t.Fatal("empty email must give nil hash")
	}
}

func TestPluginEmitsUsageAndQuotaEvents(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	store, err := Open(dir, WithClock(fixedClock(at)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	plugin := NewPlugin(store, func(authID string) (string, bool) {
		if authID == "auth-1" {
			return "Owner@Example.com", true
		}
		return "", false
	})
	plugin.now = fixedClock(at)

	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-utilization", "0.42")
	headers.Set("anthropic-ratelimit-unified-5h-reset", "1791817200") // 2026-10-12T15:00:00Z
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-7d-utilization", "1")
	headers.Set("anthropic-ratelimit-unified-7d-status", "rejected")

	// The gin context is recycled by dispatch time; identity comes from the record.
	ctx := laneContext(t, map[string]string{"source": "authorization", "lane": "someone-else"})
	plugin.HandleUsage(ctx, usage.Record{
		RequestID:       "req-1",
		Lane:            "lane-test",
		Project:         "project:822b",
		Task:            "task:a929eb4f",
		Provider:        "claude",
		Model:           "claude-fable-5-1",
		APIKey:          "lane-secret",
		AuthID:          "auth-1",
		ReasoningEffort: "high",
		RequestedAt:     at.Add(-1500 * time.Millisecond),
		Latency:         1500 * time.Millisecond,
		Detail:          helps.ParseClaudeUsage([]byte(`{"usage":{"input_tokens":100,"cache_read_input_tokens":40,"cache_creation_input_tokens":5,"output_tokens":20,"output_tokens_details":{"thinking_tokens":8}}}`)),
		ResponseHeaders: headers,
	})
	// A plain api-key request with no tokens and a failure.
	plugin.HandleUsage(laneContext(t, map[string]string{"source": "x-api-key"}), usage.Record{
		RequestID: "req-2",
		Provider:  "codex",
		Model:     "gpt-5",
		APIKey:    "plain-key",
		AuthID:    "auth-unknown",
		Failed:    true,
		Fail:      usage.Failure{StatusCode: 429},
	})
	store.Flush()

	page, err := store.Read("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 4 {
		t.Fatalf("events = %d (%s), want usage + 2 quota + usage", len(page.Events), page.Events)
	}
	var first UsageEvent
	if err := json.Unmarshal(page.Events[0], &first); err != nil {
		t.Fatal(err)
	}
	ownerHash := AccountHash("owner@example.com")
	if first.ID != "req-1" || first.Kind != KindUsage || first.KeyID != KeyID("lane-secret") || first.Lane != "lane-test" || first.Project != "project:822b" || first.Task != "task:a929eb4f" {
		t.Fatalf("usage identity = %+v", first)
	}
	if first.AccountID != "auth-1" || first.AccountHash == nil || *first.AccountHash != *ownerHash {
		t.Fatalf("usage account = %+v", first)
	}
	if first.Provider != "claude" || first.Model != "claude-fable-5-1" || first.Effort != "high" || first.Status != "ok" || first.LatencyMS != 1500 {
		t.Fatalf("usage request fields = %+v", first)
	}
	if *first.Tokens != (Tokens{Input: 145, CachedInput: 40, CacheWrite: 5, Output: 20, Reasoning: 8, Total: 165}) {
		t.Fatalf("usage tokens = %+v", first.Tokens)
	}
	if first.CacheHit == nil || !*first.CacheHit {
		t.Fatalf("cache_hit = %v, want true", first.CacheHit)
	}
	if time.Time(first.At) != at {
		t.Fatalf("at = %v, want completion time %v", time.Time(first.At), at)
	}

	var quota5h, quota7d QuotaEvent
	if err := json.Unmarshal(page.Events[1], &quota5h); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(page.Events[2], &quota7d); err != nil {
		t.Fatal(err)
	}
	if quota5h.ID != "req-1:quota:5h" || quota5h.Scope != "5h" || quota5h.Utilization != 0.42 || quota5h.Exhausted || quota5h.ResetsAt == nil || !time.Time(*quota5h.ResetsAt).Equal(time.Unix(1791817200, 0)) {
		t.Fatalf("5h quota = %+v", quota5h)
	}
	if quota5h.AccountID != "auth-1" || quota5h.AccountHash == nil || *quota5h.AccountHash != *ownerHash || quota5h.Provider != "claude" {
		t.Fatalf("5h quota account = %+v", quota5h)
	}
	if quota7d.ID != "req-1:quota:7d" || !quota7d.Exhausted || quota7d.Utilization != 1 || quota7d.ResetsAt != nil {
		t.Fatalf("7d quota = %+v", quota7d)
	}

	var second UsageEvent
	if err := json.Unmarshal(page.Events[3], &second); err != nil {
		t.Fatal(err)
	}
	if second.Lane != "" || second.Project != "" || second.Task != "" || second.KeyID != KeyID("plain-key") {
		t.Fatalf("plain key identity = %+v", second)
	}
	if second.AccountHash != nil || second.CacheHit != nil || second.Status != "error" || second.Tokens != nil || second.TokenStatus != TokensUnavailable {
		t.Fatalf("failed request = %+v", second)
	}
}

func TestPluginIgnoresNilStore(t *testing.T) {
	var plugin *Plugin
	plugin.HandleUsage(context.Background(), usage.Record{RequestID: "x"})
	NewPlugin(nil, nil).HandleUsage(context.Background(), usage.Record{RequestID: "x"})
}
