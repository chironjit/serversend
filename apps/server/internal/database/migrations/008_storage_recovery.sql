-- Startup reconciliation queries one object at a time without loading the
-- complete catalog into memory. Expiry maintenance is global across accounts.
CREATE INDEX chunks_by_object ON chunks(object_id);
CREATE INDEX uploads_by_expiry ON uploads(expires_at) WHERE state='open';
