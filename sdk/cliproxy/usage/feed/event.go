// Package feed writes the append-only `hopper.gateway-usage.v1` usage feed:
// one JSON object per line in hourly files under a configured directory,
// read back by consumers through opaque "<file>:<byte offset>" cursors.
package feed

import (
	"encoding/json"
	"time"
)

// Version is the feed contract version carried in every event's "v" field.
const Version = 1

// Event kinds.
const (
	KindUsage = "usage"
	KindQuota = "quota"
)

// Time marshals as RFC 3339 UTC with millisecond precision ("2026-10-07T12:00:00.000Z").
type Time time.Time

// MarshalJSON renders the timestamp in the feed's fixed UTC millisecond form.
func (t Time) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Time(t).UTC().Format("2006-01-02T15:04:05.000Z"))
}

// UnmarshalJSON accepts any RFC 3339 timestamp.
func (t *Time) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return err
	}
	*t = Time(parsed)
	return nil
}

// Tokens is the token breakdown of one request. CachedInput is already inside
// Input and Reasoning is already inside Output; consumers must never add them again.
type Tokens struct {
	Input       int64 `json:"input"`
	CachedInput int64 `json:"cached_input"`
	CacheWrite  int64 `json:"cache_write"`
	Output      int64 `json:"output"`
	Reasoning   int64 `json:"reasoning"`
	Total       int64 `json:"total"`
}

// UsageEvent records one completed upstream request.
type UsageEvent struct {
	V           int     `json:"v"`
	ID          string  `json:"id"`
	At          Time    `json:"at"`
	Kind        string  `json:"kind"`
	KeyID       string  `json:"key_id"`
	Lane        string  `json:"lane"`
	Project     string  `json:"project"`
	Task        string  `json:"task"`
	AccountID   string  `json:"account_id"`
	AccountHash *string `json:"account_hash"`
	Provider    string  `json:"provider"`
	Model       string  `json:"model"`
	Effort      string  `json:"effort"`
	Tokens      Tokens  `json:"tokens"`
	LatencyMS   int64   `json:"latency_ms"`
	CacheHit    *bool   `json:"cache_hit"`
	Status      string  `json:"status"`
}

// NewUsageEvent returns a usage event with the contract version and kind set.
func NewUsageEvent(id string, at time.Time) *UsageEvent {
	return &UsageEvent{V: Version, ID: id, At: Time(at), Kind: KindUsage}
}

// QuotaEvent records one provider quota window observed on a response.
type QuotaEvent struct {
	V           int     `json:"v"`
	ID          string  `json:"id"`
	At          Time    `json:"at"`
	Kind        string  `json:"kind"`
	AccountID   string  `json:"account_id"`
	AccountHash *string `json:"account_hash"`
	Provider    string  `json:"provider"`
	Scope       string  `json:"scope"`
	Utilization float64 `json:"utilization"`
	ResetsAt    *Time   `json:"resets_at"`
	Exhausted   bool    `json:"exhausted"`
}

// NewQuotaEvent returns a quota event whose id is "<request id>:quota:<scope>".
func NewQuotaEvent(requestID, scope string, at time.Time) *QuotaEvent {
	return &QuotaEvent{V: Version, ID: requestID + ":quota:" + scope, At: Time(at), Kind: KindQuota, Scope: scope}
}
