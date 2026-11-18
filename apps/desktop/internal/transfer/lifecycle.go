package transfer

import "errors"

// Paused reports whether every transfer is held by SetPaused.
func (m *Manager) Paused() bool {
	if m.durable == nil {
		return false
	}
	m.durable.mu.Lock()
	defer m.durable.mu.Unlock()
	return m.durable.paused
}

// SetPaused durably holds or releases all transfers on every server. Held work
// stops at its last checkpoint and stays queued, including across restarts;
// per-item and per-selection pauses are kept separately.
func (m *Manager) SetPaused(paused bool) error {
	q := m.durable
	if q == nil {
		return errors.New("persistent queue is unavailable")
	}
	if _, err := q.store.db.ExecContext(m.base(), `INSERT INTO transfer_preferences(key,value) VALUES('paused',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, paused); err != nil {
		return err
	}
	q.mu.Lock()
	q.paused = paused
	q.mu.Unlock()
	if paused {
		return q.suspend()
	}
	q.notify()
	return nil
}

// Wake recovers after the device resumes from sleep. Requests over connections
// that may not have survived are interrupted and resume from their checkpoints,
// pooled connections are dropped and network back-off is cleared.
func (m *Manager) Wake() error {
	q := m.durable
	if q == nil {
		return nil
	}
	err := q.suspend()
	for _, c := range m.connectedClients() {
		c.CloseIdleConnections()
	}
	if _, e := q.store.db.ExecContext(m.base(), `UPDATE transfer_tasks SET retry_at=0 WHERE state IN ('waiting_network','retry_scheduled')`); e != nil && err == nil {
		err = e
	}
	q.notify()
	return err
}

// Stopped is closed once the context given to New has ended, workers have
// recorded their checkpoints and the queue database is closed.
func (m *Manager) Stopped() <-chan struct{} {
	if m.durable == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return m.durable.closed
}
