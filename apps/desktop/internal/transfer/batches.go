package transfer

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/chironjit/serversend/shared/source"
)

const maxSkippedItems = 100

type SkippedItem struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	ID     int    `json:"id"` // durable task; 0 for in-memory skips
	// Unencrypted marks a private file over the size limit that the user may
	// choose to upload unencrypted with the "unencrypted" queue action.
	Unencrypted bool `json:"unencrypted"`
}

// Batch reports one file/folder selection. Skip details are bounded while the
// total remains exact. These in-memory outcomes will move to the durable queue.
type Batch struct {
	Scope        string        `json:"scope"`
	ID           int           `json:"id"`
	Name         string        `json:"name"`
	State        string        `json:"state"`
	Files        int           `json:"files"`
	Completed    int           `json:"completed"`
	Failed       int           `json:"failed"`
	Directories  int           `json:"directories"`
	Skipped      int           `json:"skipped"`
	Oversized    int           `json:"oversized"` // skipped private files over the size limit
	SkippedItems []SkippedItem `json:"skippedItems"`
	Error        string        `json:"error"`
	Cancelled    int           `json:"cancelled"`
	Bytes        int64         `json:"bytes"`      // uploaded by completed files
	StartedAt    int64         `json:"startedAt"`  // Unix milliseconds
	FinishedAt   int64         `json:"finishedAt"` // Unix milliseconds; 0 while unfinished
	scanning     bool
	pending      int
	cancelled    bool
	cancel       context.CancelFunc
}

// startBatch registers a selection whose context cancels its scan and files.
func (m *Manager) startBatch(parent context.Context, name string) (int, context.Context) {
	ctx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.batches == nil {
		m.batches = map[int]*Batch{}
	}
	m.nextBatch++
	id := m.nextBatch
	m.batches[id] = &Batch{ID: id, Name: name, State: "scanning", SkippedItems: []SkippedItem{}, scanning: true, StartedAt: time.Now().UnixMilli(), cancel: cancel}
	// Retain recent outcomes and all active selections. There is no file-count
	// cutoff, and historical skip paths cannot grow without bound.
	for previous, batch := range m.batches {
		if previous <= id-100 && !batch.scanning && batch.pending == 0 {
			delete(m.batches, previous)
		}
	}
	return id, ctx
}

// CancelBatch stops scanning, skips queued files and interrupts active ones.
// Files already published stay on the server.
func (m *Manager) CancelBatch(id int) {
	if m.durable != nil {
		_ = m.QueueAction(id, true, "cancel")
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if b := m.batches[id]; b != nil && (b.scanning || b.pending > 0) {
		b.cancelled = true
		b.cancel()
	}
}

func settleBatch(b *Batch) {
	if b.scanning {
		b.State = "scanning"
		return
	}
	if b.pending > 0 {
		b.State = "running"
		return
	}
	b.State = "done"
	if b.FinishedAt == 0 {
		b.FinishedAt = time.Now().UnixMilli()
		b.cancel()
	}
	if b.cancelled || (b.Cancelled > 0 && b.Completed == 0 && b.Failed == 0) {
		b.State = "cancelled"
		return
	}
	if b.Failed > 0 || b.Error != "" {
		b.State = "failed"
		return
	}
	if b.Skipped > 0 {
		b.State = "done_with_skips"
		if b.Completed == 0 && b.Directories == 0 {
			b.State = "all_skipped"
		}
	}
}

func (m *Manager) endScan(id int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.batches[id]
	if b == nil {
		return
	}
	b.scanning = false
	if err != nil && !(b.cancelled && errors.Is(err, context.Canceled)) {
		b.Error = message(err).Error()
	}
	settleBatch(b)
}

func (m *Manager) skip(id int, skip *source.SkipError) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b := m.batches[id]; b != nil {
		b.Skipped++
		if len(b.SkippedItems) < maxSkippedItems {
			b.SkippedItems = append(b.SkippedItems, SkippedItem{Path: skip.Path, Reason: skip.Reason})
		}
		settleBatch(b)
	}
}

func (m *Manager) queuedLocked(id int) {
	if b := m.batches[id]; b != nil {
		b.Files++
		b.pending++
	}
}

func (m *Manager) finishFile(batchID int, bytes int64, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.batches[batchID]
	if b == nil {
		return
	}
	b.pending--
	if skip, ok := source.Skipped(err); ok {
		b.Skipped++
		if len(b.SkippedItems) < maxSkippedItems {
			b.SkippedItems = append(b.SkippedItems, SkippedItem{Path: skip.Path, Reason: skip.Reason})
		}
	} else if errors.Is(err, context.Canceled) {
		b.Cancelled++
	} else if err != nil {
		b.Failed++
	} else {
		b.Completed++
		b.Bytes += bytes
	}
	settleBatch(b)
}

func (m *Manager) Batches() []Batch {
	if m.durable != nil {
		scope, _, err := m.durableScope()
		if err != nil {
			return []Batch{}
		}
		batches, err := m.durable.store.batches(m.base(), scope)
		if err != nil {
			return []Batch{}
		}
		return batches
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Batch, 0, len(m.batches))
	for id := max(1, m.nextBatch-99); id <= m.nextBatch; id++ {
		if b := m.batches[id]; b != nil {
			copy := *b
			copy.SkippedItems = append([]SkippedItem{}, b.SkippedItems...)
			result = append(result, copy)
		}
	}
	// Older active batches remain observable until they finish.
	for id, b := range m.batches {
		if id <= m.nextBatch-100 && (b.scanning || b.pending > 0) {
			copy := *b
			copy.SkippedItems = append([]SkippedItem{}, b.SkippedItems...)
			result = append(result, copy)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
