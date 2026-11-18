package database

import (
	"context"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

// StorageStatistics reads related counters in one SQLite snapshot. Empty scope
// is reserved for the server administrator, enforced by the HTTP layer.
func (r *Repository) StorageStatistics(ctx context.Context, account protocol.ID) (s api.StorageStatistics, err error) {
	err = r.db.QueryRowContext(ctx, `SELECT
 (SELECT coalesce(sum(total_bytes),0) FROM versions WHERE (?1='' OR account_id=?1) AND json_extract(CAST(manifest AS TEXT),'$.mode')='standard'),
 (SELECT coalesce(sum(total_bytes),0) FROM versions WHERE (?1='' OR account_id=?1) AND json_extract(CAST(manifest AS TEXT),'$.mode')='private'),
 (SELECT coalesce(sum(bytes),0) FROM reservations WHERE (?1='' OR account_id=?1)),
 (SELECT coalesce(sum(byte_count),0) FROM chunks c JOIN reservations r ON r.account_id=c.account_id AND r.upload_id=c.upload_id WHERE (?1='' OR c.account_id=?1)),
 (SELECT count(*) FROM uploads WHERE state='open' AND (?1='' OR account_id=?1))`, account).Scan(&s.LogicalBytes, &s.PrivateCiphertextBytes, &s.ReservedBytes, &s.UnwrittenBytes, &s.ActiveUploads)
	s.UnwrittenBytes = max(0, s.ReservedBytes-s.UnwrittenBytes)
	return
}
func (r *Repository) EachOpenUpload(ctx context.Context, account protocol.ID, visit func(protocol.ID) error) error {
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM uploads WHERE state='open' AND (?1='' OR account_id=?1)`, account)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id protocol.ID
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if err := visit(id); err != nil {
			return err
		}
	}
	return rows.Err()
}
