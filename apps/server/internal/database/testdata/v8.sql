-- Frozen fixture of the shipped two-factor schema (version 4).
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
ALTER TABLE accounts ADD COLUMN temporary_expires_at INTEGER CHECK (temporary_expires_at IS NULL OR temporary_expires_at>=0);
ALTER TABLE accounts ADD COLUMN credential_revision INTEGER NOT NULL DEFAULT 0 CHECK (credential_revision>=0);
ALTER TABLE accounts ADD COLUMN auth_epoch INTEGER NOT NULL DEFAULT 0 CHECK (auth_epoch>=0);
ALTER TABLE accounts ADD COLUMN failed_attempts INTEGER NOT NULL DEFAULT 0 CHECK (failed_attempts BETWEEN 0 AND 10);
ALTER TABLE accounts ADD COLUMN blocked_until INTEGER NOT NULL DEFAULT 0 CHECK (blocked_until>=0);

-- Restricted attempts convey no file, device, or administrator authority.
CREATE TABLE auth_challenges (
    token_hash TEXT PRIMARY KEY NOT NULL CHECK (length(token_hash)=64 AND token_hash NOT GLOB '*[^0-9a-f]*'),
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    scope TEXT NOT NULL CHECK (scope IN ('replace_password','enroll_totp','second_factor')),
    credential_revision INTEGER NOT NULL CHECK (credential_revision>=0),
    auth_epoch INTEGER NOT NULL CHECK (auth_epoch>=0),
    expires_at INTEGER NOT NULL CHECK (expires_at>=0)
) STRICT;
CREATE INDEX auth_challenges_by_account ON auth_challenges(account_id);

CREATE TABLE audit_events (
    id INTEGER PRIMARY KEY,
    actor_id TEXT REFERENCES accounts(id),
    target_id TEXT NOT NULL REFERENCES accounts(id),
    action TEXT NOT NULL CHECK (action IN ('bootstrap_admin','recover_bootstrap','create_user','replace_password','reset_password','disable','enable','enroll_totp','reset_totp','replace_recovery_codes')),
    created_at INTEGER NOT NULL CHECK (created_at>=0)
) STRICT;
UPDATE accounts SET temporary_expires_at=200000,credential_revision=2,auth_epoch=3 WHERE id='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
INSERT INTO auth_challenges VALUES(
    'dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd',
    'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','replace_password',2,3,150000
);
INSERT INTO audit_events(actor_id,target_id,action,created_at) VALUES(NULL,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','reset_password',100000);
-- Second-factor backoff is independent of password backoff: a correct password
-- must not reset the budget for guessing TOTP or recovery codes.
ALTER TABLE accounts ADD COLUMN second_factor_failures INTEGER NOT NULL DEFAULT 0 CHECK (second_factor_failures BETWEEN 0 AND 10);
ALTER TABLE accounts ADD COLUMN second_factor_blocked_until INTEGER NOT NULL DEFAULT 0 CHECK (second_factor_blocked_until>=0);
ALTER TABLE auth_challenges ADD COLUMN failures INTEGER NOT NULL DEFAULT 0 CHECK (failures BETWEEN 0 AND 5);

-- One secret per account, encrypted under the configuration key and bound to
-- the server/account scope. A provisional row grants nothing until confirmed.
CREATE TABLE totp_secrets (
    account_id TEXT PRIMARY KEY NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    confirmed INTEGER NOT NULL DEFAULT 0 CHECK (confirmed IN (0,1)),
    envelope BLOB NOT NULL CHECK (typeof(envelope)='blob' AND length(envelope)=61),
    last_step INTEGER NOT NULL DEFAULT -1 CHECK (last_step>=-1),
    created_at INTEGER NOT NULL CHECK (created_at>=0),
    CHECK (confirmed=1 OR last_step=-1)
) STRICT;

-- Only scoped hashes are stored; a row is deleted when its code is consumed.
CREATE TABLE recovery_codes (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    code_hash BLOB NOT NULL CHECK (typeof(code_hash)='blob' AND length(code_hash)=32),
    PRIMARY KEY (account_id,code_hash)
) STRICT, WITHOUT ROWID;
INSERT INTO totp_secrets(account_id,envelope,created_at) VALUES('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',zeroblob(61),100000);
PRAGMA user_version=4;

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

PRAGMA user_version=5;
CREATE TABLE directories (
 account_id TEXT NOT NULL REFERENCES accounts(id), id TEXT NOT NULL,
 parent_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL, revision INTEGER NOT NULL,
 PRIMARY KEY(account_id,id), UNIQUE(account_id,parent_id,name)
) STRICT;
ALTER TABLE uploads ADD COLUMN parent_id TEXT NOT NULL DEFAULT '';
ALTER TABLE files ADD COLUMN parent_id TEXT NOT NULL DEFAULT '';
CREATE INDEX files_parent ON files(account_id,parent_id,id);

PRAGMA user_version=6;

ALTER TABLE accounts ADD COLUMN temporary_password BLOB;
CREATE TRIGGER clear_temporary_password AFTER UPDATE OF state,temporary_expires_at,password_hash ON accounts
WHEN NEW.state != 'password_required' OR NEW.temporary_expires_at IS NULL OR NEW.password_hash != OLD.password_hash
BEGIN UPDATE accounts SET temporary_password=NULL WHERE id=NEW.id; END;
CREATE TABLE deleted_objects(id TEXT PRIMARY KEY NOT NULL) STRICT;

PRAGMA user_version=7;

-- Startup reconciliation queries one object at a time without loading the
-- complete catalog into memory. Expiry maintenance is global across accounts.
CREATE INDEX chunks_by_object ON chunks(object_id);
CREATE INDEX uploads_by_expiry ON uploads(expires_at) WHERE state='open';

PRAGMA user_version=8;
