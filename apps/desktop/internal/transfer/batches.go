package transfer

import (
	"github.com/chironjit/serversend/shared/source"
	"sort"
)

const maxSkippedItems = 100

type SkippedItem struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Batch reports one file/folder selection. Skip details are bounded while the
// total remains exact. These in-memory outcomes will move to the durable queue.
type Batch struct {
	ID           int           `json:"id"`
	Name         string        `json:"name"`
	State        string        `json:"state"`
	Files        int           `json:"files"`
	Completed    int           `json:"completed"`
	Failed       int           `json:"failed"`
	Directories  int           `json:"directories"`
	Skipped      int           `json:"skipped"`
	SkippedItems []SkippedItem `json:"skippedItems"`
	Error        string        `json:"error"`
	scanning     bool
	pending      int
}

func (m *Manager) startBatch(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.batches == nil {
		m.batches = map[int]*Batch{}
	}
	m.nextBatch++
	id := m.nextBatch
	m.batches[id] = &Batch{ID: id, Name: name, State: "scanning", SkippedItems: []SkippedItem{}, scanning: true}
	// Retain recent outcomes and all active selections. There is no file-count
	// cutoff, and historical skip paths cannot grow without bound.
	for previous, batch := range m.batches {
		if previous <= id-100 && !batch.scanning && batch.pending == 0 {
			delete(m.batches, previous)
		}
	}
	return id
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
	if err != nil {
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
			b.SkippedItems = append(b.SkippedItems, SkippedItem{skip.Path, skip.Reason})
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

func (m *Manager) finishFile(batchID int, err error) {
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
			b.SkippedItems = append(b.SkippedItems, SkippedItem{skip.Path, skip.Reason})
		}
	} else if err != nil {
		b.Failed++
	} else {
		b.Completed++
	}
	settleBatch(b)
}

func (m *Manager) Batches() []Batch {
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
