package transfer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chironjit/serversend/shared/vault"
	"path/filepath"
	"strings"
	"time"

	"github.com/chironjit/serversend/apps/desktop/internal/database"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/sqlite"
)

type durablePayload struct {
	Private     bool
	Sealed      []byte
	SealedScope protocol.Scope
	Path        string
	Parent      protocol.ID
	Entry       api.CatalogEntry
	Cursor      string
	Policy      string
	Upload      *client.UploadCheckpoint
	// Root is the selected, canonical local destination, retained for every child.
	Root              string
	DownloadReady     bool
	CancelDisposition string
	CancelReason      string
}
type durableTask struct {
	secret                   vault.Key
	Scope                    protocol.Scope
	ID, BatchID              int
	Kind, Name, State, Error string
	Payload                  durablePayload
	Done, Total, RetryAt     int64
	Attempts                 int
}
type durableStore struct {
	dir string
	db  *sql.DB
	key func(protocol.Scope) (vault.Key, error)
}

func openDurable(ctx context.Context, dir string) (*durableStore, error) {
	db, err := database.Open(ctx, dir)
	if err != nil {
		return nil, err
	}
	// A process exit leaves only active work ambiguous; paused/cancelled jobs
	// and completed tasks retain their exact decisions.
	if _, err = db.ExecContext(ctx, `UPDATE transfer_tasks SET state=CASE WHEN state='cancelling' THEN 'cancel_pending' ELSE 'queued' END WHERE state IN ('active','cancelling')`); err != nil {
		db.Close()
		return nil, err
	}
	return &durableStore{db: db, dir: dir}, nil
}
func (s *durableStore) create(ctx context.Context, scope protocol.Scope, name string, tasks []durableTask) (int, error) {
	if err := scope.Validate(); err != nil {
		return 0, err
	}
	for _, t := range tasks {
		if t.Payload.Private {
			name = "Private batch"
			break
		}
	}
	var id int64
	err := sqlite.Write(ctx, s.db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO transfer_batches(server_id,account_id,name,created_at) VALUES(?,?,?,?)`, scope.ServerID, scope.AccountID, name, time.Now().UnixMilli())
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		if err != nil {
			return err
		}
		for _, task := range tasks {
			task.BatchID = int(id)
			task.Scope = scope
			if err := s.insertTask(ctx, tx, task); err != nil {
				return err
			}
		}
		return nil
	})
	return int(id), err
}
func (s *durableStore) insertTask(ctx context.Context, tx *sql.Tx, t durableTask) error {
	payload, err := s.encode(t.Payload)
	if err != nil {
		return err
	}
	key := t.Payload.Path
	if t.Payload.Private {
		key = fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
		t.Name = "Private item"
	}
	var target any
	if t.Kind == "download" || t.Kind == "scan_restore" {
		key = string(t.Payload.Entry.ID)
		var prior []byte
		e := tx.QueryRowContext(ctx, `SELECT payload FROM transfer_tasks WHERE batch_id=? AND source_key=?`, t.BatchID, key).Scan(&prior)
		if e == nil {
			var previous durablePayload
			if err := s.decode(prior, &previous); err != nil {
				return err
			}
			if previous.Path != t.Payload.Path || previous.Entry.Kind != t.Payload.Entry.Kind || previous.Entry.HeadVersionID != t.Payload.Entry.HeadVersionID {
				return errors.New("catalog changed during restore discovery; completed files were retained, start a fresh restore selection")
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		canonical, err := CanonicalRestorePath(t.Payload.Path)
		if err != nil {
			return err
		}
		target = canonical
		if t.Payload.Private {
			target = fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
		}
		var existing string
		err = tx.QueryRowContext(ctx, `SELECT source_key FROM transfer_tasks WHERE batch_id=? AND target_key=?`, t.BatchID, target).Scan(&existing)
		if err == nil && existing != key {
			return errors.New("restore paths collide after Unicode/case normalization")
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	state := t.State
	if state == "" {
		state = "queued"
	}
	if t.Payload.Private && t.Error != "" {
		t.Error = "Private source could not be read"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO transfer_tasks(batch_id,source_key,target_key,kind,name,state,payload,total,error) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(batch_id,source_key) DO NOTHING`, t.BatchID, key, target, t.Kind, t.Name, state, payload, t.Total, t.Error)
	return err
}

