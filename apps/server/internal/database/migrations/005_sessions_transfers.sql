-- Vertical-slice foundations for device sessions, the standard upload
-- lifecycle and a flat file catalog. Only token hashes are stored.
CREATE TABLE device_sessions (
    id TEXT PRIMARY KEY NOT NULL CHECK (length(id)=32 AND id NOT GLOB '*[^0-9a-f]*'),
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    token_hash TEXT NOT NULL UNIQUE CHECK (length(token_hash)=64 AND token_hash NOT GLOB '*[^0-9a-f]*'),
    auth_epoch INTEGER NOT NULL CHECK (auth_epoch>=0),
    created_at INTEGER NOT NULL CHECK (created_at>=0),
    expires_at INTEGER NOT NULL CHECK (expires_at>=0)
) STRICT;
CREATE INDEX device_sessions_by_account ON device_sessions(account_id);

ALTER TABLE accounts ADD COLUMN catalog_revision INTEGER NOT NULL DEFAULT 0 CHECK (catalog_revision>=0);

-- Upload intent recorded at creation; NULL only on rows from earlier schemas.
ALTER TABLE uploads ADD COLUMN file_id TEXT CHECK (file_id IS NULL OR length(file_id)=32);
ALTER TABLE uploads ADD COLUMN version_id TEXT CHECK (version_id IS NULL OR length(version_id)=32);
ALTER TABLE uploads ADD COLUMN chunk_count INTEGER CHECK (chunk_count IS NULL OR chunk_count>=0);
ALTER TABLE uploads ADD COLUMN manifest_bytes INTEGER CHECK (manifest_bytes IS NULL OR manifest_bytes>0);
ALTER TABLE uploads ADD COLUMN expected_head INTEGER CHECK (expected_head IS NULL OR expected_head>=0);
ALTER TABLE uploads ADD COLUMN expires_at INTEGER CHECK (expires_at IS NULL OR expires_at>=0);
ALTER TABLE uploads ADD COLUMN result_catalog_revision INTEGER;
ALTER TABLE uploads ADD COLUMN result_head_revision INTEGER;

CREATE TABLE files (
    account_id TEXT NOT NULL REFERENCES accounts(id),
    id TEXT NOT NULL CHECK (length(id)=32 AND id NOT GLOB '*[^0-9a-f]*'),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
    head_version_id TEXT NOT NULL CHECK (length(head_version_id)=32),
    head_revision INTEGER NOT NULL CHECK (head_revision>0),
    PRIMARY KEY (account_id,id)
) STRICT;

-- A version is immutable: the exact manifest bytes plus the upload whose
-- chunk rows name its durable objects.
CREATE TABLE versions (
    account_id TEXT NOT NULL,
    file_id TEXT NOT NULL CHECK (length(file_id)=32 AND file_id NOT GLOB '*[^0-9a-f]*'),
    id TEXT NOT NULL CHECK (length(id)=32 AND id NOT GLOB '*[^0-9a-f]*'),
    upload_id TEXT NOT NULL,
    manifest BLOB NOT NULL CHECK (typeof(manifest)='blob' AND length(manifest)>0),
    total_bytes INTEGER NOT NULL CHECK (total_bytes>=0),
    created_at INTEGER NOT NULL CHECK (created_at>=0),
    PRIMARY KEY (account_id,file_id,id),
    FOREIGN KEY (account_id,upload_id) REFERENCES uploads(account_id,id)
) STRICT;
