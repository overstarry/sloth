package analyzer

import (
	"context"
	"testing"

	"github.com/overstarry/sloth/internal/config"
	"github.com/overstarry/sloth/internal/llm"
	"github.com/overstarry/sloth/internal/model"
)

func configMock() config.LLMConfig { return config.LLMConfig{Provider: "mock"} }

func TestParseDiagnosis_TolersuFences(t *testing.T) {
	in := "```json\n{\"root_cause\":\"seq scan\",\"risk_level\":\"warn\",\"suggestions\":[{\"kind\":\"index\",\"title\":\"add idx\"}]}\n```"
	d, err := parseDiagnosis(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.RootCause != "seq scan" {
		t.Errorf("root cause = %q", d.RootCause)
	}
	if d.RiskLevel != model.LevelWarn {
		t.Errorf("risk = %q", d.RiskLevel)
	}
	if len(d.Suggestions) != 1 || d.Suggestions[0].Kind != "index" {
		t.Errorf("suggestions = %+v", d.Suggestions)
	}
}

func TestDiagnose_MockProviderEndToEnd(t *testing.T) {
	provider, _ := llm.New(configMock())
	a := New(provider)
	ev := model.Evidence{SQL: model.SlowSQL{
		Fingerprint: "abc", Database: "appdb", QueryText: "SELECT * FROM t WHERE x = $1", MeanExecMs: 250, Calls: 10,
	}}
	d, err := a.Diagnose(context.Background(), ev)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if d.Fingerprint != "abc" {
		t.Errorf("fingerprint not propagated: %q", d.Fingerprint)
	}
	if d.RiskLevel == "" {
		t.Error("risk level should be set")
	}
	if len(d.Suggestions) == 0 {
		t.Error("expected at least one suggestion")
	}
}

func TestRunRules_FlagsSelectStarAndSeqScan(t *testing.T) {
	ev := model.Evidence{
		SQL:         model.SlowSQL{QueryText: "SELECT * FROM big WHERE k = $1"},
		ExplainJSON: []byte(`[{"Plan":{"Node Type":"Seq Scan"}}]`),
	}
	hits := runRules(ev)
	codes := map[string]bool{}
	for _, h := range hits {
		codes[h.Code] = true
	}
	if !codes["SELECT_STAR"] || !codes["SEQ_SCAN"] {
		t.Errorf("expected SELECT_STAR and SEQ_SCAN, got %v", codes)
	}
	if triageLevel(hits) != model.LevelCritical {
		t.Errorf("expected critical triage, got %s", triageLevel(hits))
	}
}
