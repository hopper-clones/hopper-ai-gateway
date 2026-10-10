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

// Dropped counts attempts refused by the bounded feed writer; invalid token
// measurements are retained as attempts and are never dropped for that reason.
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
	event.Tokens, event.TokenStatus, event.TokenFields = normalizeTokens(record)
	if event.Tokens != nil && event.TokenStatus == TokensComplete {
		event.CacheHit = cacheHit(*event.Tokens)
	}
	if !p.store.Write(event) {
		p.dropped.Add(1)
		log.WithFields(log.Fields{"request_id": record.RequestID, "provider": record.Provider, "dropped_total": p.dropped.Load()}).Warn("usage feed: attempt refused by feed writer")
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

// normalizeTokens retains request outcomes independently of token availability.
// Complete and partial measurements use the existing canonical non-overlapping
// buckets. Partial totals include explicitly unclassified tokens; consumers must
// not mistake them for fully attributed input/output or for exhaustive coverage.
func normalizeTokens(record usage.Record) (*Tokens, string, []string) {
	fields := []string{}
	if !usage.HasTokenMeasurement(record.Detail) {
		return nil, TokensUnavailable, fields
	}
	detail := usage.EnsureTokenBreakdownForProvider(record.Detail, record.Provider, record.ExecutorType)
	breakdown, evidence := detail.TokenBreakdown, detail.TokenEvidence
	if evidence.Invalid || !breakdown.Valid() || breakdown.Quality == usage.TokenAccountingQualityInconsistent {
		return nil, TokensInvalid, fields
	}
	status := TokensComplete
	if breakdown.Quality != usage.TokenAccountingQualityComplete || (evidence.Known && (!evidence.Input || !evidence.Output)) {
		status = TokensPartial
	}
	if evidence.Known {
		if evidence.Input {
			fields = append(fields, "input")
		}
		if evidence.Output {
			fields = append(fields, "output")
		}
		if evidence.Total {
			fields = append(fields, "total")
		}
	} else if status == TokensComplete {
		fields = append(fields, "input", "output", "total")
	}
	tokens := &Tokens{Input: breakdown.Input.TotalTokens, CachedInput: breakdown.Input.CacheReadTokens, CacheWrite: breakdown.Input.CacheWriteTokens, Output: breakdown.Output.TotalTokens, Reasoning: breakdown.Output.ReasoningTokens, Total: breakdown.TotalTokens, Unclassified: breakdown.UnclassifiedTokens}
	return tokens, status, fields
}

// cacheHit is unknown (nil) until the response reported input tokens.
func cacheHit(tokens Tokens) *bool {
	if tokens.Input <= 0 {
		return nil
	}
	hit := tokens.CachedInput > 0
	return &hit
}
