// Package app wires the collector, introspection, analyzer, store, and notifier
// into the on-demand diagnosis pipeline consumed by the API and the background
// loop.
package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/overstarry/sloth/internal/analyzer"
	"github.com/overstarry/sloth/internal/inspect"
	"github.com/overstarry/sloth/internal/model"
	"github.com/overstarry/sloth/internal/notify"
	"github.com/overstarry/sloth/internal/store"
)

// App is the central orchestrator implementing api.Service.
type App struct {
	store      *store.Store
	inspectors map[string]*inspect.Inspector // keyed by target instance name
	analyzer   *analyzer.Analyzer
	notifier   *notify.Dispatcher
	log        *slog.Logger
	dashboard  string // base URL for building detail links
}

// New constructs the orchestrator. inspectors maps each monitored target's
// instance name to an Inspector over that target's pool, so diagnosis runs
// EXPLAIN against the instance the slow SQL actually came from.
func New(st *store.Store, inspectors map[string]*inspect.Inspector, an *analyzer.Analyzer, nt *notify.Dispatcher, dashboardURL string, log *slog.Logger) *App {
	return &App{store: st, inspectors: inspectors, analyzer: an, notifier: nt, dashboard: dashboardURL, log: log}
}

// TopSlowSQL proxies to the store, optionally filtered by instance.
func (a *App) TopSlowSQL(ctx context.Context, limit int32, instance string) ([]model.SlowSQL, error) {
	return a.store.TopSlowSQL(ctx, limit, instance)
}

// LatestDiagnosis proxies to the store.
func (a *App) LatestDiagnosis(ctx context.Context, fingerprint string) (*model.Diagnosis, error) {
	return a.store.LatestDiagnosis(ctx, fingerprint)
}

// DiagnoseNow gathers evidence for a fingerprint, runs the analyzer, persists
// the diagnosis, and dispatches a notification when the severity warrants it.
func (a *App) DiagnoseNow(ctx context.Context, fingerprint string) (*model.Diagnosis, error) {
	sql, err := a.store.SlowSQLByFingerprint(ctx, fingerprint)
	if err != nil {
		return nil, err
	}
	if sql == nil {
		return nil, fmt.Errorf("unknown fingerprint %q", fingerprint)
	}

	ev := model.Evidence{SQL: *sql}
	// Route introspection to the instance this SQL came from; without a match
	// we still diagnose, just without an EXPLAIN plan.
	if ins := a.inspectors[sql.Instance]; ins == nil {
		a.log.Warn("no inspector for instance; explain skipped", "instance", sql.Instance, "fingerprint", fingerprint)
	} else if plan, err := ins.ExplainJSON(ctx, sql.QueryText); err != nil {
		a.log.Debug("explain skipped", "fingerprint", fingerprint, "err", err)
	} else {
		ev.ExplainJSON = plan
	}

	diag, err := a.analyzer.Diagnose(ctx, ev)
	if err != nil {
		return nil, err
	}
	if err := a.store.SaveDiagnosis(ctx, diag); err != nil {
		return nil, err
	}

	a.dispatch(ctx, *sql, diag)
	return &diag, nil
}

// dispatch sends a notification for the diagnosis. Failures are logged, not
// propagated — alerting is best-effort and must not fail the request.
func (a *App) dispatch(ctx context.Context, sql model.SlowSQL, d model.Diagnosis) {
	if a.notifier == nil {
		return
	}
	msg := notify.Message{
		Title:   "慢 SQL 诊断告警",
		Level:   d.RiskLevel,
		Summary: fmt.Sprintf("`%s` 均耗时 %.1fms / %d 次调用 (instance=%s, db=%s)", short(sql.Fingerprint), sql.MeanExecMs, sql.Calls, sql.Instance, sql.Database),
		Detail:  formatDetail(d),
		Link:    fmt.Sprintf("%s/slow-sql/%s", a.dashboard, sql.Fingerprint),
	}
	if err := a.notifier.Notify(ctx, sql.Fingerprint, msg); err != nil {
		a.log.Warn("notify dispatch failed", "err", err)
	}
}

func formatDetail(d model.Diagnosis) string {
	s := "**根因**: " + d.RootCause + "\n"
	for i, sug := range d.Suggestions {
		s += fmt.Sprintf("**建议%d (%s)**: %s\n", i+1, sug.Kind, sug.Title)
		if sug.DDL != "" {
			s += "```\n" + sug.DDL + "\n```\n"
		}
	}
	return s
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
