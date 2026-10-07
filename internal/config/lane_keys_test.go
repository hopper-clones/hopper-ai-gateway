package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const laneKeysV8Fixture = `config-version: 8
server:
  port: 8317
access:
  api-keys:
    - "plain-key"
  lane-keys:
    - id: lk-live
      key: "lane-live"
      lane: lane-test
      project: "project:822b"
      task: "task:a929eb4f"
      expires-at: "2026-10-08T00:00:00Z"
    - id: lk-expired
      key: "lane-expired"
      lane: lane-b
      expires-at: "2026-10-01T00:00:00Z"
observability:
  usage-feed:
    enabled: true
    dir: feed-data
`

func TestLaneKeysAndUsageFeedLoadFromV8Layout(t *testing.T) {
	cfg, err := ParseConfigBytes([]byte(laneKeysV8Fixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.APIKeys) != 1 || len(cfg.LaneKeys) != 2 {
		t.Fatalf("keys = %v lane keys = %+v", cfg.APIKeys, cfg.LaneKeys)
	}
	live := cfg.LaneKeys[0]
	if live.ID != "lk-live" || live.Key != "lane-live" || live.Lane != "lane-test" || live.Project != "project:822b" || live.Task != "task:a929eb4f" || live.ExpiresAt != "2026-10-08T00:00:00Z" {
		t.Fatalf("live lane key = %+v", live)
	}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if live.Expired(now) {
		t.Fatal("live key reported expired")
	}
	if !cfg.LaneKeys[1].Expired(now) {
		t.Fatal("expired key reported live")
	}
	if (LaneKey{ExpiresAt: "garbage"}).Expired(now) != true {
		t.Fatal("unparseable expiry must count as expired")
	}
	if !cfg.UsageFeed.Enabled || cfg.UsageFeed.Dir != "feed-data" {
		t.Fatalf("usage feed = %+v", cfg.UsageFeed)
	}
	if errValidate := ValidateV8Config([]byte(laneKeysV8Fixture)); errValidate != nil {
		t.Fatalf("v8 validation rejected lane keys / usage feed: %v", errValidate)
	}
}

func TestSaveConfigPrunesExpiredLaneKeysAndKeepsV8Paths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(laneKeysV8Fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if pruned := cfg.PruneExpiredLaneKeys(now); pruned != 1 || len(cfg.LaneKeys) != 1 {
		t.Fatalf("pruned = %d lane keys = %+v", pruned, cfg.LaneKeys)
	}
	cfg.LaneKeys = append(cfg.LaneKeys, LaneKey{ID: "lk-new", Key: "lane-new", Lane: "lane-b", ExpiresAt: now.Add(time.Hour).Format(time.RFC3339)})
	if err := SaveConfigPreserveComments(path, cfg, true); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "lk-expired") {
		t.Fatalf("expired key survived save:\n%s", text)
	}
	if !strings.Contains(text, "lane-keys:") || !strings.Contains(text, "lk-new") || !strings.Contains(text, "usage-feed:") {
		t.Fatalf("saved layout lost lane keys or usage feed:\n%s", text)
	}
	if strings.Contains(text, "\nlane-keys:") || strings.Contains(text, "\nusage-feed:") {
		t.Fatalf("lane keys / usage feed must stay under their v8 sections:\n%s", text)
	}
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.LaneKeys) != 2 || reloaded.LaneKeys[1].ID != "lk-new" || !reloaded.UsageFeed.Enabled {
		t.Fatalf("reloaded = %+v feed = %+v", reloaded.LaneKeys, reloaded.UsageFeed)
	}
}

func TestUsageFeedResolveDir(t *testing.T) {
	configPath := filepath.Join("/", "etc", "gateway", "config.yaml")
	if got := (UsageFeedConfig{}).ResolveDir(configPath); got != filepath.Join("/", "etc", "gateway", "usage-feed") {
		t.Fatalf("default dir = %s", got)
	}
	if got := (UsageFeedConfig{Dir: "feed"}).ResolveDir(configPath); got != filepath.Join("/", "etc", "gateway", "feed") {
		t.Fatalf("relative dir = %s", got)
	}
	abs := filepath.Join("/", "var", "feed")
	if got := (UsageFeedConfig{Dir: abs}).ResolveDir(configPath); got != abs {
		t.Fatalf("absolute dir = %s", got)
	}
}
