-- Frozen fixture of the shipped identity-only schema. Do not regenerate from
-- the current migration history: upgrades must preserve this existing database.
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
PRAGMA user_version=1;
