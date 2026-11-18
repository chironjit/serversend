package transfer

import (
	"context"
	"errors"
	"time"

	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/source"
)

func (m *Manager) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-m.queue:
			m.dispatch(j)
		}
	}
}

// dispatch sends small uploads waiting in the queue as one batch, and any
// other transfer on its own.
func (m *Manager) dispatch(j job) {
	for {
		if !m.batchable(j) {
			m.run(j)
			return
		}
		jobs, size := []job{j}, j.size
		var next *job
	collect:
		for len(jobs) < client.MaxBatchFileCount {
			select {
			case candidate := <-m.queue:
				if m.batchable(candidate) && candidate.client == j.client && size+candidate.size <= client.MaxBatchBodyBytes {
					jobs, size = append(jobs, candidate), size+candidate.size
					continue
				}
				next = &candidate
				break collect
			default:
				break collect
			}
		}
		m.runBatch(jobs)
		if next == nil {
			return
		}
		j = *next
	}
}

func (m *Manager) batchable(j job) bool {
	return j.item.Direction == "upload" && j.client != nil && j.size <= client.MaxBatchFileBytes && j.client.SupportsBatch(j.ctx)
}

// runBatch skips files cancelled while queued. A batch request is short, so
// files cancelled during it report its actual outcome.
func (m *Manager) runBatch(jobs []job) {
	live := jobs[:0]
	files := []client.BatchFile{}
	for _, j := range jobs {
		if err := j.ctx.Err(); err != nil {
			m.finish(j, err)
			continue
		}
		m.update(j.item.ID, func(item *Item) { item.State = "active" })
		live = append(live, j)
		files = append(files, client.BatchFile{Path: j.path, Parent: j.parent})
	}
	if len(live) == 0 {
		return
	}
	outcomes, err := live[0].client.UploadBatch(m.base(), files)
	for i, j := range live {
		if err == nil {
			m.finish(j, outcomes[i].Err)
		} else {
			m.finish(j, err)
		}
	}
}

func (m *Manager) run(j job) {
	id := j.item.ID
	if err := j.ctx.Err(); err != nil || j.client == nil {
		m.finish(j, err)
		return
	}
	m.update(id, func(item *Item) { item.State = "active" })
	// Progress is reported at most ten times a second so a slow window
	// cannot hold back the transfer or flood the frontend.
	last := time.Now()
	var lastDone int64
	progress := func(done, total int64) {
		if done == 0 {
			last = time.Now()
			lastDone = 0
			m.update(id, func(item *Item) { item.Done, item.Total, item.Speed = 0, total, 0 })
			return
		}
		if now := time.Now(); done == total || now.Sub(last) >= 100*time.Millisecond {
			elapsed := now.Sub(last).Seconds()
			speed := float64(done-lastDone) / elapsed
			last, lastDone = now, done
			m.update(id, func(item *Item) { item.Done, item.Total, item.Speed = done, total, speed })
		}
	}
	var err error
	if j.item.Direction == "upload" {
		_, err = j.client.UploadTo(j.ctx, j.path, j.parent, progress)
	} else {
		err = j.client.Download(j.ctx, j.entry, j.path, progress)
	}
	m.finish(j, err)
}

// finish records a transfer's outcome. A cancelled transfer reports
// cancellation, whatever error its interruption produced.
func (m *Manager) finish(j job, err error) {
	if err != nil && j.ctx != nil && j.ctx.Err() != nil {
		err = context.Canceled
	}
	var bytes int64
	m.update(j.item.ID, func(item *Item) {
		item.Speed = 0
		switch _, skipped := source.Skipped(err); {
		case err == nil || errors.Is(err, client.ErrContentMismatch):
			item.State, item.Done, item.Error = "done", item.Total, ""
			bytes = item.Total
			if err != nil {
				item.Error = err.Error()
				err = nil
			}
		case errors.Is(err, context.Canceled):
			item.State, item.Error = "cancelled", "Cancelled"
		case skipped:
			item.State, item.Error = "skipped", message(err).Error()
		default:
			item.State, item.Error = "failed", message(err).Error()
		}
	})
	m.mu.Lock()
	if cancel := m.cancels[j.item.ID]; cancel != nil {
		cancel()
		delete(m.cancels, j.item.ID)
	}
	m.mu.Unlock()
	if j.item.Direction == "upload" {
		m.finishFile(j.item.BatchID, bytes, err)
	}
}
