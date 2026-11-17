CREATE TABLE transfer_batches (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 server_id TEXT NOT NULL CHECK(length(server_id)=32 AND server_id NOT GLOB '*[^0-9a-f]*'),
 account_id TEXT NOT NULL CHECK(length(account_id)=32 AND account_id NOT GLOB '*[^0-9a-f]*'),
 name TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','active','paused','waiting_network','waiting_authentication','waiting_capacity','retry_scheduled','done','skipped','failed','cancelled','cancel_pending','cancelling')),
 created_at INTEGER NOT NULL CHECK(created_at>=0),
 finished_at INTEGER NOT NULL DEFAULT 0 CHECK(finished_at>=0),
 served_at INTEGER NOT NULL DEFAULT 0
) STRICT;
CREATE INDEX transfer_batches_scope ON transfer_batches(server_id,account_id,id);
CREATE TABLE transfer_tasks (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 batch_id INTEGER NOT NULL REFERENCES transfer_batches(id),
 source_key TEXT NOT NULL,
 target_key TEXT,
 kind TEXT NOT NULL CHECK(kind IN ('upload','scan_upload','download','scan_restore')),
 name TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','active','paused','waiting_network','waiting_authentication','waiting_capacity','retry_scheduled','done','skipped','failed','cancelled','cancel_pending','cancelling')),
 payload BLOB NOT NULL,
 done INTEGER NOT NULL DEFAULT 0 CHECK(done>=0),
 total INTEGER NOT NULL DEFAULT 0 CHECK(total>=0),
 error TEXT NOT NULL DEFAULT '',
 retry_at INTEGER NOT NULL DEFAULT 0 CHECK(retry_at>=0),
 attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts>=0),
 UNIQUE(batch_id,source_key),
 UNIQUE(batch_id,target_key)
) STRICT;
CREATE INDEX transfer_tasks_ready ON transfer_tasks(state,retry_at,batch_id,id);
CREATE INDEX transfer_tasks_batch ON transfer_tasks(batch_id,state,id);

CREATE TABLE transfer_preferences (key TEXT PRIMARY KEY,value INTEGER NOT NULL) STRICT;
