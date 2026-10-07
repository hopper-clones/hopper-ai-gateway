package feed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync/atomic"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

// AccountResolver returns the account email behind a gateway auth id when known.
type AccountResolver func(authID string) (email string, ok bool)

// Plugin turns usage records into feed events: one usage event per completed
// request and one quota event per quota window observed on its response.
type Plugin struct {
	store   *Store
	resolve AccountResolver
	now     func() time.Time
	dropped atomic.Uint64
}

// Dropped counts events discarded because their tokens could not be normalized.
func (p *Plugin) Dropped() uint64 {
	if p == nil {
		return 0
	}
	return p.dropped.Load()
}

// NewPlugin binds the feed store and the account resolver.
func NewPlugin(store *Store, resolve AccountResolver) *Plugin {
	return &Plugin{store: store, resolve: resolve, now: time.Now}
}

// KeyID is the first 16 hex characters of sha256(api key); empty for no key.
func KeyID(apiKey string) string {
	if apiKey == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:])[:16]
}

// AccountHash is sha256 of the lowercased, trimmed email; nil when there is none.
func AccountHash(email string) *string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(normalized))
	hash := hex.EncodeToString(sum[:])
	return &hash
}

// HandleUsage implements usage.Plugin.
func (p *Plugin) HandleUsage(_ context.Context, record usage.Record) {
	if p == nil || p.store == nil {
		return
	}
	at := p.completedAt(record)
	accountHash := p.accountHash(record.AuthID)
	event := NewUsageEvent(record.RequestID, at)
	event.KeyID = KeyID(record.APIKey)
	event.Lane, event.Project, event.Task = record.Lane, record.Project, record.Task
	event.AccountID = record.AuthID
	event.AccountHash = accountHash
	event.Provider = record.Provider
	event.Model = record.Model
	event.Effort = record.ReasoningEffort
	event.LatencyMS = record.Latency.Milliseconds()
	event.Status = "ok"
	if record.Failed {
		event.Status = "error"
	}
	if tokens, ok := normalizeTokens(record); ok {
		event.Tokens = tokens
		event.CacheHit = cacheHit(event.Tokens)
		p.store.Write(event)
	} else {
		p.dropped.Add(1)
		log.WithFields(log.Fields{"request_id": record.RequestID, "provider": record.Provider, "dropped_total": p.dropped.Load()}).
			Warn("usage feed: token breakdown cannot be normalized, usage event dropped")
	}
	// Quota windows do not depend on token accounting: observe them regardless.

	var quota coreauth.QuotaState
	if !quota.ObserveResponseHeadersForProvider(record.Provider, record.ResponseHeaders, at) {
		return
	}
	observed := &coreauth.Auth{Provider: record.Provider, Quota: quota}
	for _, window := range coreauth.RoutingQuotaWindows(observed, record.Model, at) {
		quotaEvent := NewQuotaEvent(record.RequestID, window.Scope, at)
		quotaEvent.AccountID = record.AuthID
		quotaEvent.AccountHash = accountHash
		quotaEvent.Provider = record.Provider
		if window.Utilization != nil {
			quotaEvent.Utilization = *window.Utilization
		}
		if !window.ResetAt.IsZero() {
			resetsAt := Time(window.ResetAt)
			quotaEvent.ResetsAt = &resetsAt
		}
		quotaEvent.Exhausted = window.Exhausted
		p.store.Write(quotaEvent)
	}
}

func (p *Plugin) completedAt(record usage.Record) time.Time {
	if !record.RequestedAt.IsZero() {
		return record.RequestedAt.Add(record.Latency)
	}
	return p.now()
}

func (p *Plugin) accountHash(authID string) *string {
	if p.resolve == nil || authID == "" {
		return nil
	}
	email, ok := p.resolve(authID)
	if !ok {
		return nil
	}
	return AccountHash(email)
}

// normalizeTokens builds the feed's token view from the accounting breakdown so
// every provider means the same thing: input includes cache reads and writes,
// output includes reasoning, total = input + output. Providers report these
// differently (Anthropic's input_tokens excludes cache reads; OpenAI's
// prompt_tokens includes them), which is why the raw Detail counters are never
// copied. A record with no tokens at all normalizes to zeros; a breakdown that
// cannot satisfy the invariants is refused.
func normalizeTokens(record usage.Record) (Tokens, bool) {
	if !detailHasTokens(record.Detail) {
		return Tokens{}, true
	}
	breakdown := usage.EnsureTokenBreakdownForProvider(record.Detail, record.Provider, record.ExecutorType).TokenBreakdown
	if !breakdown.Valid() || breakdown.UnclassifiedTokens != 0 {
		return Tokens{}, false
	}
	tokens := Tokens{
		Input:       breakdown.Input.TotalTokens,
		CachedInput: breakdown.Input.CacheReadTokens,
		CacheWrite:  breakdown.Input.CacheWriteTokens,
		Output:      breakdown.Output.TotalTokens,
		Reasoning:   breakdown.Output.ReasoningTokens,
		Total:       breakdown.Input.TotalTokens + breakdown.Output.TotalTokens,
	}
	if tokens.CachedInput > tokens.Input || tokens.Reasoning > tokens.Output || tokens.Total != tokens.Input+tokens.Output {
		return Tokens{}, false
	}
	return tokens, true
}

func detailHasTokens(detail usage.Detail) bool {
	return detail.InputTokens != 0 || detail.OutputTokens != 0 || detail.ReasoningTokens != 0 ||
		detail.CachedTokens != 0 || detail.CacheReadTokens != 0 || detail.CacheCreationTokens != 0 ||
		detail.TotalTokens != 0 || detail.TokenBreakdown.TotalTokens != 0
}

// cacheHit is unknown (nil) until the response reported input tokens.
func cacheHit(tokens Tokens) *bool {
	if tokens.Input <= 0 {
		return nil
	}
	hit := tokens.CachedInput > 0
	return &hit
}
