package config

import (
	"path/filepath"
	"strings"
	"time"
)

// DefaultUsageFeedDir is the usage feed directory relative to the config file.
const DefaultUsageFeedDir = "usage-feed"

// LaneKeyNow is the clock lane keys are issued, listed, refused and pruned by.
// Tests replace it so expiry never depends on the wall clock.
var LaneKeyNow = time.Now

// LaneKey is a short-lived client credential that carries lane, project and task
// identity into the usage feed. It is accepted beside access.api-keys.
type LaneKey struct {
	// ID identifies the key in management responses; the key itself is never listed.
	ID string `yaml:"id" json:"id"`
	// Key is the plaintext presented by clients; the gateway must compare it at auth time.
	Key string `yaml:"key" json:"key"`
	// Lane, Project and Task are copied verbatim onto every usage event.
	Lane    string `yaml:"lane" json:"lane"`
	Project string `yaml:"project,omitempty" json:"project,omitempty"`
	Task    string `yaml:"task,omitempty" json:"task,omitempty"`
	// ExpiresAt is an RFC 3339 timestamp after which the key is refused and pruned.
	ExpiresAt string `yaml:"expires-at" json:"expires-at"`
}

// Expiry parses ExpiresAt; an unparseable value yields false.
func (k LaneKey) Expiry() (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(k.ExpiresAt))
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

// Expired reports whether the key must be refused at now. A missing or
// unparseable expiry counts as expired so a malformed key never authenticates.
func (k LaneKey) Expired(now time.Time) bool {
	expiry, ok := k.Expiry()
	return !ok || !now.Before(expiry)
}

// PruneExpiredLaneKeys removes expired lane keys and returns how many were dropped.
func (cfg *Config) PruneExpiredLaneKeys(now time.Time) int {
	if cfg == nil || len(cfg.LaneKeys) == 0 {
		return 0
	}
	kept := make([]LaneKey, 0, len(cfg.LaneKeys))
	for _, key := range cfg.LaneKeys {
		if !key.Expired(now) {
			kept = append(kept, key)
		}
	}
	pruned := len(cfg.LaneKeys) - len(kept)
	if len(kept) == 0 {
		kept = nil
	}
	cfg.LaneKeys = kept
	return pruned
}

// UsageFeedConfig enables the append-only usage feed (hopper.gateway-usage.v1).
type UsageFeedConfig struct {
	// Enabled starts the feed writer and serves the feed management endpoints.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Dir holds the hourly files; relative paths resolve against the config file directory.
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`
}

// ResolveDir returns the absolute feed directory for the given config file path.
func (u UsageFeedConfig) ResolveDir(configPath string) string {
	dir := strings.TrimSpace(u.Dir)
	if dir == "" {
		dir = DefaultUsageFeedDir
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(filepath.Dir(configPath), dir)
}
