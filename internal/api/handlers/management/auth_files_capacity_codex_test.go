package management

import (
	"encoding/json"
	"strings"
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

type fakeCapacityStatus struct{}

func (fakeCapacityStatus) CapacityStatus() (string, string) {
	return "unavailable", "CAPACITY_CODEX_REFRESH_FAILED"
}

func TestAuthFileEntryShowsCapacityCodexSourceWithoutSecrets(t *testing.T) {
	auth := &coreauth.Auth{
		ID:       "capacity-codex-hash1",
		Provider: "codex",
		Label:    "codex 3",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"runtime_only":        "true",
			"auth_kind":           "oauth",
			"credential_source":   "capacity-codex",
			"capacity_account_id": "hash1",
			"capacity_label":      "codex 3",
		},
		Metadata: map[string]any{"type": "codex", "account_id": "acct"},
		Runtime:  fakeCapacityStatus{},
	}
	entry := (&Handler{}).buildAuthFileEntry(auth)
	if entry == nil {
		t.Fatal("capacity-codex auth is not listed")
	}
	if entry["source"] != "capacity-codex" || entry["state"] != "unavailable" || entry["state_reason"] != "CAPACITY_CODEX_REFRESH_FAILED" {
		t.Fatalf("entry = %v", entry)
	}
	account, _ := entry["capacity_account"].(map[string]any)
	if account == nil {
		raw, _ := json.Marshal(entry["capacity_account"])
		_ = json.Unmarshal(raw, &account)
	}
	if account["id"] != "hash1" || account["label"] != "codex 3" {
		t.Fatalf("capacity_account = %v", entry["capacity_account"])
	}
	encoded, _ := json.Marshal(entry)
	for _, forbidden := range []string{"access_token", "refresh_token", "auth.json", `"path"`} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("entry exposes %s: %s", forbidden, encoded)
		}
	}
}
