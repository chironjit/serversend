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
