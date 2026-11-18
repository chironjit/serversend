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

INSERT INTO accounts(id,username,role,state,created_at,password_hash,temporary_expires_at,temporary_password)
VALUES('bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','pending-user','user','password_required',100000,'hash',200000,X'010203');
INSERT INTO auth_challenges(token_hash,account_id,scope,credential_revision,auth_epoch,expires_at)
VALUES(printf('%064d',1),'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','replace_password',0,0,200000);
PRAGMA user_version=13;
