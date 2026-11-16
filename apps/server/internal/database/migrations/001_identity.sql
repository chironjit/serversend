CREATE TABLE server_identity (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    server_id TEXT NOT NULL UNIQUE CHECK (length(server_id) = 32),
    key_seed BLOB NOT NULL CHECK (typeof(key_seed) = 'blob' AND length(key_seed) = 32)
);
