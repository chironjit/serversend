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
