// Package store wraps the sqlc-generated queries with domain-oriented methods,
// isolating the rest of the app from generated code and pgx types.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/overstarry/sloth/internal/model"
	"github.com/overstarry/sloth/internal/store/gen"
)

// Store is the persistence facade backed by a pgx pool.
type Store struct {
	pool *pgxpool.Pool
	q    *gen.Queries
}

// New opens a connection pool to the sloth state database.
func New(ctx context.Context, dsn string, maxConns int32) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool, q: gen.New(pool)}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// SaveSnapshot upserts the fingerprint and inserts one metrics row.
func (s *Store) SaveSnapshot(ctx context.Context, sql model.SlowSQL) error {
	if err := s.q.UpsertFingerprint(ctx, gen.UpsertFingerprintParams{
		Fingerprint: sql.Fingerprint,
		QueryText:   sql.QueryText,
		Database:    sql.Database,
	}); err != nil {
		return err
	}
	return s.q.InsertSnapshot(ctx, gen.InsertSnapshotParams{
		Fingerprint: sql.Fingerprint,
		Calls:       sql.Calls,
		MeanExecMs:  sql.MeanExecMs,
		TotalExecMs: sql.TotalExecMs,
		RowsPerCall: sql.RowsPerCall,
		CapturedAt:  sql.CapturedAt,
	})
}

// TopSlowSQL returns the slowest statements from the most recent snapshot.
func (s *Store) TopSlowSQL(ctx context.Context, limit int32) ([]model.SlowSQL, error) {
	rows, err := s.q.ListTopSlowSQL(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]model.SlowSQL, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.SlowSQL{
			Fingerprint: r.Fingerprint,
			QueryText:   r.QueryText,
			Database:    r.Database,
			Calls:       r.Calls,
			MeanExecMs:  r.MeanExecMs,
			TotalExecMs: r.TotalExecMs,
			RowsPerCall: r.RowsPerCall,
			CapturedAt:  r.CapturedAt,
		})
	}
	return out, nil
}

// SlowSQLByFingerprint reconstructs a SlowSQL from the fingerprint row plus its
// most recent metrics snapshot. Returns nil if the fingerprint is unknown.
func (s *Store) SlowSQLByFingerprint(ctx context.Context, fingerprint string) (*model.SlowSQL, error) {
	fp, err := s.q.GetFingerprint(ctx, fingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sql := &model.SlowSQL{
		Fingerprint: fp.Fingerprint,
		QueryText:   fp.QueryText,
		Database:    fp.Database,
	}
	snaps, err := s.q.ListSnapshotsByFingerprint(ctx, gen.ListSnapshotsByFingerprintParams{
		Fingerprint: fingerprint,
		Limit:       1,
	})
	if err == nil && len(snaps) > 0 {
		sql.Calls = snaps[0].Calls
		sql.MeanExecMs = snaps[0].MeanExecMs
		sql.TotalExecMs = snaps[0].TotalExecMs
		sql.RowsPerCall = snaps[0].RowsPerCall
		sql.CapturedAt = snaps[0].CapturedAt
	}
	return sql, nil
}

// SaveDiagnosis persists a diagnosis result and returns nil on success.
func (s *Store) SaveDiagnosis(ctx context.Context, d model.Diagnosis) error {
	suggestions, err := json.Marshal(d.Suggestions)
	if err != nil {
		return err
	}
	_, err = s.q.SaveDiagnosis(ctx, gen.SaveDiagnosisParams{
		Fingerprint: d.Fingerprint,
		RootCause:   d.RootCause,
		Suggestions: suggestions,
		RiskLevel:   string(d.RiskLevel),
		Model:       d.Model,
	})
	return err
}

// LatestDiagnosis returns the most recent diagnosis for a fingerprint, if any.
func (s *Store) LatestDiagnosis(ctx context.Context, fingerprint string) (*model.Diagnosis, error) {
	row, err := s.q.GetLatestDiagnosis(ctx, fingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var suggestions []model.Suggestion
	_ = json.Unmarshal(row.Suggestions, &suggestions)
	return &model.Diagnosis{
		Fingerprint: row.Fingerprint,
		RootCause:   row.RootCause,
		Suggestions: suggestions,
		RiskLevel:   model.Level(row.RiskLevel),
		Model:       row.Model,
		CreatedAt:   row.CreatedAt,
	}, nil
}

// LastNotified implements notify.CooldownStore.
func (s *Store) LastNotified(ctx context.Context, fingerprint string) (time.Time, bool, error) {
	t, err := s.q.LastNotifiedAt(ctx, fingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	if t.IsZero() {
		return time.Time{}, false, nil
	}
	return t, true, nil
}

// RecordNotify implements notify.CooldownStore.
func (s *Store) RecordNotify(ctx context.Context, fingerprint, channel string, level model.Level, success bool, errMsg string) error {
	var errPtr *string
	if errMsg != "" {
		errPtr = &errMsg
	}
	return s.q.InsertNotifyLog(ctx, gen.InsertNotifyLogParams{
		Fingerprint: fingerprint,
		Channel:     channel,
		Level:       string(level),
		Success:     success,
		Error:       errPtr,
	})
}

// Pool exposes the underlying pool for migrations/health checks.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }
