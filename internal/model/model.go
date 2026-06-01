// Package model holds the domain types shared across sloth's layers.
package model

import (
	"encoding/json"
	"time"
)

// Level is an alert severity.
type Level string

const (
	LevelInfo     Level = "info"
	LevelWarn     Level = "warn"
	LevelCritical Level = "critical"
)

// SlowSQL is one normalized slow statement captured from the target.
type SlowSQL struct {
	Fingerprint string    // stable hash of the normalized query
	QueryID     int64     // pg_stat_statements queryid
	QueryText   string    // normalized query text ($1, $2, ...)
	Calls       int64     // call delta in the snapshot window
	MeanExecMs  float64   // mean execution time (ms)
	TotalExecMs float64   // total execution time delta (ms)
	RowsPerCall float64   // mean rows returned
	Database    string    // database name
	CapturedAt  time.Time // snapshot time
}

// Evidence is the structured context gathered for a slow SQL before diagnosis.
type Evidence struct {
	SQL         SlowSQL
	ExplainJSON json.RawMessage // EXPLAIN (FORMAT JSON) output
	Tables      []TableInfo
	RuleHits    []RuleHit // cheap static heuristics
}

// TableInfo is introspected metadata for a table referenced by the query.
type TableInfo struct {
	Schema      string
	Name        string
	RowEstimate int64
	TotalBytes  int64
	Indexes     []IndexInfo
}

// IndexInfo describes an existing index.
type IndexInfo struct {
	Name       string
	Definition string
	IsUnique   bool
	IsPrimary  bool
}

// RuleHit is a finding from the static rule engine.
type RuleHit struct {
	Code    string // e.g. SEQ_SCAN, MISSING_INDEX, SELECT_STAR
	Message string
	Weight  int
}

// Diagnosis is the LLM (or rule-engine) verdict for a slow SQL.
type Diagnosis struct {
	Fingerprint string       `json:"fingerprint"`
	RootCause   string       `json:"root_cause"`
	Suggestions []Suggestion `json:"suggestions"`
	RiskLevel   Level        `json:"risk_level"`
	Model       string       `json:"model"`
	CreatedAt   time.Time    `json:"created_at"`
}

// Suggestion is one actionable optimization proposal.
type Suggestion struct {
	Kind         string `json:"kind"` // index | rewrite | config
	Title        string `json:"title"`
	DDL          string `json:"ddl,omitempty"` // advisory only, never auto-applied
	Detail       string `json:"detail"`
	ExpectedGain string `json:"expected_gain"`
}
