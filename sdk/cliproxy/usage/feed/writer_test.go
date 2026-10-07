package feed

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixedClock(at time.Time) func() time.Time {
	return func() time.Time { return at }
}

func readAll(t *testing.T, dir string, cursor string, limit int) ([]json.RawMessage, string) {
	t.Helper()
	var out []json.RawMessage
	for {
		page, err := Read(dir, cursor, limit)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		out = append(out, page.Events...)
		cursor = page.NextCursor
		if !page.HasMore {
			return out, cursor
		}
	}
}

func eventIDs(t *testing.T, events []json.RawMessage) []string {
	t.Helper()
	ids := make([]string, 0, len(events))
	for _, raw := range events {
		var ev struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatalf("decode event %s: %v", raw, err)
		}
		ids = append(ids, ev.ID)
	}
	return ids
}

func TestWriterBatchesIntoHourlyFilesAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 10, 7, 12, 30, 0, 0, time.UTC)
	clock := &movingClock{at: start}

	first, err := NewWriter(dir, WithClock(clock.Now))
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	for i := 0; i < 5000; i++ {
		if i == 2500 {
			clock.Set(start.Add(time.Hour))
		}
		if !first.Write(NewUsageEvent(fmt.Sprintf("req-%05d", i), clock.Now())) {
			t.Fatalf("write %d dropped", i)
		}
	}
	// A consumer reads part of the feed before the "restart" and keeps its cursor.
	if err := first.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}
	beforeRestart, cursor := readAll(t, dir, "", 777)
	if len(beforeRestart) != 5000 {
		t.Fatalf("events before restart = %d, want 5000", len(beforeRestart))
	}

	second, err := NewWriter(dir, WithClock(clock.Now))
	if err != nil {
		t.Fatalf("new writer after restart: %v", err)
	}
	for i := 5000; i < 10000; i++ {
		if !second.Write(NewUsageEvent(fmt.Sprintf("req-%05d", i), clock.Now())) {
			t.Fatalf("write %d dropped", i)
		}
	}
	if err := second.Close(); err != nil {
		t.Fatalf("close second: %v", err)
	}
	if second.Dropped() != 0 {
		t.Fatalf("dropped = %d, want 0", second.Dropped())
	}

	afterRestart, _ := readAll(t, dir, cursor, 777)
	all := eventIDs(t, append(beforeRestart, afterRestart...))
	if len(all) != 10000 {
		t.Fatalf("total events = %d, want 10000", len(all))
	}
	seen := make(map[string]struct{}, len(all))
	for i, id := range all {
		want := fmt.Sprintf("req-%05d", i)
		if id != want {
			t.Fatalf("event %d id = %s, want %s (order or duplicate)", i, id, want)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate event id %s", id)
		}
		seen[id] = struct{}{}
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("hourly files = %v, want two (12Z and 13Z)", files)
	}
	if filepath.Base(files[0]) != "2026-10-07T12Z.jsonl" || filepath.Base(files[1]) != "2026-10-07T13Z.jsonl" {
		t.Fatalf("unexpected file names %v", files)
	}
}

func TestWriterFlushesOnTickWithoutClosing(t *testing.T) {
	dir := t.TempDir()
	ticks := make(chan time.Time)
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	w, err := NewWriter(dir, WithClock(fixedClock(at)), WithTicker(ticks))
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	defer func() { _ = w.Close() }()
	w.Write(NewUsageEvent("tick-1", at))
	ticks <- at.Add(250 * time.Millisecond)
	w.Flush()
	page, err := Read(dir, "", 10)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(page.Events) != 1 || eventIDs(t, page.Events)[0] != "tick-1" {
		t.Fatalf("events after tick = %s", page.Events)
	}
}

func TestWriterNeverBlocksWhenQueueIsFull(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	ticks := make(chan time.Time)
	w, err := NewWriter(dir, WithClock(fixedClock(at)), WithTicker(ticks), WithQueueSize(2), withPausedLoop())
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	defer func() { _ = w.Close() }()
	accepted := 0
	for i := 0; i < 10; i++ {
		if w.Write(NewUsageEvent(fmt.Sprintf("full-%d", i), at)) {
			accepted++
		}
	}
	if accepted != 2 {
		t.Fatalf("accepted = %d, want 2", accepted)
	}
	if w.Dropped() != 8 {
		t.Fatalf("dropped = %d, want 8", w.Dropped())
	}
}

func TestWriterRejectsUnwritableDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWriter(file); err == nil {
		t.Fatal("expected error for a file used as directory")
	}
}

func TestUsageEventJSONMatchesContract(t *testing.T) {
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	ev := NewUsageEvent("abc", at)
	ev.KeyID = "0123456789abcdef"
	ev.Lane, ev.Project, ev.Task = "lane-test", "project:822b", "task:a929eb4f"
	ev.AccountID = "auth-1"
	ev.Provider, ev.Model, ev.Effort = "claude", "claude-fable-5-1", "high"
	ev.Tokens = Tokens{Input: 10, CachedInput: 4, CacheWrite: 1, Output: 5, Reasoning: 2, Total: 15}
	ev.LatencyMS = 42
	ev.Status = "ok"
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"v":1,"id":"abc","at":"2026-10-07T12:00:00.000Z","kind":"usage","key_id":"0123456789abcdef","lane":"lane-test","project":"project:822b","task":"task:a929eb4f","account_id":"auth-1","account_hash":null,"provider":"claude","model":"claude-fable-5-1","effort":"high","tokens":{"input":10,"cached_input":4,"cache_write":1,"output":5,"reasoning":2,"total":15},"latency_ms":42,"cache_hit":null,"status":"ok"}`
	if string(raw) != want {
		t.Fatalf("usage json\n got %s\nwant %s", raw, want)
	}

	quota := NewQuotaEvent("abc", "5h", at)
	quota.AccountID = "auth-1"
	quota.Provider = "claude"
	quota.Utilization = 0.42
	quota.Exhausted = false
	rawQuota, err := json.Marshal(quota)
	if err != nil {
		t.Fatal(err)
	}
	wantQuota := `{"v":1,"id":"abc:quota:5h","at":"2026-10-07T12:00:00.000Z","kind":"quota","account_id":"auth-1","account_hash":null,"provider":"claude","scope":"5h","utilization":0.42,"resets_at":null,"exhausted":false}`
	if string(rawQuota) != wantQuota {
		t.Fatalf("quota json\n got %s\nwant %s", rawQuota, wantQuota)
	}
}

// movingClock is a controllable clock shared by a writer and its test.
type movingClock struct {
	at time.Time
}

func (c *movingClock) Now() time.Time   { return c.at }
func (c *movingClock) Set(at time.Time) { c.at = at }

func TestWriterCountsDropsAfterClose(t *testing.T) {
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	w, err := NewWriter(t.TempDir(), WithClock(fixedClock(at)))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if w.Write(NewUsageEvent("late", at)) {
		t.Fatal("write after close must fail")
	}
	if w.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", w.Dropped())
	}
}

// tornFile fails its first write after half the bytes, like a disk-full tear.
type tornFile struct {
	data   []byte
	writes int
}

func (f *tornFile) Write(p []byte) (int, error) {
	f.writes++
	if f.writes == 1 {
		half := len(p) / 2
		f.data = append(f.data, p[:half]...)
		return half, fmt.Errorf("torn write")
	}
	f.data = append(f.data, p...)
	return len(p), nil
}
func (f *tornFile) Sync() error  { return nil }
func (f *tornFile) Close() error { return nil }
func (f *tornFile) Truncate(size int64) error {
	f.data = f.data[:size]
	return nil
}
func (f *tornFile) Seek(offset int64, whence int) (int64, error) {
	return int64(len(f.data)), nil
}

func TestWriterTruncatesTornWriteBeforeNextBatch(t *testing.T) {
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	file := &tornFile{}
	ticks := make(chan time.Time)
	w, err := NewWriter(t.TempDir(), WithClock(fixedClock(at)), WithTicker(ticks), withFileOpener(func(string) (feedFile, error) { return file, nil }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	w.Write(NewUsageEvent("first", at))
	w.Flush()
	w.Write(NewUsageEvent("second", at))
	w.Flush()
	want, _ := json.Marshal(NewUsageEvent("second", at))
	if string(file.data) != string(want)+"\n" {
		t.Fatalf("file after torn write = %q, want only the second event", file.data)
	}
}
