ALTER TABLE accounts ADD COLUMN recovery_kind TEXT NOT NULL DEFAULT '' CHECK(recovery_kind IN ('','password','totp'));
ALTER TABLE device_sessions ADD COLUMN refresh_hash TEXT;
ALTER TABLE device_sessions ADD COLUMN previous_hash TEXT;
ALTER TABLE device_sessions ADD COLUMN rotation_id TEXT;
ALTER TABLE device_sessions ADD COLUMN last_refresh INTEGER NOT NULL DEFAULT 0;
ALTER TABLE device_sessions ADD COLUMN last_factor INTEGER NOT NULL DEFAULT 0;
ALTER TABLE device_sessions ADD COLUMN last_seen INTEGER NOT NULL DEFAULT 0;
ALTER TABLE device_sessions ADD COLUMN revoked_at INTEGER;
ALTER TABLE device_sessions ADD COLUMN platform TEXT NOT NULL DEFAULT '';
ALTER TABLE device_sessions ADD COLUMN app_version TEXT NOT NULL DEFAULT '';
UPDATE device_sessions SET last_seen=created_at,last_factor=created_at,last_refresh=created_at;
CREATE TABLE device_access (
 token_hash TEXT PRIMARY KEY NOT NULL,
 device_id TEXT NOT NULL REFERENCES device_sessions(id) ON DELETE CASCADE,
 expires_at INTEGER NOT NULL
) STRICT;
CREATE INDEX device_access_by_device ON device_access(device_id);
CREATE INDEX device_access_by_expiry ON device_access(expires_at);
ALTER TABLE auth_challenges ADD COLUMN device_id TEXT REFERENCES device_sessions(id);

CREATE TABLE audit_events_next (
 id INTEGER PRIMARY KEY,
 actor_id TEXT REFERENCES accounts(id),
 target_id TEXT NOT NULL REFERENCES accounts(id),
 action TEXT NOT NULL CHECK(action IN ('bootstrap_admin','recover_bootstrap','create_user','replace_password','reset_password','disable','enable','enroll_totp','reset_totp','replace_recovery_codes','revoke_device','refresh_reuse_revocation','device_reauthenticate','device_reauthenticate_recovery')),
 created_at INTEGER NOT NULL CHECK(created_at>=0),
 device_id TEXT REFERENCES device_sessions(id)
) STRICT;
INSERT INTO audit_events_next(id,actor_id,target_id,action,created_at) SELECT id,actor_id,target_id,action,created_at FROM audit_events;
DROP TABLE audit_events;
ALTER TABLE audit_events_next RENAME TO audit_events;
