-- schema.sql is the source of truth for sqlc type inference.
-- It must stay in sync with internal/store/migrations/*.up.sql.

-- One row per slow-SQL fingerprint observed (the dedup key).
CREATE TABLE sql_fingerprint (
    fingerprint  TEXT PRIMARY KEY,
    query_text   TEXT        NOT NULL,
    database     TEXT        NOT NULL,
    first_seen   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Time series of per-window metrics for each fingerprint.
CREATE TABLE slow_sql_snapshot (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    fingerprint   TEXT        NOT NULL REFERENCES sql_fingerprint(fingerprint),
    calls         BIGINT      NOT NULL,
    mean_exec_ms  DOUBLE PRECISION NOT NULL,
    total_exec_ms DOUBLE PRECISION NOT NULL,
    rows_per_call DOUBLE PRECISION NOT NULL,
    captured_at   TIMESTAMPTZ NOT NULL,
    UNIQUE (fingerprint, captured_at)
);

-- LLM / rule-engine diagnosis results, cached per fingerprint.
CREATE TABLE diagnosis (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    fingerprint  TEXT        NOT NULL REFERENCES sql_fingerprint(fingerprint),
    root_cause   TEXT        NOT NULL,
    suggestions  JSONB       NOT NULL,
    risk_level   TEXT        NOT NULL,
    model        TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Audit log of notifications sent (also powers cooldown checks).
CREATE TABLE notify_log (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    fingerprint  TEXT        NOT NULL,
    channel      TEXT        NOT NULL,
    level        TEXT        NOT NULL,
    sent_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    success      BOOLEAN     NOT NULL,
    error        TEXT
);
