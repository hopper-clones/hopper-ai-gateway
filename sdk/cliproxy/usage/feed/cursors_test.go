package feed

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCursorsAckPersistsAtomicallyAndLists(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for _, name := range []string{"2026-10-07T11Z.jsonl", "2026-10-07T12Z.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cursors := NewCursors(dir)
	cursors.now = fixedClock(at)
	if err := cursors.Ack("capacity", FormatCursor("2026-10-07T12Z.jsonl", 120)); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if err := cursors.Ack("effectiveness", FormatCursor("2026-10-07T11Z.jsonl", 0)); err != nil {
		t.Fatalf("ack: %v", err)
	}
	listed, err := cursors.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 2 || listed["capacity"].Cursor != "2026-10-07T12Z.jsonl:120" || !time.Time(listed["capacity"].AckedAt).Equal(at) {
		t.Fatalf("listed = %+v", listed)
	}
	// A fresh instance reads the same file back.
	reloaded, err := NewCursors(dir).List()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded["effectiveness"].Cursor != "2026-10-07T11Z.jsonl:0" {
		t.Fatalf("reloaded = %+v", reloaded)
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "cursors.json*")); len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(dir, "cursors.json")); err != nil {
		t.Fatal(err)
	}
}

func TestCursorsRejectBadInput(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "2026-10-07T12Z.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cursors := NewCursors(dir)
	if err := cursors.Ack("", FormatCursor("2026-10-07T12Z.jsonl", 0)); err == nil {
		t.Fatal("empty consumer accepted")
	}
	if err := cursors.Ack("../x", FormatCursor("2026-10-07T12Z.jsonl", 0)); err == nil {
		t.Fatal("path-like consumer accepted")
	}
	if err := cursors.Ack("capacity", "garbage"); err == nil {
		t.Fatal("malformed cursor accepted")
	}
	listed, err := cursors.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("listed = %+v, want empty", listed)
	}
}

func TestCursorsAckRefusesBackwardsAndMissingFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"2026-10-07T12Z.jsonl", "2026-10-07T13Z.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{\"id\":\"x\"}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cursors := NewCursors(dir)
	if err := cursors.Ack("capacity", FormatCursor("2026-10-07T13Z.jsonl", 5)); err != nil {
		t.Fatalf("forward ack: %v", err)
	}
	if err := cursors.Ack("capacity", FormatCursor("2026-10-07T13Z.jsonl", 4)); err == nil {
		t.Fatal("ack moving the offset backwards must be refused")
	}
	if err := cursors.Ack("capacity", FormatCursor("2026-10-07T12Z.jsonl", 100)); err == nil {
		t.Fatal("ack moving to an earlier file must be refused")
	}
	if err := cursors.Ack("capacity", FormatCursor("2026-10-07T14Z.jsonl", 0)); err == nil {
		t.Fatal("ack naming a file that does not exist must be refused")
	}
	if err := cursors.Ack("capacity", FormatCursor("2026-10-07T13Z.jsonl", 5)); err != nil {
		t.Fatalf("re-acking the same cursor must be allowed: %v", err)
	}
	if err := cursors.Delete("capacity"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := cursors.Delete("capacity"); err == nil {
		t.Fatal("deleting an unknown consumer must report it")
	}
	listed, err := cursors.List()
	if err != nil || len(listed) != 0 {
		t.Fatalf("listed after delete = %+v err=%v", listed, err)
	}
}
