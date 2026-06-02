// Package collector periodically samples pg_stat_statements from the target
// PostgreSQL instance, computes per-window deltas, and persists the slowest
// statements as fingerprinted snapshots.
package collector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/overstarry/sloth/internal/config"
	"github.com/overstarry/sloth/internal/model"
)

// Sink persists collected slow SQL (implemented by store.Store).
type Sink interface {
	SaveSnapshot(ctx context.Context, sql model.SlowSQL) error
}

// Collector samples the target and writes to a Sink.
type Collector struct {
	pool     *pgxpool.Pool
	instance string
	cfg      config.CollectorConfig
	sink     Sink
	log      *slog.Logger

	// prev holds the previous cumulative counters per queryid for delta math.
	prev map[int64]counters
}

type counters struct {
	calls     int64
	totalTime float64
}

// New connects to the target instance (read-only usage) and returns a Collector.
// The target's Name namespaces every fingerprint this collector produces so
// identical SQL on different instances never collides.
func New(ctx context.Context, target config.TargetConfig, cfg config.CollectorConfig, sink Sink, log *slog.Logger) (*Collector, error) {
	pcfg, err := pgxpool.ParseConfig(target.DSN)
	if err != nil {
		return nil, err
	}
	if target.MaxConns > 0 {
		pcfg.MaxConns = target.MaxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, err
	}
	return &Collector{pool: pool, instance: target.Name, cfg: cfg, sink: sink, log: log, prev: map[int64]counters{}}, nil
}

// Instance returns the configured name of the monitored target.
func (c *Collector) Instance() string { return c.instance }

// Close releases the target pool.
func (c *Collector) Close() { c.pool.Close() }

// TargetPool exposes the target connection pool for read-only introspection.
func (c *Collector) TargetPool() *pgxpool.Pool { return c.pool }

// Preflight verifies pg_stat_statements is installed and queryable, returning a
// helpful error otherwise.
func (c *Collector) Preflight(ctx context.Context) error {
	var installed bool
	err := c.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements')`,
	).Scan(&installed)
	if err != nil {
		return fmt.Errorf("preflight query failed (check monitor grants): %w", err)
	}
	if !installed {
		return fmt.Errorf("pg_stat_statements not installed: add it to shared_preload_libraries and run CREATE EXTENSION pg_stat_statements")
	}
	return nil
}

// Run blocks, sampling every cfg.Interval until ctx is cancelled.
func (c *Collector) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()
	// Prime the baseline so the first persisted window reflects real deltas.
	if _, err := c.sample(ctx, false); err != nil {
		c.log.Warn("collector baseline sample failed", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			n, err := c.sample(ctx, true)
			if err != nil {
				c.log.Error("collector sample failed", "err", err)
				continue
			}
			c.log.Debug("collector sample done", "persisted", n)
		}
	}
}

const statQuery = `
SELECT s.queryid, s.query, d.datname, s.calls, s.total_exec_time, s.rows
FROM pg_stat_statements s
JOIN pg_database d ON d.oid = s.dbid
WHERE s.queryid IS NOT NULL`

// sample reads current counters, diffs against the previous reading, and (when
// persist is true) writes the slowest statements that exceed the threshold.
func (c *Collector) sample(ctx context.Context, persist bool) (int, error) {
	rows, err := c.pool.Query(ctx, statQuery)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	now := time.Now()
	var candidates []model.SlowSQL
	next := make(map[int64]counters)

	for rows.Next() {
		var (
			queryid   int64
			query     string
			datname   string
			calls     int64
			totalTime float64
			rowsRet   int64
		)
		if err := rows.Scan(&queryid, &query, &datname, &calls, &totalTime, &rowsRet); err != nil {
			return 0, err
		}
		next[queryid] = counters{calls: calls, totalTime: totalTime}

		prev := c.prev[queryid]
		dCalls := calls - prev.calls
		dTime := totalTime - prev.totalTime
		// Skip statements with no activity this window or that were reset.
		if dCalls <= 0 || dTime < 0 {
			continue
		}
		meanMs := dTime / float64(dCalls)
		if meanMs < c.cfg.MinMeanExecMs {
			continue
		}
		candidates = append(candidates, model.SlowSQL{
			Fingerprint: Fingerprint(c.instance, datname, query),
			Instance:    c.instance,
			QueryID:     queryid,
			QueryText:   query,
			Calls:       dCalls,
			MeanExecMs:  meanMs,
			TotalExecMs: dTime,
			RowsPerCall: float64(rowsRet) / float64(max64(dCalls, 1)),
			Database:    datname,
			CapturedAt:  now,
		})
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	c.prev = next

	if !persist {
		return 0, nil
	}

	topN := topByMean(candidates, c.cfg.TopN)
	for _, s := range topN {
		if err := c.sink.SaveSnapshot(ctx, s); err != nil {
			c.log.Error("persist snapshot failed", "fingerprint", s.Fingerprint, "err", err)
		}
	}
	return len(topN), nil
}

// Fingerprint derives a stable dedup key. pg_stat_statements already normalizes
// literals to $N, so hashing (instance, database, normalized query) is
// sufficient — instance keeps identical SQL on different targets distinct.
func Fingerprint(instance, database, normalizedQuery string) string {
	h := sha256.Sum256([]byte(instance + "::" + database + "::" + normalizedQuery))
	return hex.EncodeToString(h[:16])
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
