-- Frozen fixture of the shipped identity/account/upload schema (version 2).
-- Do not regenerate from current migrations: existing rows must survive upgrades.
CREATE TABLE server_identity (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    server_id TEXT NOT NULL UNIQUE CHECK (length(server_id) = 32),
    key_seed BLOB NOT NULL CHECK (typeof(key_seed) = 'blob' AND length(key_seed) = 32)
);
INSERT INTO server_identity VALUES (
    1,
    'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    X'000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f'
);
-- Password verification, TOTP, devices and catalog publication are later chunks.
-- This layer stores hashes/opaque identifiers, never passwords or file bytes.
CREATE TABLE accounts (
    id TEXT PRIMARY KEY NOT NULL CHECK (length(id)=32 AND id NOT GLOB '*[^0-9a-f]*'),
    username TEXT NOT NULL UNIQUE CHECK (length(username) BETWEEN 1 AND 128 AND username=trim(username)),
    role TEXT NOT NULL CHECK (role IN ('admin','user')),
    state TEXT NOT NULL CHECK (state IN ('password_required','totp_required','active')),
    totp_enrolled INTEGER NOT NULL DEFAULT 0 CHECK (totp_enrolled IN (0,1)),
    disabled INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0,1)),
    password_hash TEXT NOT NULL CHECK (length(password_hash)>0),
    created_at INTEGER NOT NULL CHECK (created_at>=0),
    CHECK ((state!='active' OR totp_enrolled=1) AND (state!='totp_required' OR totp_enrolled=0))
) STRICT;
CREATE TABLE uploads (
    account_id TEXT NOT NULL REFERENCES accounts(id),
    id TEXT NOT NULL CHECK (length(id)=32 AND id NOT GLOB '*[^0-9a-f]*'),
    state TEXT NOT NULL CHECK (state IN ('open','finalizing','committed','conflicted','cancelled','expired')),
    mode TEXT NOT NULL CHECK (mode IN ('standard','private')),
    idempotency_key TEXT NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
    expected_bytes INTEGER NOT NULL CHECK (expected_bytes>=0),
    created_at INTEGER NOT NULL CHECK (created_at>=0),
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,idempotency_key)
) STRICT;
CREATE TABLE reservations (
    account_id TEXT NOT NULL,
    upload_id TEXT NOT NULL,
    bytes INTEGER NOT NULL CHECK (bytes>=0),
    PRIMARY KEY (account_id,upload_id),
    FOREIGN KEY (account_id,upload_id) REFERENCES uploads(account_id,id) ON DELETE CASCADE
) STRICT;
CREATE TABLE chunks (
    account_id TEXT NOT NULL,
    upload_id TEXT NOT NULL,
    chunk_index INTEGER NOT NULL CHECK (chunk_index>=0),
    byte_count INTEGER NOT NULL CHECK (byte_count>0),
    digest TEXT NOT NULL CHECK (length(digest)=64 AND digest NOT GLOB '*[^0-9a-f]*'),
    object_id TEXT NOT NULL CHECK (length(object_id)=32 AND object_id NOT GLOB '*[^0-9a-f]*'),
    PRIMARY KEY (account_id,upload_id,chunk_index),
    FOREIGN KEY (account_id,upload_id) REFERENCES uploads(account_id,id)
) STRICT;
CREATE INDEX uploads_by_state ON uploads(account_id,state,id);

INSERT INTO accounts(id,username,role,state,password_hash,created_at) VALUES(
    'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    'user','password_required','opaque-encoded-hash',100000
);
INSERT INTO uploads VALUES(
    'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    'cccccccccccccccccccccccccccccccc',
    'open','standard','create-1',42,100000
);
INSERT INTO reservations VALUES(
    'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    'cccccccccccccccccccccccccccccccc',42
);
PRAGMA user_version=2;
