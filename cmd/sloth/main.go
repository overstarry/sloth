// Command sloth is a slow-SQL diagnosis agent for PostgreSQL: it samples
// pg_stat_statements, diagnoses the slowest statements with an LLM, and pushes
// alerts to WeCom/Feishu/Lark while serving a dashboard API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/overstarry/sloth/internal/analyzer"
	"github.com/overstarry/sloth/internal/api"
	"github.com/overstarry/sloth/internal/app"
	"github.com/overstarry/sloth/internal/collector"
	"github.com/overstarry/sloth/internal/config"
	"github.com/overstarry/sloth/internal/inspect"
	"github.com/overstarry/sloth/internal/llm"
	"github.com/overstarry/sloth/internal/notify"
	"github.com/overstarry/sloth/internal/store"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(*cfgPath, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(cfgPath string, log *slog.Logger) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Storage.
	st, err := store.New(ctx, cfg.Store.DSN, cfg.Store.MaxConns)
	if err != nil {
		return err
	}
	defer st.Close()

	// One collector + inspector per monitored target instance.
	collectors := make([]*collector.Collector, 0, len(cfg.Targets))
	inspectors := make(map[string]*inspect.Inspector, len(cfg.Targets))
	for _, t := range cfg.Targets {
		coll, err := collector.New(ctx, t, cfg.Collector, st, log)
		if err != nil {
			return fmt.Errorf("target %q: %w", t.Name, err)
		}
		defer coll.Close()
		if err := coll.Preflight(ctx); err != nil {
			return fmt.Errorf("target %q preflight: %w", t.Name, err)
		}
		collectors = append(collectors, coll)
		inspectors[t.Name] = inspect.New(coll.TargetPool())
	}

	// LLM + analyzer.
	provider, err := llm.New(cfg.LLM)
	if err != nil {
		return err
	}
	an := analyzer.New(provider)

	// Notifier.
	dispatcher, err := notify.NewDispatcher(cfg.Notify, st, log)
	if err != nil {
		return err
	}

	application := app.New(st, inspectors, an, dispatcher, "http://localhost"+cfg.Server.Addr, log)

	// Background collection loop, one goroutine per target.
	for _, coll := range collectors {
		coll := coll
		go func() {
			if err := coll.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Error("collector stopped", "instance", coll.Instance(), "err", err)
			}
		}()
	}

	// HTTP server.
	srv := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           api.New(application).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("http server listening", "addr", cfg.Server.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server error", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutCtx)
}
