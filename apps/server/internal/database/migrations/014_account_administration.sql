-- Expire credentials whose recoverable copies may exist in older backups.
-- Hash-only temporary credentials keep their existing expiration.
PRAGMA secure_delete=ON;
DELETE FROM auth_challenges WHERE account_id IN (SELECT id FROM accounts WHERE temporary_password IS NOT NULL);
UPDATE accounts SET temporary_expires_at=0 WHERE temporary_password IS NOT NULL;
DROP TRIGGER clear_temporary_password;
ALTER TABLE accounts DROP COLUMN temporary_password;
