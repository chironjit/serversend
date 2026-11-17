-- Browsable file tree. Contents now live at their real paths on the data
-- volume, so chunks record offsets within one file instead of object IDs,
-- and filesystem changes are journaled with the metadata that requires them.
-- Earlier file metadata must be empty: Open refuses otherwise and
-- serversend -reset-files clears it.
DROP INDEX chunks_by_object;
DROP TABLE chunks;
CREATE TABLE chunks (
    account_id TEXT NOT NULL,
    upload_id TEXT NOT NULL,
    chunk_index INTEGER NOT NULL CHECK (chunk_index>=0),
    byte_count INTEGER NOT NULL CHECK (byte_count>0),
    digest TEXT NOT NULL CHECK (length(digest)=64 AND digest NOT GLOB '*[^0-9a-f]*'),
    byte_offset INTEGER NOT NULL CHECK (byte_offset>=0),
    PRIMARY KEY (account_id,upload_id,chunk_index),
    FOREIGN KEY (account_id,upload_id) REFERENCES uploads(account_id,id)
) STRICT;
DROP TABLE deleted_objects;
CREATE TABLE storage_journal (
    -- Never reused: applied entries are recognised by ID.
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL CHECK (kind IN ('move','remove','mkdir')),
    source TEXT NOT NULL DEFAULT '',
    target TEXT NOT NULL CHECK (length(target)>0)
) STRICT;
-- Every non-final chunk has this size, which fixes each chunk's offset.
ALTER TABLE uploads ADD COLUMN chunk_bytes INTEGER CHECK (chunk_bytes IS NULL OR chunk_bytes>0);
-- A file replaced by a same-named upload stays retained but leaves the catalog.
ALTER TABLE files ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0 CHECK (hidden IN (0,1));
CREATE UNIQUE INDEX files_visible_name ON files(account_id,parent_id,name) WHERE hidden=0;
