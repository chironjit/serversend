package testutil

import (
	"math/rand"
	"sync"
)

// Random is a repeatable io.Reader for test IDs/keys; it is NOT cryptographic.
// Supply bytes.NewReader when a test needs finite entropy or an EOF failure.
type Random struct {
	mu     sync.Mutex
	source *rand.Rand
}

func NewRandom(seed int64) *Random { return &Random{source: rand.New(rand.NewSource(seed))} }
func (r *Random) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.source.Read(p)
}
