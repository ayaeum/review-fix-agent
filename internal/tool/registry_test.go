package tool

import (
	"context"
	"reflect"
	"testing"
)

// stubTool is a minimal Tool for exercising the registry.
type stubTool struct{ name string }

func (s stubTool) Name() string                        { return s.name }
func (s stubTool) Description() string                 { return s.name + " desc" }
func (s stubTool) InputSchema() map[string]any         { return map[string]any{"type": "object"} }
func (s stubTool) ReadOnly(map[string]any) bool        { return true }
func (s stubTool) ConcurrencySafe(map[string]any) bool { return true }
func (s stubTool) Validate(map[string]any) error       { return nil }
func (s stubTool) Call(context.Context, map[string]any, *Context) (Result, error) {
	return Result{}, nil
}

func TestRegistryDedupAndSort(t *testing.T) {
	// Insert out of order with a duplicate name; first wins, output is sorted.
	r := NewRegistry(
		stubTool{"grep"},
		stubTool{"bash"},
		stubTool{"grep"}, // duplicate — ignored
		stubTool{"read_file"},
	)
	want := []string{"bash", "grep", "read_file"}
	if got := r.Names(); !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
	all := r.All()
	if len(all) != 3 {
		t.Fatalf("All() len = %d, want 3 (deduped)", len(all))
	}
	for i, n := range want {
		if all[i].Name() != n {
			t.Errorf("All()[%d] = %q, want %q", i, all[i].Name(), n)
		}
	}
}

func TestRegistryGet(t *testing.T) {
	r := NewRegistry(stubTool{"read_file"})
	if tl, ok := r.Get("read_file"); !ok || tl.Name() != "read_file" {
		t.Errorf("Get(read_file) = %v, %v", tl, ok)
	}
	if _, ok := r.Get("missing"); ok {
		t.Error("Get(missing) should report not found")
	}
}

// TestRegistryNamesIsACopy ensures mutating the returned slice does not corrupt
// the registry's internal order.
func TestRegistryNamesIsACopy(t *testing.T) {
	r := NewRegistry(stubTool{"a"}, stubTool{"b"})
	names := r.Names()
	names[0] = "MUTATED"
	if again := r.Names(); again[0] != "a" {
		t.Errorf("Names() returned an aliased slice; got %v after external mutation", again)
	}
}
