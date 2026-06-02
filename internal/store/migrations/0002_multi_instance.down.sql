DROP INDEX IF EXISTS idx_sql_fingerprint_instance;

ALTER TABLE sql_fingerprint DROP COLUMN instance;
