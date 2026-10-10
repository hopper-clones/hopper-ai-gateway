package helps

import (
	"math/big"
	"strconv"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	"github.com/tidwall/gjson"
)

// usageCount refuses string/bool/null coercion and fractional, negative or
// overflowing numbers. Decimal/exponent notation is accepted only when exact.
func usageCount(node gjson.Result) (int64, bool) {
	if node.Raw == "" {
		return 0, true
	}
	if node.Type != gjson.Number {
		return 0, false
	}
	if n, err := strconv.ParseInt(node.Raw, 10, 64); err == nil {
		return n, n >= 0
	}
	n, ok := new(big.Rat).SetString(node.Raw)
	if !ok || !n.IsInt() || !n.Num().IsInt64() || n.Sign() < 0 {
		return 0, false
	}
	return n.Num().Int64(), true
}

func usageCountValue(node gjson.Result) int64 {
	n, _ := usageCount(node)
	return n
}

func firstReportedUsageNode(node gjson.Result, paths ...string) gjson.Result {
	for _, path := range paths {
		if field := node.Get(path); field.Raw != "" {
			return field
		}
	}
	return gjson.Result{}
}

func usageEvidence(node gjson.Result, input, output gjson.Result, cacheRead, cacheWrite, reasoning gjson.Result) usage.TokenEvidence {
	total := node.Get("total_tokens")
	e := usage.TokenEvidence{Known: true, Input: input.Raw != "", Output: output.Raw != "", CacheRead: cacheRead.Raw != "", CacheWrite: cacheWrite.Raw != "", Reasoning: reasoning.Raw != "", Total: total.Raw != "", Invalid: !node.IsObject()}
	for _, field := range []gjson.Result{input, output, cacheRead, cacheWrite, reasoning, total} {
		if _, valid := usageCount(field); !valid {
			e.Invalid = true
		}
	}
	return e
}

// mergeReportedUsage is used for Claude's partial usage frames. Presence, not
// a nonzero value, decides whether a newer counter replaces its predecessor.
func mergeReportedUsage(existing, update usage.Detail) usage.Detail {
	merged := update
	old, next := existing.TokenEvidence, update.TokenEvidence
	if !next.Input {
		merged.InputTokens = existing.InputTokens
	}
	if !next.Output {
		merged.OutputTokens = existing.OutputTokens
	}
	if !next.CacheRead {
		merged.CacheReadTokens = existing.CacheReadTokens
	}
	if !next.CacheWrite {
		merged.CacheCreationTokens = existing.CacheCreationTokens
	}
	if !next.Reasoning {
		merged.ReasoningTokens = existing.ReasoningTokens
	}
	if merged.ResponseServiceTier == "" {
		merged.ResponseServiceTier = existing.ResponseServiceTier
	}
	merged.TokenEvidence = usage.TokenEvidence{Known: true, Input: old.Input || next.Input, Output: old.Output || next.Output, CacheRead: old.CacheRead || next.CacheRead, CacheWrite: old.CacheWrite || next.CacheWrite, Reasoning: old.Reasoning || next.Reasoning, Total: next.Total, Invalid: old.Invalid || next.Invalid}
	merged.CachedTokens = merged.CacheReadTokens
	if merged.CachedTokens == 0 {
		merged.CachedTokens = merged.CacheCreationTokens
	}
	// An explicit zero output also proves that its reasoning subset is zero.
	if next.Output && merged.OutputTokens == 0 && !next.Reasoning {
		merged.ReasoningTokens = 0
	}
	total, valid := safeUsageTokenSum(merged.InputTokens, merged.OutputTokens, merged.CacheReadTokens, merged.CacheCreationTokens)
	if !next.Total {
		merged.TotalTokens = total
	}
	merged.TokenBreakdown = usage.NewIndependentTokenBreakdown(merged.InputTokens, merged.CacheReadTokens, merged.CacheCreationTokens, merged.OutputTokens-merged.ReasoningTokens, merged.ReasoningTokens, merged.TotalTokens)
	if !valid || merged.TokenEvidence.Invalid || (next.Total && merged.TotalTokens != total) {
		merged.TokenEvidence.Invalid = true
		merged.TokenBreakdown = invalidUsageTokenBreakdown(merged.TotalTokens)
	}
	return merged
}
