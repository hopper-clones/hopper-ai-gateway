package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const capacityCodexV8Fixture = `config-version: 8
server:
  port: 8317
credentials:
  capacity-codex:
    enabled: true
    refresh-seconds: 120
`

func TestCapacityCodexLoadsAndSavesUnderCredentials(t *testing.T) {
	cfg, err := ParseConfigBytes([]byte(capacityCodexV8Fixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cfg.CapacityCodex.Enabled || cfg.CapacityCodex.RefreshInterval() != 2*time.Minute {
		t.Fatalf("capacity-codex = %+v", cfg.CapacityCodex)
	}
	if errValidate := ValidateV8Config([]byte(capacityCodexV8Fixture)); errValidate != nil {
		t.Fatalf("v8 validation rejected capacity-codex: %v", errValidate)
	}
	if got := (CapacityCodexConfig{}).RefreshInterval(); got != 300*time.Second {
		t.Fatalf("default refresh = %s", got)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err = os.WriteFile(path, []byte(capacityCodexV8Fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = SaveConfigPreserveComments(path, loaded, true); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "\ncapacity-codex:") || !strings.Contains(string(raw), "capacity-codex:") {
		t.Fatalf("capacity-codex must stay under credentials:\n%s", raw)
	}
	reloaded, err := LoadConfig(path)
	if err != nil || !reloaded.CapacityCodex.Enabled || reloaded.CapacityCodex.RefreshSeconds != 120 {
		t.Fatalf("reloaded = %+v err=%v", reloaded.CapacityCodex, err)
	}
}
