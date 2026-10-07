package usage

import (
	"context"
	"sync/atomic"
	"testing"
)

type countingPlugin struct{ seen atomic.Int64 }

func (p *countingPlugin) HandleUsage(context.Context, Record) { p.seen.Add(1) }

func TestManagerStopWaitsForDrain(t *testing.T) {
	manager := NewManager(16)
	plugin := &countingPlugin{}
	manager.Register(plugin)
	manager.Start(context.Background())
	for i := 0; i < 1000; i++ {
		manager.Publish(context.Background(), Record{RequestID: "drain"})
	}
	manager.Stop()
	if got := plugin.seen.Load(); got != 1000 {
		t.Fatalf("delivered %d of 1000 before Stop returned", got)
	}
	manager.Publish(context.Background(), Record{RequestID: "late"})
	if got := plugin.seen.Load(); got != 1000 {
		t.Fatalf("publish after Stop delivered: %d", got)
	}
}
