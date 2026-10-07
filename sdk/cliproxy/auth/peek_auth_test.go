package auth

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func TestPeekAuthLeavesSelectorStateUntouched(t *testing.T) {
	for name, newSelector := range map[string]func() Selector{
		"round-robin":          func() Selector { return &RoundRobinSelector{} },
		"weighted-round-robin": func() Selector { return &WeightedRoundRobinSelector{} },
		"session-affinity/rr":  func() Selector { return NewSessionAffinitySelector(&RoundRobinSelector{}) },
	} {
		t.Run(name, func(t *testing.T) {
			const model = "peek-model"
			manager := NewManager(nil, newSelector(), nil)
			manager.RegisterExecutor(&mockCustomErrorExecutor{identifier: "codex"})
			ids := []string{"peek-a-" + t.Name(), "peek-b-" + t.Name()}
			for _, id := range ids {
				candidate := &Auth{ID: id, Provider: "codex", Status: StatusActive, Attributes: map[string]string{AttributeWeight: "1"}}
				registry.GetGlobalRegistry().RegisterClient(id, "codex", []*registry.ModelInfo{{ID: model}})
				t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(id) })
				if _, err := manager.Register(context.Background(), candidate); err != nil {
					t.Fatal(err)
				}
			}
			first, err := manager.PeekAuth(context.Background(), "codex", model, cliproxyexecutor.Options{})
			if err != nil {
				t.Fatal(err)
			}
			second, err := manager.PeekAuth(context.Background(), "codex", model, cliproxyexecutor.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if first.ID != second.ID {
				t.Fatalf("consecutive peeks differ: %s then %s", first.ID, second.ID)
			}
			// Real traffic starts where it would have without the peeks and then rotates.
			served, err := manager.SelectAuth(context.Background(), "codex", model, cliproxyexecutor.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if served.ID != first.ID {
				t.Fatalf("peek skipped real traffic: peek=%s served=%s", first.ID, served.ID)
			}
			next, err := manager.SelectAuth(context.Background(), "codex", model, cliproxyexecutor.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if next.ID == served.ID {
				t.Fatalf("rotation stalled after peek: %s twice", next.ID)
			}
		})
	}
}

func TestPeekAuthRefusesSelectorWithoutPeek(t *testing.T) {
	manager := NewManager(nil, &opaqueSelector{}, nil)
	manager.RegisterExecutor(&mockCustomErrorExecutor{identifier: "codex"})
	if _, err := manager.Register(context.Background(), &Auth{ID: "opaque-" + t.Name(), Provider: "codex", Status: StatusActive}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PeekAuth(context.Background(), "codex", "", cliproxyexecutor.Options{}); err == nil {
		t.Fatal("a selector without Peek must be refused rather than mutated")
	}
}

type opaqueSelector struct{}

func (s *opaqueSelector) Pick(_ context.Context, _, _ string, _ cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	return auths[0], nil
}
