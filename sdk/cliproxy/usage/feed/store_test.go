package feed

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreWritesReadsAcksAndPrunesOnRotation(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	clock := &movingClock{at: start}
	stale := start.Add(-40*24*time.Hour).Format(hourLayout) + hourSuffix
	if err := os.WriteFile(filepath.Join(dir, stale), []byte("{\"id\":\"old\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := Open(dir, WithClock(clock.Now))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()
	if store.Dir() != dir {
		t.Fatalf("dir = %s", store.Dir())
	}

	store.Write(NewUsageEvent("one", clock.Now()))
	store.Flush()
	page, err := store.Read("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if ids := eventIDs(t, page.Events); len(ids) != 2 || ids[0] != "old" || ids[1] != "one" {
		t.Fatalf("ids = %v", ids)
	}
	if err := store.Ack("capacity", page.NextCursor); err != nil {
		t.Fatalf("ack: %v", err)
	}
	acked, err := store.Cursors()
	if err != nil {
		t.Fatal(err)
	}
	if acked["capacity"].Cursor != page.NextCursor {
		t.Fatalf("acked = %+v", acked)
	}

	// Moving into the next hour rotates the file and prunes what every consumer passed.
	clock.Set(start.Add(time.Hour))
	store.Write(NewUsageEvent("two", clock.Now()))
	store.Flush()
	if _, err := os.Stat(filepath.Join(dir, stale)); !os.IsNotExist(err) {
		t.Fatalf("stale file still present after rotation: %v", err)
	}
	page, err = store.Read(page.NextCursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if ids := eventIDs(t, page.Events); len(ids) != 1 || ids[0] != "two" {
		t.Fatalf("ids after rotation = %v", ids)
	}
}
