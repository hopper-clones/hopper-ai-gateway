package feed

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestPruneDeletesOnlyFilesEveryConsumerHasPassed(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	old := now.Add(-40*24*time.Hour).Format(hourLayout) + hourSuffix
	edge := now.Add(-31*24*time.Hour).Format(hourLayout) + hourSuffix
	recent := now.Add(-1*time.Hour).Format(hourLayout) + hourSuffix
	for _, name := range []string{old, edge, recent} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{\"id\":\"x\"}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	size := int64(len("{\"id\":\"x\"}\n"))

	// No consumer has acked anything: nothing may be deleted.
	deleted, err := Prune(dir, now, nil)
	if err != nil || len(deleted) != 0 {
		t.Fatalf("prune without consumers: deleted=%v err=%v", deleted, err)
	}

	// Consumer A is past every old file; consumer B still sits at the start of the edge file.
	acked := map[string]AckedCursor{
		"a": {Cursor: FormatCursor(recent, 0)},
		"b": {Cursor: FormatCursor(edge, 0)},
	}
	deleted, err = Prune(dir, now, acked)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != old {
		t.Fatalf("deleted = %v, want [%s]", deleted, old)
	}

	// Once B has consumed the whole edge file it is past it as well.
	acked["b"] = AckedCursor{Cursor: FormatCursor(edge, size)}
	deleted, err = Prune(dir, now, acked)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != edge {
		t.Fatalf("deleted = %v, want [%s]", deleted, edge)
	}
	remaining, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	sort.Strings(remaining)
	if len(remaining) != 1 || filepath.Base(remaining[0]) != recent {
		t.Fatalf("remaining = %v, want only %s", remaining, recent)
	}
}

func TestPruneKeepsFilesWithinRetention(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	name := now.Add(-29*24*time.Hour).Format(hourLayout) + hourSuffix
	if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deleted, err := Prune(dir, now, map[string]AckedCursor{"a": {Cursor: FormatCursor("2099-01-01T00Z.jsonl", 0)}})
	if err != nil || len(deleted) != 0 {
		t.Fatalf("deleted=%v err=%v", deleted, err)
	}
}
