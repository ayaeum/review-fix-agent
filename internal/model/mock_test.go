package model

import (
	"context"
	"sync"
	"testing"

	"github.com/review-fix-agent/rfa/internal/message"
)

// TestMockConcurrentStream exercises Mock under concurrent callers (as
// ParallelReview does). Run with -race to catch turn-counter data races.
func TestMockConcurrentStream(t *testing.T) {
	m := &Mock{}
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := m.Stream(context.Background(), Request{}, nil); err != nil {
				t.Errorf("stream: %v", err)
			}
		}()
	}
	wg.Wait()
	if m.turn != n {
		t.Errorf("turn = %d, want %d (no lost increments)", m.turn, n)
	}
}

// TestMockResponderReceivesTurn verifies the Responder still sees a monotonic
// turn index.
func TestMockResponderReceivesTurn(t *testing.T) {
	seen := map[int]bool{}
	var mu sync.Mutex
	m := &Mock{Responder: func(_ Request, turn int) message.Message {
		mu.Lock()
		seen[turn] = true
		mu.Unlock()
		return message.NewAssistantText("ok")
	}}
	for i := 0; i < 3; i++ {
		m.Stream(context.Background(), Request{}, nil)
	}
	for i := 0; i < 3; i++ {
		if !seen[i] {
			t.Errorf("turn %d was not passed to Responder", i)
		}
	}
}
