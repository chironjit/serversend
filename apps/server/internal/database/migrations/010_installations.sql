ALTER TABLE device_sessions ADD COLUMN installation_id TEXT;
CREATE UNIQUE INDEX device_installation ON device_sessions(account_id,installation_id) WHERE installation_id IS NOT NULL;
-- Older bearer-only grants have no device refresh policy. Preserve their history,
-- invalidate authority once, and require full password plus second-factor login.
UPDATE device_sessions SET revoked_at=coalesce(revoked_at,created_at) WHERE refresh_hash IS NULL;
DELETE FROM auth_challenges;
