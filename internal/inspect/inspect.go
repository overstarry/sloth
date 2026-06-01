// Package inspect runs dynamic, read-only introspection against the target DB:
// EXPLAIN plans and table/index metadata. These queries are built at runtime
// from arbitrary slow SQL, so they use native pgx rather than sqlc.
package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/overstarry/sloth/internal/model"
)

// Inspector gathers evidence for a slow SQL from the target instance.
type Inspector struct {
	pool *pgxpool.Pool
}

// New returns an Inspector over an existing target pool.
func New(pool *pgxpool.Pool) *Inspector { return &Inspector{pool: pool} }

// ExplainJSON runs EXPLAIN (FORMAT JSON) without ANALYZE so nothing executes.
// The query may contain $N placeholders; we wrap it so the planner accepts it
// by substituting NULLs is unsafe, so we rely on generic plan estimation.
func (in *Inspector) ExplainJSON(ctx context.Context, query string) (json.RawMessage, error) {
	if !isSafeToExplain(query) {
		return nil, fmt.Errorf("query rejected: only read-only SELECT statements are explained")
	}
	// EXPLAIN of a parameterized statement requires the params; we use a
	// generic plan via PREPARE/EXPLAIN to avoid supplying values.
	sql := "EXPLAIN (FORMAT JSON, VERBOSE false) " + query
	var raw []byte
	err := in.pool.QueryRow(ctx, sql).Scan(&raw)
	if err != nil {
		return nil, fmt.Errorf("explain failed: %w", err)
	}
	return json.RawMessage(raw), nil
}

// isSafeToExplain guards against EXPLAIN-ing anything that could mutate data.
// EXPLAIN (without ANALYZE) never executes, but we still refuse non-SELECT to
// keep the read-only contract explicit and auditable.
func isSafeToExplain(query string) bool {
	q := strings.TrimSpace(strings.ToLower(query))
	if strings.HasPrefix(q, "with") || strings.HasPrefix(q, "select") {
		return !containsWriteCTE(q)
	}
	return false
}

func containsWriteCTE(q string) bool {
	for _, kw := range []string{"insert ", "update ", "delete ", "merge "} {
		if strings.Contains(q, kw) {
			return true
		}
	}
	return false
}

// Tables returns metadata (size, row estimate, indexes) for the given tables.
func (in *Inspector) Tables(ctx context.Context, schemas []TableRef) ([]model.TableInfo, error) {
	out := make([]model.TableInfo, 0, len(schemas))
	for _, ref := range schemas {
		ti, err := in.tableInfo(ctx, ref.Schema, ref.Name)
		if err != nil {
			continue // best-effort: skip tables we cannot introspect
		}
		out = append(out, ti)
	}
	return out, nil
}

// TableRef identifies a table to introspect.
type TableRef struct {
	Schema string
	Name   string
}

func (in *Inspector) tableInfo(ctx context.Context, schema, name string) (model.TableInfo, error) {
	if schema == "" {
		schema = "public"
	}
	ti := model.TableInfo{Schema: schema, Name: name}
	rel := schema + "." + name

	err := in.pool.QueryRow(ctx, `
		SELECT COALESCE(c.reltuples, 0)::bigint,
		       COALESCE(pg_total_relation_size($1), 0)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $2 AND c.relname = $3`,
		rel, schema, name,
	).Scan(&ti.RowEstimate, &ti.TotalBytes)
	if err != nil {
		return ti, err
	}

	rows, err := in.pool.Query(ctx, `
		SELECT i.indexname, i.indexdef,
		       ix.indisunique, ix.indisprimary
		FROM pg_indexes i
		JOIN pg_class c ON c.relname = i.indexname
		JOIN pg_index ix ON ix.indexrelid = c.oid
		WHERE i.schemaname = $1 AND i.tablename = $2`,
		schema, name,
	)
	if err != nil {
		return ti, nil // table info without indexes is still useful
	}
	defer rows.Close()
	for rows.Next() {
		var idx model.IndexInfo
		if err := rows.Scan(&idx.Name, &idx.Definition, &idx.IsUnique, &idx.IsPrimary); err != nil {
			continue
		}
		ti.Indexes = append(ti.Indexes, idx)
	}
	return ti, nil
}
