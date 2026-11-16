ALTER TABLE accounts ADD COLUMN temporary_password BLOB;
CREATE TRIGGER clear_temporary_password AFTER UPDATE OF state,temporary_expires_at,password_hash ON accounts
WHEN NEW.state != 'password_required' OR NEW.temporary_expires_at IS NULL OR NEW.password_hash != OLD.password_hash
BEGIN UPDATE accounts SET temporary_password=NULL WHERE id=NEW.id; END;
CREATE TABLE deleted_objects(id TEXT PRIMARY KEY NOT NULL) STRICT;
