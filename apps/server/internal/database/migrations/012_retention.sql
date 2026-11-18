CREATE TABLE trash_entries (
 account_id TEXT NOT NULL, id TEXT NOT NULL, parent_id TEXT NOT NULL, name TEXT NOT NULL,
 directory INTEGER NOT NULL, deleted_at INTEGER NOT NULL,
 PRIMARY KEY(account_id,id)
) STRICT;
CREATE INDEX trash_expiry ON trash_entries(deleted_at);
CREATE TABLE retention_policy (id INTEGER PRIMARY KEY CHECK(id=1), trash_days INTEGER NOT NULL CHECK(trash_days BETWEEN 1 AND 36500), version_days INTEGER NOT NULL CHECK(version_days BETWEEN 0 AND 36500)) STRICT;
INSERT INTO retention_policy VALUES(1,30,0);
CREATE TABLE storage_audit (id INTEGER PRIMARY KEY, account_id TEXT NOT NULL, kind TEXT NOT NULL, created_at INTEGER NOT NULL) STRICT;
ALTER TABLE versions ADD COLUMN resolved INTEGER NOT NULL DEFAULT 0 CHECK(resolved IN (0,1));
-- Make earlier same-name displacements recoverable through the new trash UI.
INSERT INTO trash_entries(account_id,id,parent_id,name,directory,deleted_at)
 SELECT account_id,id,parent_id,name,0,CAST(strftime('%s','now') AS INTEGER)*1000 FROM files WHERE hidden=1;
INSERT INTO storage_journal(kind,source,target)
 SELECT 'move','.serversend/versions/'||account_id||'/'||id||'/'||head_version_id,
 '.serversend/recoverable/'||account_id||'/'||id||'/'||name FROM files WHERE hidden=1;
UPDATE files SET parent_id=id,hidden=0 WHERE hidden=1;
