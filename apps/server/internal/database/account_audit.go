package database

import (
	"context"
	"time"

	"github.com/chironjit/serversend/shared/api"
)

const auditPageLimit = 50

func (r *Repository) AccountAudit(ctx context.Context, before int64) (api.AccountAuditPage, error) {
	query := `SELECT e.id,coalesce(actor.username,e.actor_id,''),coalesce(target.username,e.target_id),
		e.action,coalesce(e.device_id,''),e.created_at FROM audit_events e
		LEFT JOIN accounts actor ON actor.id=e.actor_id
		LEFT JOIN accounts target ON target.id=e.target_id`
	var args []any
	if before > 0 {
		query += ` WHERE e.id<?`
		args = append(args, before)
	}
	query += ` ORDER BY e.id DESC LIMIT ?`
	rows, err := r.db.QueryContext(ctx, query, append(args, auditPageLimit+1)...)
	if err != nil {
		return api.AccountAuditPage{}, err
	}
	defer rows.Close()
	page := api.AccountAuditPage{Events: make([]api.AccountAuditEvent, 0, auditPageLimit)}
	for rows.Next() {
		if len(page.Events) == auditPageLimit {
			page.NextBefore = page.Events[auditPageLimit-1].ID
			break
		}
		var event api.AccountAuditEvent
		var created int64
		if err := rows.Scan(&event.ID, &event.Actor, &event.Target, &event.Action, &event.DeviceID, &created); err != nil {
			return api.AccountAuditPage{}, err
		}
		event.CreatedAt = time.UnixMilli(created).UTC()
		page.Events = append(page.Events, event)
	}
	return page, rows.Err()
}
