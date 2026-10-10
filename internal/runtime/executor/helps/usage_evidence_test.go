package helps

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	"github.com/tidwall/gjson"
)

func TestUsagePresenceSurvivesNormalization(t *testing.T) {
	for _, payload := range []string{`{}`, `{"service_tier":"priority"}`, `{"usage":null}`, `{"usage":{}}`} {
		d := usage.EnsureTokenBreakdownForProvider(ParseOpenAIUsage([]byte(payload)), "openai", "")
		if usage.HasTokenMeasurement(d) {
			t.Fatalf("metadata manufactured usage: %s: %+v", payload, d)
		}
	}
	for _, parse := range []func([]byte) usage.Detail{ParseOpenAIUsage, ParseClaudeUsage} {
		d := parse([]byte(`{"usage":{"input_tokens":0,"output_tokens":0}}`))
		if !usage.HasTokenMeasurement(d) || !d.TokenEvidence.Input || !d.TokenEvidence.Output || d.TokenEvidence.Invalid || !d.TokenBreakdown.Valid() {
			t.Fatalf("lost measured zero: %+v", d)
		}
	}
}

func TestUsageRejectsCoercedNativeCounters(t *testing.T) {
	for _, parse := range []func([]byte) usage.Detail{ParseOpenAIUsage, ParseClaudeUsage} {
		for _, invalid := range []string{`"12"`, `true`, `null`, `-1`, `1.5`, `9223372036854775808`, `1e100`} {
			d := parse([]byte(`{"usage":{"input_tokens":` + invalid + `,"output_tokens":0}}`))
			d = usage.EnsureTokenBreakdownForProvider(d, "openai", "")
			if !d.TokenEvidence.Invalid || d.TokenBreakdown.Quality != usage.TokenAccountingQualityInconsistent {
				t.Fatalf("coerced %s: %+v", invalid, d)
			}
		}
	}
	for _, field := range []string{`"input_tokens_details":{"cache_creation_tokens":null}`, `"output_tokens_details":{"reasoning_tokens":"3"}`} {
		d := ParseOpenAIUsage([]byte(`{"usage":{"input_tokens":10,"output_tokens":5,` + field + `}}`))
		if !d.TokenEvidence.Invalid {
			t.Fatalf("coerced optional field %s: %+v", field, d)
		}
	}
	d := ParseOpenAIUsage([]byte(`{"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":0}}`))
	if !d.TokenEvidence.Invalid {
		t.Fatalf("explicit contradictory total treated as absent: %+v", d)
	}
}

func TestUsageCountAcceptsExactIntegerNotation(t *testing.T) {
	for raw, want := range map[string]int64{"0": 0, "12.0": 12, "1.2e2": 120, "9007199254740993": 9007199254740993, "9223372036854775807": 9223372036854775807} {
		if got, ok := usageCount(gjson.Parse(raw)); !ok || got != want {
			t.Fatalf("%s = %d/%v, want %d", raw, got, ok, want)
		}
	}
}

func TestOpenAIStreamExplicitZeroReplacesEarlierUsageWithTier(t *testing.T) {
	var b StreamUsageBuffer
	b.ObserveOpenAIStream([]byte(`data: {"usage":{"prompt_tokens":10,"completion_tokens":5},"service_tier":"priority"}`))
	b.ObserveOpenAIStream([]byte(`data: {"usage":{"prompt_tokens":0,"completion_tokens":0},"service_tier":"default"}`))
	b.ObserveOpenAIStream([]byte(`data: {"service_tier":"priority"}`))
	d, ok := b.Detail()
	if !ok || !usage.HasTokenMeasurement(d) || d.InputTokens != 0 || d.OutputTokens != 0 || d.ResponseServiceTier != "default" {
		t.Fatalf("final zero lost: %+v", d)
	}
}

func TestClaudePartialStreamPreservesAbsentFieldsAndReplacesZero(t *testing.T) {
	var b StreamUsageBuffer
	b.ObserveClaudeStream([]byte(`data: {"message":{"usage":{"input_tokens":10,"cache_read_input_tokens":20,"cache_creation_input_tokens":3,"output_tokens":5,"thinking_tokens":2}}}`))
	b.ObserveClaudeStream([]byte(`data: {"usage":{"output_tokens":0,"cache_creation_input_tokens":0}}`))
	d, ok := b.Detail()
	if !ok || d.InputTokens != 10 || d.CacheReadTokens != 20 || d.CacheCreationTokens != 0 || d.OutputTokens != 0 || d.ReasoningTokens != 0 || d.TotalTokens != 30 || !d.TokenBreakdown.Valid() || d.TokenBreakdown.Quality != usage.TokenAccountingQualityComplete {
		t.Fatalf("partial zero update: %+v", d)
	}
	var partial StreamUsageBuffer
	partial.ObserveClaudeStream([]byte(`data: {"usage":{"input_tokens":0}}`))
	d, _ = partial.Detail()
	if !d.TokenEvidence.Input || d.TokenEvidence.Output {
		t.Fatalf("invented output presence: %+v", d)
	}
	partial.ObserveClaudeStream([]byte(`data: {"usage":{"output_tokens":0}}`))
	d, _ = partial.Detail()
	if !d.TokenEvidence.Input || !d.TokenEvidence.Output || !usage.HasTokenMeasurement(d) {
		t.Fatalf("lost zero frame presence: %+v", d)
	}
}

func TestPluginResponsesRetainsExplicitZeroUsage(t *testing.T) {
	payload := []byte(`{"response":{"usage":{"input_tokens":0,"output_tokens":0}},"service_tier":"priority"}`)
	d := ParsePluginExecutorResponseUsage("codex", payload)
	if !usage.HasTokenMeasurement(d) || !d.TokenEvidence.Input || !d.TokenEvidence.Output {
		t.Fatalf("plugin lost zero: %+v", d)
	}
	var b StreamUsageBuffer
	ObservePluginExecutorStreamUsage("codex", []byte(`data: `+string(payload)), &b)
	d, ok := b.Detail()
	if !ok || !usage.HasTokenMeasurement(d) {
		t.Fatalf("stream plugin lost zero: %+v", d)
	}
}
