-- Add the instance dimension so a single sloth can monitor multiple PG targets.
-- Existing rows default to '' (the implicit single-instance namespace).
ALTER TABLE sql_fingerprint ADD COLUMN instance TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_sql_fingerprint_instance ON sql_fingerprint (instance);
