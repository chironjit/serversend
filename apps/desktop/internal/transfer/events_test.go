package transfer

import (
	"testing"
	"time"
)

// A stalled window must not hold back transfers, and it receives only the
// newest state of each transfer once it catches up.
func TestCoalesceNeverBlocksAndKeepsNewestState(t *testing.T) {
	release := make(chan struct{})
	batches := make(chan []Item, 10)
	emit := coalesce(t.Context(), func(items []Item) {
		<-release
		batches <- items
	})
	emit(Item{ID: 1, State: "queued"})
	time.Sleep(20 * time.Millisecond) // the first delivery is now stalled

	done := make(chan struct{})
	go func() {
		for i := range 10000 {
			emit(Item{ID: 2 + i%3, State: "active", Done: int64(i)})
		}
		emit(Item{ID: 2, State: "done"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("emitting blocked behind a stalled window")
	}

	close(release)
	if first := <-batches; len(first) != 1 || first[0].ID != 1 {
		t.Fatalf("first delivery = %+v", first)
	}
	second := <-batches
	if len(second) != 3 || second[0].ID != 2 || second[0].State != "done" || second[2].Done != 9998 {
		t.Fatalf("second delivery = %+v", second)
	}
}
