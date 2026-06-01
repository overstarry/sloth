package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/overstarry/sloth/internal/llm"
	"github.com/overstarry/sloth/internal/model"
)

// Analyzer orchestrates rule-based triage and LLM diagnosis for a slow SQL.
type Analyzer struct {
	provider llm.Provider
}

// New returns an Analyzer backed by the given model provider.
func New(provider llm.Provider) *Analyzer {
	return &Analyzer{provider: provider}
}

// Diagnose produces a Diagnosis from gathered evidence. The rule engine seeds
// the prompt and provides a fallback severity; the LLM supplies root cause and
// concrete suggestions.
func (a *Analyzer) Diagnose(ctx context.Context, ev model.Evidence) (model.Diagnosis, error) {
	ev.RuleHits = runRules(ev)

	req := llm.Request{
		System: systemPrompt,
		User:   buildUserPrompt(ev),
	}
	out, err := a.provider.Complete(ctx, req)
	if err != nil {
		return model.Diagnosis{}, fmt.Errorf("llm diagnose: %w", err)
	}

	d, err := parseDiagnosis(out)
	if err != nil {
		return model.Diagnosis{}, fmt.Errorf("parse diagnosis: %w", err)
	}
	d.Fingerprint = ev.SQL.Fingerprint
	d.Model = a.provider.Name()
	d.CreatedAt = time.Now()
	if d.RiskLevel == "" {
		d.RiskLevel = triageLevel(ev.RuleHits)
	}
	return d, nil
}

const systemPrompt = `You are a senior PostgreSQL performance engineer. Given a slow SQL statement,
its EXPLAIN plan, table/index metadata, and static rule findings, identify the
root cause and propose concrete, safe optimizations.

Rules:
- Never propose statements that modify data. Index DDL is advisory only.
- Prefer CREATE INDEX CONCURRENTLY for index suggestions.
- Be specific: name columns and indexes.
Respond with ONLY a JSON object of this shape:
{
  "root_cause": string,
  "risk_level": "info" | "warn" | "critical",
  "suggestions": [
    {"kind":"index|rewrite|config","title":string,"ddl":string,"detail":string,"expected_gain":string}
  ]
}`

func buildUserPrompt(ev model.Evidence) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Database: %s\n", ev.SQL.Database)
	fmt.Fprintf(&b, "Mean exec time: %.2f ms over %d calls\n", ev.SQL.MeanExecMs, ev.SQL.Calls)
	fmt.Fprintf(&b, "\nSQL:\n%s\n", ev.SQL.QueryText)

	if len(ev.ExplainJSON) > 0 {
		fmt.Fprintf(&b, "\nEXPLAIN (JSON):\n%s\n", string(ev.ExplainJSON))
	}
	if len(ev.Tables) > 0 {
		b.WriteString("\nTables:\n")
		for _, t := range ev.Tables {
			fmt.Fprintf(&b, "- %s.%s: ~%d rows, %d bytes, %d indexes\n",
				t.Schema, t.Name, t.RowEstimate, t.TotalBytes, len(t.Indexes))
			for _, idx := range t.Indexes {
				fmt.Fprintf(&b, "    * %s\n", idx.Definition)
			}
		}
	}
	if len(ev.RuleHits) > 0 {
		b.WriteString("\nStatic findings:\n")
		for _, h := range ev.RuleHits {
			fmt.Fprintf(&b, "- [%s] %s\n", h.Code, h.Message)
		}
	}
	return b.String()
}

// parseDiagnosis extracts the JSON object from the model response, tolerating
// surrounding prose or code fences.
func parseDiagnosis(out string) (model.Diagnosis, error) {
	jsonStr := extractJSON(out)
	var raw struct {
		RootCause   string             `json:"root_cause"`
		RiskLevel   string             `json:"risk_level"`
		Suggestions []model.Suggestion `json:"suggestions"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return model.Diagnosis{}, err
	}
	return model.Diagnosis{
		RootCause:   raw.RootCause,
		RiskLevel:   model.Level(raw.RiskLevel),
		Suggestions: raw.Suggestions,
	}, nil
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}
