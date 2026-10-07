package managementasset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestBundledConsoleMatchesManifest(t *testing.T) {
	html, err := BundledConsole()
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := os.ReadFile("console/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		HTMLSHA256 string `json:"html_sha256"`
		HTMLBytes  int    `json:"html_bytes"`
	}
	if err = json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(html)
	if hex.EncodeToString(digest[:]) != manifest.HTMLSHA256 || len(html) != manifest.HTMLBytes {
		t.Fatal("console does not match its release manifest")
	}
	if !bytes.Contains(html, []byte("Quota Management")) || !bytes.Contains(html, []byte("</html>")) {
		t.Fatal("bundled console is incomplete")
	}
	if bytes.Contains(html, []byte(`<script src="/`)) {
		t.Fatal("console requires external scripts")
	}
}
