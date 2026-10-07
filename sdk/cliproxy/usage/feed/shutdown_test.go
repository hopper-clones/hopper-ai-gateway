package feed

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func TestStopDeliversEveryPublishedEventToDisk(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	store, err := Open(dir, WithClock(fixedClock(at)))
	if err != nil {
		t.Fatal(err)
	}
	manager := usage.NewManager(16)
	manager.Register(NewPlugin(store, nil))
	manager.Start(context.Background())
	for i := 0; i < 1000; i++ {
		manager.Publish(context.Background(), usage.Record{RequestID: fmt.Sprintf("shutdown-%04d", i), Provider: "claude"})
	}
	manager.Stop()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if store.Dropped() != 0 {
		t.Fatalf("dropped = %d", store.Dropped())
	}
	events, _ := readAll(t, dir, "", 400)
	if len(events) != 1000 {
		t.Fatalf("events on disk = %d, want 1000", len(events))
	}
	if !store.Write(NewUsageEvent("after-close", at)) && store.Dropped() != 1 {
		t.Fatalf("a write after close must be counted as dropped, got %d", store.Dropped())
	}
}
