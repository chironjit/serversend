CREATE TABLE vault_envelopes (
 account_id TEXT PRIMARY KEY NOT NULL REFERENCES accounts(id),
 revision INTEGER NOT NULL CHECK(revision>0),
 envelope BLOB NOT NULL
) STRICT;
CREATE TABLE private_directories (
 account_id TEXT NOT NULL,
 id TEXT NOT NULL,
 envelope BLOB NOT NULL,
 PRIMARY KEY(account_id,id),
 FOREIGN KEY(account_id,id) REFERENCES directories(account_id,id) ON DELETE CASCADE
) STRICT;
