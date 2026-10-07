package feed

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func TestPluginNormalizesTokensFromBreakdown(t *testing.T) {
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	store, err := Open(t.TempDir(), WithClock(fixedClock(at)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	plugin := NewPlugin(store, nil)

	// Anthropic's input_tokens excludes cache reads and writes.
	claude := helps.ParseClaudeUsage([]byte(`{"usage":{"input_tokens":300,"cache_read_input_tokens":120000,"cache_creation_input_tokens":1500,"output_tokens":800}}`))
	plugin.HandleUsage(context.Background(), usage.Record{RequestID: "claude", Provider: "claude", Detail: claude})
	// OpenAI's prompt_tokens already includes cached_tokens.
	codex := helps.ParseOpenAIUsage([]byte(`{"usage":{"prompt_tokens":1000,"completion_tokens":50,"total_tokens":1050,"prompt_tokens_details":{"cached_tokens":600},"completion_tokens_details":{"reasoning_tokens":20}}}`))
	plugin.HandleUsage(context.Background(), usage.Record{RequestID: "codex", Provider: "codex", Detail: codex})
	// No tokens at all is still a request that happened.
	plugin.HandleUsage(context.Background(), usage.Record{RequestID: "empty", Provider: "claude", Failed: true})
	// A breakdown that cannot be normalized is dropped and counted.
	plugin.HandleUsage(context.Background(), usage.Record{RequestID: "broken", Provider: "claude", Detail: usage.Detail{TotalTokens: 10, TokenBreakdown: usage.NewUnclassifiedTokenBreakdown(10)}})
	store.Flush()

	page, err := store.Read("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 3 {
		t.Fatalf("events = %d (%s), want claude, codex, empty", len(page.Events), page.Events)
	}
	var events []UsageEvent
	for _, raw := range page.Events {
		var ev UsageEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatal(err)
		}
		events = append(events, ev)
	}
	if events[0].Tokens != (Tokens{Input: 121800, CachedInput: 120000, CacheWrite: 1500, Output: 800, Reasoning: 0, Total: 122600}) {
		t.Fatalf("claude tokens = %+v", events[0].Tokens)
	}
	if events[1].Tokens != (Tokens{Input: 1000, CachedInput: 600, CacheWrite: 0, Output: 50, Reasoning: 20, Total: 1050}) {
		t.Fatalf("codex tokens = %+v", events[1].Tokens)
	}
	if events[2].ID != "empty" || events[2].Tokens != (Tokens{}) || events[2].CacheHit != nil {
		t.Fatalf("empty event = %+v", events[2])
	}
	if plugin.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1 (the unnormalizable event)", plugin.Dropped())
	}
	for _, ev := range events {
		if ev.Tokens.CachedInput > ev.Tokens.Input || ev.Tokens.Total != ev.Tokens.Input+ev.Tokens.Output {
			t.Fatalf("invariant broken: %+v", ev.Tokens)
		}
	}
}
