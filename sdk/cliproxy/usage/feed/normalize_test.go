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
	// Unclassified usage retains the attempt and its authoritative total.
	plugin.HandleUsage(context.Background(), usage.Record{RequestID: "broken", Provider: "claude", Detail: usage.Detail{TotalTokens: 10, TokenBreakdown: usage.NewUnclassifiedTokenBreakdown(10)}})
	store.Flush()

	page, err := store.Read("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 4 {
		t.Fatalf("events = %d (%s), want claude, codex, empty, broken", len(page.Events), page.Events)
	}
	var events []UsageEvent
	for _, raw := range page.Events {
		var ev UsageEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatal(err)
		}
		events = append(events, ev)
	}
	if *events[0].Tokens != (Tokens{Input: 121800, CachedInput: 120000, CacheWrite: 1500, Output: 800, Reasoning: 0, Total: 122600}) {
		t.Fatalf("claude tokens = %+v", events[0].Tokens)
	}
	if *events[1].Tokens != (Tokens{Input: 1000, CachedInput: 600, CacheWrite: 0, Output: 50, Reasoning: 20, Total: 1050}) {
		t.Fatalf("codex tokens = %+v", events[1].Tokens)
	}
	if events[2].ID != "empty" || events[2].Tokens != nil || events[2].TokenStatus != TokensUnavailable || events[2].CacheHit != nil {
		t.Fatalf("empty event = %+v", events[2])
	}
	if plugin.Dropped() != 0 {
		t.Fatalf("dropped = %d, want 0 (all attempts retained)", plugin.Dropped())
	}
	if events[3].TokenStatus != TokensPartial || events[3].Tokens.Unclassified != 10 {
		t.Fatalf("lost partial total: %+v", events[3])
	}
	for _, ev := range events {
		if ev.Tokens == nil {
			continue
		}
		if ev.Tokens.CachedInput > ev.Tokens.Input || ev.Tokens.Total != ev.Tokens.Input+ev.Tokens.Output+ev.Tokens.Unclassified {
			t.Fatalf("invariant broken: %+v", ev.Tokens)
		}
	}
}

func TestPluginPersistsAttemptMeasurementAvailability(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	store, err := Open(dir, WithClock(fixedClock(at)))
	if err != nil {
		t.Fatal(err)
	}
	plugin := NewPlugin(store, nil)
	fixtures := []struct {
		id, payload, status string
		failed              bool
		total               int64
	}{
		{"zero", `{"usage":{"input_tokens":0,"output_tokens":0}}`, TokensComplete, false, 0},
		{"failed-zero", `{"usage":{"input_tokens":0,"output_tokens":0}}`, TokensComplete, true, 0},
		{"no-usage", `{}`, TokensUnavailable, false, 0},
		{"failed-no-usage", `{}`, TokensUnavailable, true, 0},
		{"tier-only", `{"service_tier":"priority"}`, TokensUnavailable, false, 0},
		{"partial", `{"usage":{"input_tokens":12}}`, TokensPartial, true, 12},
		{"cache-only", `{"usage":{"input_tokens_details":{"cached_tokens":8}}}`, TokensPartial, false, 8},
		{"total-only", `{"usage":{"total_tokens":22}}`, TokensPartial, false, 22},
		{"invalid", `{"usage":{"input_tokens":"12","output_tokens":0}}`, TokensInvalid, true, 0},
		{"inconsistent", `{"usage":{"input_tokens":2,"output_tokens":0,"input_tokens_details":{"cached_tokens":4}}}`, TokensInvalid, false, 0},
	}
	for _, f := range fixtures {
		d := usage.EnsureTokenBreakdownForProvider(helps.ParseOpenAIUsage([]byte(f.payload)), "codex", "")
		plugin.HandleUsage(context.Background(), usage.Record{RequestID: f.id, Provider: "codex", Detail: d, Failed: f.failed})
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, WithClock(fixedClock(at)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	page, err := reopened.Read("", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != len(fixtures) {
		t.Fatalf("retained %d of %d attempts", len(page.Events), len(fixtures))
	}
	for i, raw := range page.Events {
		f := fixtures[i]
		var event UsageEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		if event.V != UsageVersion || event.ID != f.id || event.TokenStatus != f.status || (event.Status == "error") != f.failed {
			t.Fatalf("attempt mismatch: %+v", event)
		}
		if f.status == TokensUnavailable || f.status == TokensInvalid {
			if event.Tokens != nil {
				t.Fatalf("invented token measurement: %+v", event)
			}
		} else if event.Tokens == nil || event.Tokens.Total != f.total {
			t.Fatalf("lost measurement: %+v", event)
		}
	}
	if plugin.Dropped() != 0 {
		t.Fatalf("dropped attempts: %d", plugin.Dropped())
	}
}
