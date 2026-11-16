package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

var ErrJobConflict = errors.New("job is absent or its state has changed")

type Job struct {
	Key       protocol.ResourceKey
	State     protocol.QueueState
	Mode      string
	CreatedAt time.Time
}

func (r *Repository) CreateJob(ctx context.Context, job Job) error {
	if err := job.Key.Validate(); err != nil {
		return err
	}
	if job.State != protocol.Queued || job.CreatedAt.IsZero() || job.CreatedAt.UnixMilli() < 0 {
		return errors.New("new jobs must be queued with a creation time")
	}
	return r.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO jobs(server_id,account_id,id,state,mode,created_at) VALUES(?,?,?,?,?,?)`, job.Key.ServerID, job.Key.AccountID, job.Key.ID, job.State, job.Mode, job.CreatedAt.UnixMilli())
		return err
	})
}

// ListJobs uses bounded keyset pagination. An empty after value starts a page;
// results are ordered by opaque ID, not creation time. No whole-batch loading.
func (r *Repository) ListJobs(ctx context.Context, scope protocol.Scope, after protocol.ID, limit int) ([]Job, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if after != "" {
		if err := after.Validate(); err != nil {
			return nil, err
		}
	}
	if limit < 1 || limit > 1000 {
		return nil, errors.New("job page size must be between 1 and 1000")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,state,mode,created_at FROM jobs WHERE server_id=? AND account_id=? AND id>? ORDER BY id LIMIT ?`, scope.ServerID, scope.AccountID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]Job, 0, limit)
	for rows.Next() {
		job := Job{Key: protocol.ResourceKey{Scope: scope}}
		var millis int64
		if err := rows.Scan(&job.Key.ID, &job.State, &job.Mode, &millis); err != nil {
			return nil, err
		}
		job.CreatedAt = time.UnixMilli(millis).UTC()
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// TransitionJob checks the shared state machine and atomically compares the
// previous state. Scheduling, retries and crash reconciliation belong to queue.
func (r *Repository) TransitionJob(ctx context.Context, key protocol.ResourceKey, from, to protocol.QueueState) error {
	if err := key.Validate(); err != nil {
		return err
	}
	if err := protocol.ValidateQueueTransition(from, to); err != nil {
		return err
	}
	return r.write(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE jobs SET state=? WHERE server_id=? AND account_id=? AND id=? AND state=?`, to, key.ServerID, key.AccountID, key.ID, from)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrJobConflict
		}
		return nil
	})
}
