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
	cursors := NewCursors(t.TempDir())
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
