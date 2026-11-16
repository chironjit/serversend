-- Profiles here are already paired. Unpaired UI drafts are not trusted profiles.
CREATE TABLE profiles (
    server_id TEXT NOT NULL CHECK (length(server_id)=32 AND server_id NOT GLOB '*[^0-9a-f]*'),
    account_id TEXT NOT NULL CHECK (length(account_id)=32 AND account_id NOT GLOB '*[^0-9a-f]*'),
    display_name TEXT NOT NULL CHECK (length(display_name) BETWEEN 1 AND 128),
    identity_key BLOB NOT NULL CHECK (typeof(identity_key)='blob' AND length(identity_key)=32),
    catalog_revision TEXT NOT NULL DEFAULT '0' CHECK (
        length(catalog_revision) BETWEEN 1 AND 20 AND catalog_revision NOT GLOB '*[^0-9]*'
        AND (catalog_revision='0' OR substr(catalog_revision,1,1)!='0')
        AND (length(catalog_revision)<20 OR catalog_revision<='18446744073709551615')),
    PRIMARY KEY (server_id,account_id)
) STRICT;
CREATE TABLE endpoints (
    server_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    origin TEXT NOT NULL,
    PRIMARY KEY (server_id,account_id,origin),
    FOREIGN KEY (server_id,account_id) REFERENCES profiles(server_id,account_id) ON DELETE CASCADE
) STRICT;
CREATE TABLE jobs (
    server_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    id TEXT NOT NULL CHECK (length(id)=32 AND id NOT GLOB '*[^0-9a-f]*'),
    state TEXT NOT NULL CHECK (state IN ('queued','scanning','running','paused','waiting_network','waiting_authentication','waiting_vault','waiting_capacity','retry_scheduled','complete','partial','cancelled','failed')),
    mode TEXT NOT NULL CHECK (mode IN ('standard','private')),
    created_at INTEGER NOT NULL CHECK (created_at>=0),
    retry_at INTEGER CHECK (retry_at>=0),
    PRIMARY KEY (server_id,account_id,id),
    FOREIGN KEY (server_id,account_id) REFERENCES profiles(server_id,account_id)
) STRICT;
CREATE INDEX jobs_by_state ON jobs(server_id,account_id,state,id);
-- Paths/fingerprints are local source metadata. Private remote metadata caches
-- must contain ciphertext only; the encrypted-index implementation is chunk 20.
CREATE TABLE file_tasks (
    server_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    job_id TEXT NOT NULL,
    id TEXT NOT NULL CHECK (length(id)=32 AND id NOT GLOB '*[^0-9a-f]*'),
    source_path TEXT NOT NULL,
    source_fingerprint BLOB,
    upload_id TEXT CHECK (upload_id IS NULL OR (length(upload_id)=32 AND upload_id NOT GLOB '*[^0-9a-f]*')),
    PRIMARY KEY (server_id,account_id,job_id,id),
    FOREIGN KEY (server_id,account_id,job_id) REFERENCES jobs(server_id,account_id,id) ON DELETE CASCADE
) STRICT;
CREATE TABLE chunk_acknowledgements (
    server_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    job_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    chunk_index INTEGER NOT NULL CHECK (chunk_index>=0),
    byte_count INTEGER NOT NULL CHECK (byte_count>0),
    digest TEXT NOT NULL CHECK (length(digest)=64 AND digest NOT GLOB '*[^0-9a-f]*'),
    PRIMARY KEY (server_id,account_id,job_id,task_id,chunk_index),
    FOREIGN KEY (server_id,account_id,job_id,task_id) REFERENCES file_tasks(server_id,account_id,job_id,id) ON DELETE CASCADE
) STRICT;
