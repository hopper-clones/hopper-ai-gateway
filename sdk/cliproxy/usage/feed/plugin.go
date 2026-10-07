package feed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

// AccountResolver returns the account email behind a gateway auth id when known.
type AccountResolver func(authID string) (email string, ok bool)

// Plugin turns usage records into feed events: one usage event per completed
// request and one quota event per quota window observed on its response.
type Plugin struct {
	store   *Store
	resolve AccountResolver
	now     func() time.Time
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
func (p *Plugin) HandleUsage(ctx context.Context, record usage.Record) {
	if p == nil || p.store == nil {
		return
	}
	at := p.completedAt(record)
	accountHash := p.accountHash(record.AuthID)
	event := NewUsageEvent(record.RequestID, at)
	event.KeyID = KeyID(record.APIKey)
	event.Lane, event.Project, event.Task = laneIdentity(ctx)
	event.AccountID = record.AuthID
	event.AccountHash = accountHash
	event.Provider = record.Provider
	event.Model = record.Model
	event.Effort = record.ReasoningEffort
	event.Tokens = tokensOf(record.Detail)
	event.LatencyMS = record.Latency.Milliseconds()
	event.CacheHit = cacheHit(event.Tokens)
	event.Status = "ok"
	if record.Failed {
		event.Status = "error"
	}
	p.store.Write(event)

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

// laneIdentity reads the lane key metadata the access provider stored on the gin context.
func laneIdentity(ctx context.Context) (lane, project, task string) {
	if ctx == nil {
		return "", "", ""
	}
	ginCtx, ok := ctx.Value("gin").(*gin.Context)
	if !ok || ginCtx == nil {
		return "", "", ""
	}
	raw, exists := ginCtx.Get("accessMetadata")
	if !exists {
		return "", "", ""
	}
	metadata, ok := raw.(map[string]string)
	if !ok {
		return "", "", ""
	}
	return metadata["lane"], metadata["project"], metadata["task"]
}

func tokensOf(detail usage.Detail) Tokens {
	cached := detail.CachedTokens
	if cached == 0 {
		cached = detail.CacheReadTokens
	}
	total := detail.TotalTokens
	if total == 0 {
		total = detail.InputTokens + detail.OutputTokens
	}
	return Tokens{
		Input:       detail.InputTokens,
		CachedInput: cached,
		CacheWrite:  detail.CacheCreationTokens,
		Output:      detail.OutputTokens,
		Reasoning:   detail.ReasoningTokens,
		Total:       total,
	}
}

// cacheHit is unknown (nil) until the response reported input tokens.
func cacheHit(tokens Tokens) *bool {
	if tokens.Input <= 0 {
		return nil
	}
	hit := tokens.CachedInput > 0
	return &hit
}
