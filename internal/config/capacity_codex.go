package config

import "time"

// DefaultCapacityCodexRefreshSeconds is how often the Capacity Codex account list is re-read.
const DefaultCapacityCodexRefreshSeconds = 300

// CapacityCodexConfig registers the Codex logins AI Capacity tracks as gateway
// credentials. Tokens stay in each login's own auth.json; nothing is copied.
type CapacityCodexConfig struct {
	// Enabled reads the Capacity account list on start and every RefreshSeconds.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// RefreshSeconds is the account list interval; non-positive uses the default.
	RefreshSeconds int `yaml:"refresh-seconds,omitempty" json:"refresh-seconds,omitempty"`
}

// RefreshInterval returns the account list interval.
func (c CapacityCodexConfig) RefreshInterval() time.Duration {
	if c.RefreshSeconds <= 0 {
		return DefaultCapacityCodexRefreshSeconds * time.Second
	}
	return time.Duration(c.RefreshSeconds) * time.Second
}
