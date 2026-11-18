ALTER TABLE transfer_tasks RENAME TO transfer_tasks_v2;
CREATE TABLE transfer_tasks (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 batch_id INTEGER NOT NULL REFERENCES transfer_batches(id),
 source_key TEXT NOT NULL,
 target_key TEXT,
 kind TEXT NOT NULL CHECK(kind IN ('upload','scan_upload','download','scan_restore')),
 name TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','active','paused','waiting_network','waiting_authentication','waiting_capacity','waiting_vault','retry_scheduled','done','skipped','failed','cancelled','cancel_pending','cancelling')),
 payload BLOB NOT NULL,
 done INTEGER NOT NULL DEFAULT 0 CHECK(done>=0),
 total INTEGER NOT NULL DEFAULT 0 CHECK(total>=0),
 error TEXT NOT NULL DEFAULT '',
 retry_at INTEGER NOT NULL DEFAULT 0 CHECK(retry_at>=0),
 attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts>=0),
 UNIQUE(batch_id,source_key),
 UNIQUE(batch_id,target_key)
) STRICT;
INSERT INTO transfer_tasks SELECT * FROM transfer_tasks_v2;
DROP TABLE transfer_tasks_v2;
CREATE INDEX transfer_tasks_ready ON transfer_tasks(state,retry_at,batch_id,id);
CREATE INDEX transfer_tasks_batch ON transfer_tasks(batch_id,state,id);
