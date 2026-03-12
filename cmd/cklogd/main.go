package main

import (
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/audric/heros-cklog/internal/config"
	"github.com/audric/heros-cklog/internal/ingester"
)

func main() {
	cfgPath := flag.String("config", "cklogd.ini", "path to ini configuration file")
	flag.Parse()

	cfg := config.Default()
	if err := config.Load(*cfgPath, cfg); err != nil {
		slog.Error("failed to load config", "path", *cfgPath, "err", err)
		os.Exit(1)
	}

	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	slog.Info("config loaded",
		"dbdir", cfg.DBDir,
		"retain_months", cfg.RetainMonths,
		"logs", len(cfg.Logs),
	)

	ing, err := ingester.New(cfg)
	if err != nil {
		slog.Error("failed to create ingester", "err", err)
		os.Exit(1)
	}

	slog.Info("scanning existing files", "watching", strings.Join(ing.WatchedPaths(), ", "))
	ing.ScanExisting()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sig
		slog.Info("shutting down")
		ing.Close()
	}()

	ing.Run()
	slog.Info("done")
}
