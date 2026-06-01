// Package analyzer turns a slow SQL plus introspected evidence into a
// diagnosis, using cheap static rules for triage and an LLM for deep analysis.
package analyzer

import (
	"encoding/json"
	"strings"

	"github.com/overstarry/sloth/internal/model"
)

// runRules applies cheap heuristics over the query text and EXPLAIN plan to
// produce a scored set of findings used for triage and to seed the LLM prompt.
func runRules(ev model.Evidence) []model.RuleHit {
	var hits []model.RuleHit
	q := strings.ToLower(ev.SQL.QueryText)

	if strings.Contains(q, "select *") {
		hits = append(hits, model.RuleHit{
			Code: "SELECT_STAR", Weight: 1,
			Message: "SELECT * fetches unused columns and defeats covering indexes.",
		})
	}
	if planContains(ev.ExplainJSON, "Seq Scan") {
		hits = append(hits, model.RuleHit{
			Code: "SEQ_SCAN", Weight: 3,
			Message: "Plan uses a sequential scan; a filtered/large table likely needs an index.",
		})
	}
	if planContains(ev.ExplainJSON, "external merge") || planContains(ev.ExplainJSON, "Sort Method: external") {
		hits = append(hits, model.RuleHit{
			Code: "SORT_SPILL", Weight: 2,
			Message: "Sort spills to disk; consider work_mem or an index providing order.",
		})
	}
	if strings.Contains(q, " like '%") {
		hits = append(hits, model.RuleHit{
			Code: "LEADING_WILDCARD", Weight: 2,
			Message: "Leading-wildcard LIKE cannot use a btree index; consider trigram/GIN.",
		})
	}
	return hits
}

// planContains does a substring scan over the raw EXPLAIN JSON. It is a cheap
// signal; the LLM does the precise structural reasoning.
func planContains(plan json.RawMessage, needle string) bool {
	if len(plan) == 0 {
		return false
	}
	return strings.Contains(string(plan), needle)
}

// triageLevel maps the summed rule weight to a severity used before the LLM
// refines it.
func triageLevel(hits []model.RuleHit) model.Level {
	total := 0
	for _, h := range hits {
		total += h.Weight
	}
	switch {
	case total >= 4:
		return model.LevelCritical
	case total >= 2:
		return model.LevelWarn
	default:
		return model.LevelInfo
	}
}
