package testutil

import (
	"errors"
	"sync"
)

type fault struct {
	remaining int
	err       error
}

// Faults injects one-shot errors at named operation boundaries. Arm with nth=1
// to fail the next call, or a larger count to fail after known successful calls.
type Faults struct {
	mu     sync.Mutex
	points map[string]fault
}

func (f *Faults) Arm(name string, nth int, err error) {
	if nth < 1 || err == nil {
		panic("fault requires a positive call count and error")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.points == nil {
		f.points = make(map[string]fault)
	}
	f.points[name] = fault{nth, err}
}
func (f *Faults) Hit(name string) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.points[name]
	if !ok {
		return nil
	}
	p.remaining--
	if p.remaining == 0 {
		delete(f.points, name)
		return p.err
	}
	f.points[name] = p
	return nil
}

// ErrInterrupted is a synthetic network/process interruption, not a retry policy.
var ErrInterrupted = errors.New("injected interruption")
