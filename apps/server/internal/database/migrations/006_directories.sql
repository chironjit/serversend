CREATE TABLE directories (
 account_id TEXT NOT NULL REFERENCES accounts(id), id TEXT NOT NULL,
 parent_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL, revision INTEGER NOT NULL,
 PRIMARY KEY(account_id,id), UNIQUE(account_id,parent_id,name)
) STRICT;
ALTER TABLE uploads ADD COLUMN parent_id TEXT NOT NULL DEFAULT '';
ALTER TABLE files ADD COLUMN parent_id TEXT NOT NULL DEFAULT '';
CREATE INDEX files_parent ON files(account_id,parent_id,id);
