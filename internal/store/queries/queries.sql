-- name: UpsertFingerprint :exec
INSERT INTO sql_fingerprint (fingerprint, instance, query_text, database, first_seen, last_seen)
VALUES ($1, $2, $3, $4, now(), now())
ON CONFLICT (fingerprint) DO UPDATE
    SET last_seen = now(),
        query_text = EXCLUDED.query_text;

-- name: InsertSnapshot :exec
INSERT INTO slow_sql_snapshot (fingerprint, calls, mean_exec_ms, total_exec_ms, rows_per_call, captured_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (fingerprint, captured_at) DO NOTHING;

-- name: ListTopSlowSQL :many
-- Latest snapshot per fingerprint (each target's collector ticks independently,
-- so a single global max(captured_at) would hide all but one instance). An empty
-- instance filter (empty string) returns every instance.
SELECT f.fingerprint, f.instance, f.query_text, f.database, s.calls, s.mean_exec_ms,
       s.total_exec_ms, s.rows_per_call, s.captured_at
FROM slow_sql_snapshot s
JOIN sql_fingerprint f ON f.fingerprint = s.fingerprint
WHERE s.captured_at = (
        SELECT max(s2.captured_at) FROM slow_sql_snapshot s2
        WHERE s2.fingerprint = s.fingerprint
      )
  AND (sqlc.arg(instance)::text = '' OR f.instance = sqlc.arg(instance)::text)
ORDER BY s.mean_exec_ms DESC
LIMIT $1;

-- name: GetFingerprint :one
SELECT fingerprint, instance, query_text, database, first_seen, last_seen
FROM sql_fingerprint
WHERE fingerprint = $1;

-- name: ListSnapshotsByFingerprint :many
SELECT fingerprint, calls, mean_exec_ms, total_exec_ms, rows_per_call, captured_at
FROM slow_sql_snapshot
WHERE fingerprint = $1
ORDER BY captured_at DESC
LIMIT $2;

-- name: SaveDiagnosis :one
INSERT INTO diagnosis (fingerprint, root_cause, suggestions, risk_level, model, created_at)
VALUES ($1, $2, $3, $4, $5, now())
RETURNING id;

-- name: GetLatestDiagnosis :one
SELECT id, fingerprint, root_cause, suggestions, risk_level, model, created_at
FROM diagnosis
WHERE fingerprint = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: InsertNotifyLog :exec
INSERT INTO notify_log (fingerprint, channel, level, success, error)
VALUES ($1, $2, $3, $4, $5);

-- name: LastNotifiedAt :one
SELECT max(sent_at)::timestamptz AS last_sent
FROM notify_log
WHERE fingerprint = $1 AND success = true;