const taskColumns = `t.id,t.batch_id,t.kind,t.name,t.state,t.payload,t.done,t.total,t.error,t.retry_at,t.attempts`

func (s *durableStore) readTask(row interface{ Scan(...any) error }) (durableTask, error) {
	var t durableTask
	var payload []byte
	err := row.Scan(&t.ID, &t.BatchID, &t.Kind, &t.Name, &t.State, &payload, &t.Done, &t.Total, &t.Error, &t.RetryAt, &t.Attempts)
	if err == nil {
		err = s.decode(payload, &t.Payload, &t.secret)
		if errors.Is(err, vault.ErrLocked) {
			t.Payload = durablePayload{Private: true}
			err = nil
		}
	}
	return t, err
}
func (s *durableStore) claim(ctx context.Context, scope protocol.Scope, exclude ...int) (durableTask, error) {
	return s.claimWithPrivate(ctx, scope, true, exclude...)
}
func (s *durableStore) claimWithPrivate(ctx context.Context, scope protocol.Scope, allowPrivate bool, exclude ...int) (durableTask, error) {
	var task durableTask
	err := sqlite.Write(ctx, s.db, func(tx *sql.Tx) error {
		var err error
		filter := ""
		if !allowPrivate {
			filter = " AND coalesce(json_extract(t.payload,'$.Private'),0)=0"
		}
		if s.key != nil {
			if _, e := s.key(scope); e != nil {
				filter = " AND coalesce(json_extract(t.payload,'$.Private'),0)=0"
			}
		}
		args := []any{scope.ServerID, scope.AccountID, time.Now().UnixMilli(), time.Now().UnixMilli()}
		if len(exclude) > 0 {
			filter += " AND t.id NOT IN (" + strings.TrimSuffix(strings.Repeat("?,", len(exclude)), ",") + ")"
			for _, id := range exclude {
				args = append(args, id)
			}
		}
		task, err = s.readTask(tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM transfer_tasks t JOIN transfer_batches b ON b.id=t.batch_id WHERE b.server_id=? AND b.account_id=? AND ((b.state NOT IN ('paused','cancelled') AND t.state IN ('queued','waiting_network','waiting_capacity','waiting_authentication','retry_scheduled','waiting_vault') AND t.retry_at<=?) OR (t.state='cancel_pending' AND t.retry_at<=?))`+filter+` ORDER BY b.served_at,t.id LIMIT 1`, args...))
		if err != nil {
			return err
		}
		if task.Payload.Private && task.Payload.Path == "" {
			return vault.ErrLocked
		}
		// Keep cancellation intent distinct while its server reservation is released.
		if task.State != "cancel_pending" {
			_, err = tx.ExecContext(ctx, `UPDATE transfer_tasks SET state='active' WHERE id=?`, task.ID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE transfer_tasks SET state='cancelling' WHERE id=?`, task.ID)
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE transfer_batches SET served_at=? WHERE id=?`, time.Now().UnixNano(), task.BatchID)
		return err
	})
	task.Scope = scope
	return task, err
}
func (s *durableStore) save(ctx context.Context, t durableTask) error {
	var payload []byte
	var err error
	if t.Payload.Private && t.secret != (vault.Key{}) {
		raw, e := json.Marshal(t.Payload)
		if e != nil {
			return e
		}
		payload, err = json.Marshal(durablePayload{Private: true, SealedScope: t.Payload.SealedScope, Sealed: vault.Seal(t.secret, raw, []byte("serversend-private-queue-v1"))})
		clear(raw)
	} else {
		payload, err = s.encode(t.Payload)
	}
	if err != nil {
		return err
	}
	// Checkpoints are saved even after pause/cancel; outcomes only replace an
	// active state, so a late response cannot undo a persisted user decision.
	_, err = s.db.ExecContext(ctx, `UPDATE transfer_tasks SET payload=?,done=?,total=? WHERE id=?`, payload, t.Done, t.Total, t.ID)
	return err
}

const privateAttention = "Private transfer needs attention; unlock and retry"

// privateSafe reports whether a private task may keep its message: skip
// reasons and the staging capacity messages are fixed text without paths.
func privateSafe(state, msg string) bool {
	return msg == "" || state == "skipped" || msg == privateBudgetFull || msg == privateDiskFull
}

func (s *durableStore) finish(ctx context.Context, t durableTask, state, msg string, retry int64) error {
	if t.Payload.Private && !privateSafe(state, msg) {
		msg = privateAttention
	}
	_, err := s.db.ExecContext(ctx, `UPDATE transfer_tasks SET state=?,error=?,retry_at=?,attempts=attempts+1,done=?,total=? WHERE id=? AND state='active'`, state, msg, retry, t.Done, t.Total, t.ID)
	return err
}

// unencrypted requeues skipped private files that exceed the private size
// limit as standard uploads to the same folder. The user chose this; their
// names and contents are then stored unencrypted on the server.
func (s *durableStore) unencrypted(ctx context.Context, tx *sql.Tx, filter string, args []any) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,payload FROM transfer_tasks WHERE `+filter+` AND state='skipped' AND error=?`, append(args, client.PrivateTooLargeReason)...)
	if err != nil {
		return err
	}
	type task struct {
		id  int
		raw []byte
	}
	var tasks []task
	for rows.Next() {
		var t task
		if err := rows.Scan(&t.id, &t.raw); err != nil {
			rows.Close()
			return err
		}
		tasks = append(tasks, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(tasks) == 0 {
		return errors.New("no skipped private files over the size limit to upload unencrypted")
	}
	for _, t := range tasks {
		var p durablePayload
		if err := s.decode(t.raw, &p); err != nil {
			return err
		}
		p = durablePayload{Path: p.Path, Parent: p.Parent, Root: p.Root}
		payload, err := s.encode(p)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE transfer_tasks SET payload=?,name=?,state='queued',error='',retry_at=0,done=0,total=0 WHERE id=?`, payload, taskName(p.Path), t.id); err != nil {
			return err
		}
	}
	return nil
}
func (s *durableStore) children(ctx context.Context, parent durableTask, children []durableTask) error {
	return sqlite.Write(ctx, s.db, func(tx *sql.Tx) error {
		var state string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM transfer_tasks WHERE id=?`, parent.ID).Scan(&state); err != nil {
			return err
		}
		if state != "active" {
			return context.Canceled
		}
		for _, t := range children {
			t.BatchID = parent.BatchID
			if err := s.insertTask(ctx, tx, t); err != nil {
				return err
			}
		}
		payload, err := s.encode(parent.Payload)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE transfer_tasks SET payload=? WHERE id=?`, payload, parent.ID)
		return err
	})
}
func (s *durableStore) items(ctx context.Context, scope protocol.Scope) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+taskColumns+` FROM transfer_tasks t JOIN transfer_batches b ON b.id=t.batch_id WHERE b.server_id=? AND b.account_id=? ORDER BY (t.state IN ('active','cancelling')) DESC,t.id DESC LIMIT 200`, scope.ServerID, scope.AccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		t, err := s.readTask(rows)
		if err != nil {
			return nil, err
		}
		t.Scope = scope
		items = append(items, t.item())
		clear(t.secret[:])
	}
	return items, rows.Err()
}
func (t durableTask) item() Item {
	direction := "upload"
	if t.Kind == "download" || t.Kind == "scan_restore" {
		direction = "download"
	}
	return Item{Scope: string(t.Scope.ServerID) + "/" + string(t.Scope.AccountID), ID: t.ID, BatchID: t.BatchID, Name: t.Name, Direction: direction, State: t.State, Done: t.Done, Total: t.Total, Error: t.Error}
}
func (s *durableStore) control(ctx context.Context, scope protocol.Scope, id int, batch bool, action string) error {
	return sqlite.Write(ctx, s.db, func(tx *sql.Tx) error {
		filter := `id=? AND batch_id IN (SELECT id FROM transfer_batches WHERE server_id=? AND account_id=?)`
		if batch {
			filter = `batch_id=? AND batch_id IN (SELECT id FROM transfer_batches WHERE server_id=? AND account_id=?)`
		}
		args := []any{id, scope.ServerID, scope.AccountID}
		var matches int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM transfer_tasks WHERE `+filter, args...).Scan(&matches); err != nil {
			return err
		}
		if matches == 0 {
			return errors.New("transfer not found for the signed-in account")
		}
		switch action {
		case "pause":
			_, err := tx.ExecContext(ctx, `UPDATE transfer_tasks SET state='paused' WHERE `+filter+` AND state NOT IN ('done','skipped','cancelled','failed','cancel_pending','cancelling')`, args...)
			if err != nil {
				return err
			}
		case "resume":
			_, err := tx.ExecContext(ctx, `UPDATE transfer_tasks SET state='queued',error='',retry_at=0 WHERE `+filter+` AND state IN ('paused','waiting_network','waiting_authentication','waiting_capacity','retry_scheduled','waiting_vault')`, args...)
			if err != nil {
				return err
			}
		case "retry":
			_, err := tx.ExecContext(ctx, `UPDATE transfer_tasks SET state='queued',error='',retry_at=0 WHERE `+filter+` AND state IN ('failed','cancelled')`, args...)
			if err != nil {
				return err
			}
		case "cancel":
			_, err := tx.ExecContext(ctx, `UPDATE transfer_tasks SET state='cancel_pending',retry_at=0 WHERE `+filter+` AND state NOT IN ('done','skipped','cancelled','cancelling')`, args...)
			if err != nil {
				return err
			}
		case "unencrypted":
			if err := s.unencrypted(ctx, tx, filter, args); err != nil {
				return err
			}
		case "keep_skipped":
			result, err := tx.ExecContext(ctx, `UPDATE transfer_tasks SET error=? WHERE `+filter+` AND state='skipped' AND error=?`, append([]any{client.PrivateTooLargeKeptReason}, append(args, client.PrivateTooLargeReason)...)...)
			if err != nil {
				return err
			}
			if n, _ := result.RowsAffected(); n == 0 {
				return errors.New("no skipped private files over the size limit to keep skipped")
			}
		default:
			return errors.New("unknown queue action")
		}
		if !batch && action == "unencrypted" {
			if _, err := tx.ExecContext(ctx, `UPDATE transfer_batches SET finished_at=0 WHERE id=(SELECT batch_id FROM transfer_tasks WHERE id=?)`, id); err != nil {
				return err
			}
		}
		if !batch && action == "resume" {
			if _, err := tx.ExecContext(ctx, `UPDATE transfer_batches SET state='queued',finished_at=0 WHERE id=(SELECT batch_id FROM transfer_tasks WHERE id=?) AND server_id=? AND account_id=? AND state='paused'`, id, scope.ServerID, scope.AccountID); err != nil {
				return err
			}
		}
		if batch && action != "keep_skipped" {
			state := "queued"
			if action == "pause" {
				state = "paused"
			}
			if action == "cancel" {
				state = "cancelled"
			}
			_, err := tx.ExecContext(ctx, `UPDATE transfer_batches SET state=?,finished_at=0 WHERE id=? AND server_id=? AND account_id=?`, state, id, scope.ServerID, scope.AccountID)
			return err
		}
		return nil
	})
}
func (s *durableStore) batches(ctx context.Context, scope protocol.Scope) ([]Batch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,state,created_at,finished_at FROM transfer_batches WHERE server_id=? AND account_id=? ORDER BY id DESC LIMIT 100`, scope.ServerID, scope.AccountID)
	if err != nil {
		return nil, err
	}
	batches := []Batch{}
	for rows.Next() {
		var b Batch
		if err := rows.Scan(&b.ID, &b.Name, &b.State, &b.StartedAt, &b.FinishedAt); err != nil {
			rows.Close()
			return nil, err
		}
		b.SkippedItems = []SkippedItem{}
		batches = append(batches, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range batches {
		b := &batches[i]
		rs, err := s.db.QueryContext(ctx, `SELECT kind,state,count(*),coalesce(sum(total),0),max(error) FROM transfer_tasks WHERE batch_id=? GROUP BY kind,state`, b.ID)
		if err != nil {
			return nil, err
		}
		pending := 0
		scanning := false
		runnable := false
		waiting := "paused"
		for rs.Next() {
			var kind, state, msg string
			var n int
			var bytes int64
			if err := rs.Scan(&kind, &state, &n, &bytes, &msg); err != nil {
				rs.Close()
				return nil, err
			}
			isScan := kind == "scan_upload" || kind == "scan_restore"
			if !isScan {
				b.Files += n
			}
			switch state {
			case "done":
				if isScan {
					b.Directories += n
				} else {
					b.Completed += n
					b.Bytes += bytes
					if msg != "" && b.Error == "" {
						b.Error = msg // completed with a warning; failures take precedence
					}
				}
			case "skipped":
				b.Skipped += n
			case "failed":
				b.Failed += n
				b.Error = msg
			case "cancelled":
				b.Cancelled += n
			default:
				pending += n
				if state == "queued" || state == "active" {
					runnable = true
				} else if state != "paused" {
					waiting = state
				}
				if isScan {
					scanning = true
				}
			}
		}
		err = rs.Err()
		rs.Close()
		if err != nil {
			return nil, err
		}
		if pending == 0 && b.FinishedAt == 0 {
			b.FinishedAt = time.Now().UnixMilli()
			_, err = s.db.ExecContext(ctx, `UPDATE transfer_batches SET finished_at=? WHERE id=?`, b.FinishedAt, b.ID)
			if err != nil {
				return nil, err
			}
		}
		if b.State == "cancelled" && pending > 0 {
			b.State = "cancel_pending"
		}
		if b.State != "paused" && b.State != "cancelled" && b.State != "cancel_pending" {
			b.State = "running"
			if scanning {
				b.State = "scanning"
			}
			if pending > 0 && !runnable {
				b.State = waiting
			}
			if pending == 0 {
				b.State = "done"
				if b.Failed > 0 {
					b.State = "failed"
					if b.Completed > 0 || b.Directories > 0 {
						b.State = "partial"
					}
				}
				if b.Skipped > 0 && b.Failed == 0 {
					b.State = "done_with_skips"
				}
				if b.Completed == 0 && b.Directories == 0 && b.Skipped > 0 && b.Failed == 0 {
					b.State = "all_skipped"
				}
			}
		}
		if b.Skipped > 0 {
			err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM transfer_tasks WHERE batch_id=? AND state='skipped' AND error=?`, b.ID, client.PrivateTooLargeReason).Scan(&b.Oversized)
			if err != nil {
				return nil, err
			}
		}
		details, err := s.db.QueryContext(ctx, `SELECT id,payload,error FROM transfer_tasks WHERE batch_id=? AND state='skipped' ORDER BY id LIMIT 100`, b.ID)
		if err != nil {
			return nil, err
		}
		for details.Next() {
			var id int
			var raw []byte
			var reason string
			if err := details.Scan(&id, &raw, &reason); err != nil {
				details.Close()
				return nil, err
			}
			var p durablePayload
			if err := s.decode(raw, &p); errors.Is(err, vault.ErrLocked) {
				p.Path = "Private item"
			} else if err != nil {
				details.Close()
				return nil, err
			}
			b.SkippedItems = append(b.SkippedItems, SkippedItem{Path: p.Path, Reason: reason, ID: id, Unencrypted: reason == client.PrivateTooLargeReason})
		}
		err = details.Err()
		details.Close()
		if err != nil {
			return nil, err
		}
	}
	return batches, nil
}
func taskName(path string) string { return filepath.Base(path) }

func (s *durableStore) encode(p durablePayload) ([]byte, error) {
	if !p.Private {
		return json.Marshal(p)
	}
	if s.key == nil {
		return nil, vault.ErrLocked
	}
	k, err := s.key(p.SealedScope)
	if err != nil {
		return nil, err
	}
	defer clear(k[:])
	p.Sealed = nil
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	return json.Marshal(durablePayload{Private: true, SealedScope: p.SealedScope, Sealed: vault.Seal(k, raw, []byte("serversend-private-queue-v1"))})
}
func (s *durableStore) decode(raw []byte, p *durablePayload, keys ...*vault.Key) error {
	if err := json.Unmarshal(raw, p); err != nil {
		return err
	}
	if !p.Private {
		return nil
	}
	if s.key == nil {
		return vault.ErrLocked
	}
	k, err := s.key(p.SealedScope)
	if err != nil {
		return err
	}
	defer clear(k[:])
	plain, err := vault.Open(k, p.Sealed, []byte("serversend-private-queue-v1"))
	if err != nil {
		return err
	}
	defer clear(plain)
	err = json.Unmarshal(plain, p)
	if err == nil && len(keys) > 0 {
		*keys[0] = k
	}
	return err
}
