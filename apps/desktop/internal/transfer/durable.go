package transfer

import (
	"context"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/source"
)

type activeDurable struct {
	batch  int
	cancel context.CancelFunc
}
type durableQueue struct {
	closed       chan struct{}
	storageError string
	manager      *Manager
	store        *durableStore
	mu           sync.Mutex
	active       map[int]activeDurable
	wake         chan struct{}
	wg           sync.WaitGroup
	bandwidth    int64
	rateMu       sync.Mutex
	nextByte     time.Time
	rateChanged  chan struct{}
}

func (m *Manager) enableDurable(dir string) error {
	if dir == "" {
		config, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		dir = filepath.Join(config, "ServerSend")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	lock, err := lockQueue(dir)
	if err != nil {
		return err
	}
	store, err := openDurable(m.base(), dir)
	if err != nil {
		lock.Close()
		return err
	}
	q := &durableQueue{closed: make(chan struct{}), manager: m, store: store, active: map[int]activeDurable{}, wake: make(chan struct{}, workers), rateChanged: make(chan struct{})}
	var bandwidth int64
	err = store.db.QueryRowContext(m.base(), `SELECT value FROM transfer_preferences WHERE key='bandwidth'`).Scan(&bandwidth)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		store.db.Close()
		lock.Close()
		return err
	}
	q.bandwidth = bandwidth
	m.durable = q
	for range workers {
		q.wg.Add(1)
		go q.work()
	}
	go func() { <-m.base().Done(); q.wg.Wait(); store.db.Close(); lock.Close(); close(q.closed) }()
	return nil
}
func (q *durableQueue) notify() {
	for range workers {
		select {
		case q.wake <- struct{}{}:
		default:
			return
		}
	}
}
func (m *Manager) durableScope() (protocol.Scope, *client.Client, error) {
	c, err := m.current()
	if err != nil {
		return protocol.Scope{}, nil, err
	}
	account, ok := c.Account()
	if !ok {
		return protocol.Scope{}, nil, errors.New("sign in before managing transfers")
	}
	return protocol.Scope{ServerID: account.ServerID, AccountID: account.AccountID}, c, nil
}
func (q *durableQueue) work() {
	defer q.wg.Done()
	for {
		if q.manager.base().Err() != nil {
			return
		}
		if q.manager.connectionMu.TryLock() {
			scope, c, err := q.manager.durableScope()
			q.manager.mu.Lock()
			pendingLogin := q.manager.challenge != ""
			q.manager.mu.Unlock()
			if err == nil && !pendingLogin {
				q.mu.Lock()
				exclude := make([]int, 0, len(q.active))
				for id := range q.active {
					exclude = append(exclude, id)
				}
				t, e := q.store.claim(q.manager.base(), scope, exclude...)
				if e == nil {
					ctx, cancel := context.WithCancel(q.manager.base())
					q.active[t.ID] = activeDurable{t.BatchID, cancel}
					q.mu.Unlock()
					q.manager.connectionMu.Unlock()
					q.run(ctx, c, t)
					cancel()
					q.mu.Lock()
					delete(q.active, t.ID)
					q.mu.Unlock()
					continue
				}
				if !errors.Is(e, sql.ErrNoRows) {
					q.storageError = e.Error()
				}
				q.mu.Unlock()
			}
			q.manager.connectionMu.Unlock()
		}
		select {
		case <-q.manager.base().Done():
			return
		case <-q.wake:
		case <-time.After(500 * time.Millisecond):
		}
	}
}
func (q *durableQueue) run(ctx context.Context, c *client.Client, t durableTask) {
	if t.State == "cancel_pending" {
		var err error
		if t.Payload.Upload != nil {
			err = c.CancelUploadCheckpoint(ctx, t.Payload.Upload, func(cp *client.UploadCheckpoint) error {
				t.Payload.Upload = cp
				return q.store.save(q.manager.base(), t)
			})
		}
		state := "cancelled"
		if t.Payload.CancelDisposition != "" {
			state = t.Payload.CancelDisposition
		}
		if err == nil && t.Kind == "download" && t.Payload.DownloadReady {
			var matched bool
			matched, err = c.DownloadMatches(ctx, t.Payload.Entry, t.Payload.Path)
			var missing api.Error
			if errors.As(err, &missing) && (missing.Code == api.NotFound || missing.Code == api.UploadGone) {
				err = nil
			}
			if matched {
				state = "done"
				t.Done = t.Total
			}
		}
		if err == nil && t.Kind == "download" && state != "done" {
			err = c.CancelDownload(ctx, t.Payload.Entry, t.Payload.Path)
		}
		if err == nil && t.Payload.Upload != nil && t.Payload.Upload.Result != nil {
			state = "done"
			t.Done = t.Total
			if t.Payload.Upload.Result.State == protocol.UploadConflicted {
				state = "failed"
			}
		}
		retry := int64(0)
		msg := ""
		if state == "skipped" {
			msg = t.Payload.CancelReason
		}
		if state == "failed" {
			msg = "The upload was retained as a conflicting server version"
		}
		if err != nil {
			state = "cancel_pending"
			retry = time.Now().Add(30 * time.Second).UnixMilli()
			msg = message(err).Error()
		}
		q.persist(func() error {
			if e := q.store.save(q.manager.base(), t); e != nil {
				return e
			}
			_, e := q.store.db.ExecContext(q.manager.base(), `UPDATE transfer_tasks SET state=?,retry_at=?,error=?,done=?,total=? WHERE id=? AND state='cancelling'`, state, retry, msg, t.Done, t.Total, t.ID)
			return e
		})
		t.State, t.Error = state, msg
		q.manager.emit(t.item())
		return
	}
	t.State = "active"
	q.manager.emit(t.item())
	requestCtx, stopRequest := context.WithCancel(ctx)
	defer stopRequest()
	var progressErr error
	last := time.Now()
	lastDone := t.Done
	rateDone := t.Done
	progress := func(done, total int64) {
		delta := max(int64(0), done-rateDone)
		rateDone = done
		q.throttle(ctx, delta)
		t.Done, t.Total = done, total
		if t.Kind == "download" {
			if done == total {
				t.Payload.DownloadReady = true
			}
			if e := q.store.save(q.manager.base(), t); e != nil {
				progressErr = e
				stopRequest()
			}
		}
		if time.Since(last) >= 200*time.Millisecond || done == total {
			snapshot := t.item()
			snapshot.Speed = float64(max(int64(0), done-lastDone)) / max(time.Since(last).Seconds(), 0.001)
			q.manager.emit(snapshot)
			last = time.Now()
			lastDone = done
		}
	}
	var err error
	switch t.Kind {
	case "upload":
		if t.Payload.Upload == nil {
			t.Payload.Upload = &client.UploadCheckpoint{}
		}
		_, err = c.UploadResumable(requestCtx, t.Payload.Path, t.Payload.Parent, t.Payload.Upload, func(cp *client.UploadCheckpoint) error {
			t.Payload.Upload = cp
			t.Done, t.Total = cp.AcknowledgedBytes, cp.Manifest.TotalBytes
			// Writes are bounded by a chunk acknowledgement or admission, not every
			// progress callback; paused work still records a received acknowledgement.
			return q.store.save(q.manager.base(), t)
		}, progress)
	case "scan_upload":
		err = q.scanUpload(requestCtx, c, &t)
	case "download":
		matched := false
		if t.Payload.DownloadReady {
			matched, err = c.DownloadMatches(requestCtx, t.Payload.Entry, t.Payload.Path)
		}
		if err != nil {
			break
		}
		if matched {
			t.Done = t.Total
			break
		}
		err = c.DownloadWithPolicy(requestCtx, t.Payload.Entry, t.Payload.Path, t.Payload.Policy, progress)
	case "scan_restore":
		err = q.scanRestore(requestCtx, c, &t)
	default:
		err = errors.New("unknown persisted transfer kind")
	}
	if progressErr != nil {
		err = progressErr
	}
	if e := q.store.save(q.manager.base(), t); e != nil && err == nil {
		err = e
	}
	state, msg, retry := "done", "", int64(0)
	if err != nil {
		msg = message(err).Error()
		var typed api.Error
		var network net.Error
		var transport *url.Error
		var unknown x509.UnknownAuthorityError
		var invalid x509.CertificateInvalidError
		var hostname x509.HostnameError
		switch {
		case errors.Is(err, errContinue):
			state = "queued"
			msg = ""
		case ctx.Err() != nil:
			msg = ""
			state = "queued" // persisted pause/cancel wins the CAS below
		case errors.Is(err, client.ErrIdentityChanged) || errors.As(err, &unknown) || errors.As(err, &invalid) || errors.As(err, &hostname):
			state = "failed"
		case errors.Is(err, syscall.ENOSPC):
			state = "waiting_capacity"
			retry = time.Now().Add(30 * time.Second).UnixMilli()
		case errors.Is(err, client.ErrSourceChanged):
			msg = "The source changed; a fresh attempt will start automatically"
			state = "retry_scheduled"
			retry = time.Now().Add(time.Duration(min(60, 5<<min(t.Attempts, 4))) * time.Second).UnixMilli()
		case isSkipped(err):
			state = "skipped"
			if t.Kind == "upload" && t.Payload.Upload != nil && t.Payload.Upload.Manifest.FileID != "" {
				t.Payload.CancelDisposition = "skipped"
				t.Payload.CancelReason = msg
				state = "cancel_pending"
			}
		case errors.Is(err, client.ErrTOTPRequired):
			state = "waiting_authentication"
			retry = time.Now().Add(30 * time.Second).UnixMilli()
		case errors.As(err, &typed):
			switch typed.Code {
			case api.AuthenticationRequired:
				state = "waiting_authentication"
			case api.CapacityExhausted:
				state = "waiting_capacity"
			case api.Overloaded, api.Unavailable:
				state = "retry_scheduled"
			case api.UploadGone:
				if t.Kind == "upload" {
					state = "retry_scheduled"
				} else {
					state = "failed"
				}
			case api.SnapshotExpired:
				t.Payload.Cursor = ""
				_ = q.store.save(q.manager.base(), t)
				state = "queued"
			default:
				state = "failed"
			}
			if state != "failed" && state != "queued" {
				retry = time.Now().Add(time.Duration(max(int64(30), min(typed.RetryAfterSeconds, int64(3600)))) * time.Second).UnixMilli()
			}
		case errors.As(err, &transport) || errors.As(err, &network) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
			state = "waiting_network"
			retry = time.Now().Add(time.Duration(min(300, 5<<min(t.Attempts, 6))) * time.Second).UnixMilli()
		default:
			state = "failed"
		}
	}
	if err == nil {
		t.Done = t.Total
	}
	if !q.persist(func() error {
		if e := q.store.save(q.manager.base(), t); e != nil {
			return e
		}
		return q.store.finish(q.manager.base(), t, state, msg, retry)
	}) {
		return
	}
	t.State, t.Error = state, msg
	q.manager.emit(t.item())
	q.notify()
}
func isSkipped(err error) bool { _, ok := source.Skipped(err); return ok }
func (q *durableQueue) throttle(ctx context.Context, bytes int64) {
	q.rateMu.Lock()
	limit := q.bandwidth
	if limit <= 0 || bytes <= 0 {
		q.rateMu.Unlock()
		return
	}
	now := time.Now()
	start := maxTime(now, q.nextByte)
	duration := time.Duration(float64(bytes) / float64(limit) * float64(time.Second))
	q.nextByte = start.Add(duration)
	changed := q.rateChanged
	wait := time.Until(q.nextByte)
	q.rateMu.Unlock()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		q.rateMu.Lock()
		q.nextByte = time.Now()
		if q.rateChanged != nil {
			close(q.rateChanged)
		}
		q.rateChanged = make(chan struct{})
		q.rateMu.Unlock()
	case <-changed:
	case <-timer.C:
	}
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func (q *durableQueue) scanUpload(ctx context.Context, c *client.Client, t *durableTask) error {
	f, err := source.Open(t.Payload.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("selected upload directory changed or disappeared")
	}
	folder, err := c.CreateDirectory(ctx, t.Payload.Parent, info.Name())
	if err != nil {
		return err
	}
	for {
		entries, readErr := f.ReadDir(128)
		children := make([]durableTask, 0, len(entries))
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			path := filepath.Join(t.Payload.Path, entry.Name())
			child := durableTask{Name: entry.Name(), Kind: "upload", Payload: durablePayload{Path: path, Parent: folder.ID}}
			opened, err := source.Open(path)
			if err != nil {
				if isSkipped(err) {
					child.State = "skipped"
					child.Error = err.Error()
				} else {
					child.State = "failed"
					child.Error = err.Error()
				}
			} else {
				stat, e := opened.Stat()
				opened.Close()
				if e != nil {
					child.State = "failed"
					child.Error = e.Error()
				} else if stat.IsDir() {
					child.Kind = "scan_upload"
				} else {
					child.Total = stat.Size()
				}
			}
			children = append(children, child)
		}
		if err := q.store.children(ctx, *t, children); err != nil {
			return err
		}
		q.notify()
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}
func (q *durableQueue) scanRestore(ctx context.Context, c *client.Client, t *durableTask) error {
	page, err := PlanRestoreDirectory(ctx, c, t.Payload.Entry, t.Payload.Path, t.Payload.Cursor, t.Payload.Policy)
	if err != nil {
		return err
	}
	children := make([]durableTask, 0, len(page.Tasks))
	for _, child := range page.Tasks {
		children = append(children, durableTask{Kind: child.Kind, Name: child.Entry.Standard.Name, Payload: durablePayload{Path: child.Path, Root: t.Payload.Root, Entry: child.Entry, Policy: t.Payload.Policy}})
	}
	t.Payload.Cursor = page.NextCursor
	if err := q.store.children(ctx, *t, children); err != nil {
		return err
	}
	if page.NextCursor != "" {
		return errContinue
	}
	return nil
}
func (m *Manager) queueUploads(paths []string, parent string, folder bool) error {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	if m.durableErr != nil {
		return m.durableErr
	}
	scope, _, err := m.durableScope()
	if err != nil {
		return err
	}
	if parent != "" && protocol.ID(parent).Validate() != nil {
		return errors.New("invalid destination folder")
	}
	tasks := make([]durableTask, 0, len(paths))
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		kind := "upload"
		if folder {
			kind = "scan_upload"
		}
		tasks = append(tasks, durableTask{Kind: kind, Name: taskName(path), Payload: durablePayload{Path: absolute, Parent: protocol.ID(parent)}})
	}
	name := "Selected files"
	if len(paths) == 1 {
		name = taskName(paths[0])
	}
	_, err = m.durable.store.create(m.base(), scope, name, tasks)
	m.durable.notify()
	return err
}
func (m *Manager) queueDownload(entry api.CatalogEntry, path, policy string) error {
	if m.durableErr != nil {
		return m.durableErr
	}
	scope, _, err := m.durableScope()
	if err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return err
	}
	path = filepath.Join(root, filepath.Base(path))
	kind := "download"
	if entry.Kind == api.Directory {
		kind = "scan_restore"
	}
	task := durableTask{Kind: kind, Name: entry.Standard.Name, Payload: durablePayload{Path: path, Root: root, Entry: entry, Policy: policy}}
	_, err = m.durable.store.create(m.base(), scope, entry.Standard.Name, []durableTask{task})
	m.durable.notify()
	return err
}
func (m *Manager) QueueAction(id int, batch bool, action string) error {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	if m.durable == nil {
		return errors.New("persistent queue is unavailable")
	}
	scope, _, err := m.durableScope()
	if err != nil {
		return err
	}
	q := m.durable
	q.mu.Lock()
	if err = q.store.control(m.base(), scope, id, batch, action); err != nil {
		q.mu.Unlock()
		return err
	}
	stopping := action == "pause" || action == "cancel"
	if stopping {
		for taskID, active := range q.active {
			if (batch && active.batch == id) || (!batch && taskID == id) {
				active.cancel()
			}
		}
	}
	q.mu.Unlock()
	q.notify()
	if stopping {
		deadline := time.Now().Add(15 * time.Second)
		for {
			q.mu.Lock()
			pending := false
			for taskID, active := range q.active {
				if (batch && active.batch == id) || (!batch && taskID == id) {
					pending = true
				}
			}
			q.mu.Unlock()
			if !pending {
				return nil
			}
			if time.Now().After(deadline) {
				return errors.New("transfer decision was saved; active requests are still stopping")
			}
			select {
			case <-m.base().Done():
				return m.base().Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	return nil
}
func (m *Manager) SetBandwidth(bytesPerSecond int64) error {
	if bytesPerSecond < 0 || bytesPerSecond > 1<<40 {
		return errors.New("bandwidth must be between zero and 1 TiB/s")
	}
	if m.durable == nil {
		return errors.New("persistent queue is unavailable")
	}
	q := m.durable
	if _, err := q.store.db.ExecContext(m.base(), `INSERT INTO transfer_preferences(key,value) VALUES('bandwidth',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, bytesPerSecond); err != nil {
		return err
	}
	q.rateMu.Lock()
	q.bandwidth = bytesPerSecond
	if q.rateChanged != nil {
		close(q.rateChanged)
	}
	q.rateChanged = make(chan struct{})
	q.nextByte = time.Time{}
	q.rateMu.Unlock()
	return nil
}
func (q *durableQueue) suspend() error {
	q.mu.Lock()
	for _, active := range q.active {
		active.cancel()
	}
	q.mu.Unlock()
	deadline := time.Now().Add(15 * time.Second)
	for {
		q.mu.Lock()
		n := len(q.active)
		q.mu.Unlock()
		if n == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("transfers are still stopping; try again")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Restore queues selected files/directories under an existing user-chosen root.
// Source entries come from the current bounded catalog page only.
func (m *Manager) Restore(ids []string, destination, policy string) error {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	if policy != "replace" && policy != "skip" && policy != "fail" {
		return errors.New("choose replace, skip or fail for existing files")
	}
	scope, _, err := m.durableScope()
	if err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(destination)
	if err != nil {
		return err
	}
	if err := ValidateRestorePath(root); err != nil {
		return err
	}
	tasks := make([]durableTask, 0, len(ids))
	m.mu.Lock()
	for _, id := range ids {
		entry, ok := m.catalog[id]
		if !ok {
			m.mu.Unlock()
			return errors.New("refresh the file list and select the entries again")
		}
		if err := client.ValidateRestoreName(entry.Standard.Name); err != nil {
			m.mu.Unlock()
			return err
		}
		kind := "download"
		if entry.Kind == api.Directory {
			kind = "scan_restore"
		}
		tasks = append(tasks, durableTask{Kind: kind, Name: entry.Standard.Name, Payload: durablePayload{Path: filepath.Join(root, entry.Standard.Name), Root: root, Entry: entry, Policy: policy}})
	}
	m.mu.Unlock()
	if m.durable == nil {
		return errors.New("persistent queue is unavailable")
	}
	_, err = m.durable.store.create(m.base(), scope, "Restore selection", tasks)
	m.durable.notify()
	return err
}

var errContinue = errors.New("continue directory discovery")

func (m *Manager) Bandwidth() int64 {
	if m.durable == nil {
		return 0
	}
	q := m.durable
	q.rateMu.Lock()
	defer q.rateMu.Unlock()
	return q.bandwidth
}

// A failed local checkpoint keeps its worker and identity until the database
// can record the outcome. It must never silently strand an active task.
func (q *durableQueue) persist(write func() error) bool {
	for {
		err := write()
		q.mu.Lock()
		if err == nil {
			q.storageError = ""
		} else {
			q.storageError = err.Error()
		}
		q.mu.Unlock()
		if err == nil {
			return true
		}
		select {
		case <-q.manager.base().Done():
			return false
		case <-time.After(time.Second):
		}
	}
}
func (m *Manager) QueueHealth() string {
	if m.durableErr != nil {
		return m.durableErr.Error()
	}
	if m.durable == nil {
		return ""
	}
	q := m.durable
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.storageError
}

func (m *Manager) wakeAuthenticated(c *client.Client) {
	if m.durable == nil {
		return
	}
	account, ok := c.Account()
	if !ok {
		return
	}
	q := m.durable
	_, err := q.store.db.ExecContext(m.base(), `UPDATE transfer_tasks SET retry_at=0 WHERE state='waiting_authentication' AND batch_id IN (SELECT id FROM transfer_batches WHERE server_id=? AND account_id=?)`, account.ServerID, account.AccountID)
	if err != nil {
		q.mu.Lock()
		q.storageError = err.Error()
		q.mu.Unlock()
	}
	q.notify()
}
