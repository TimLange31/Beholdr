// Command beholdr runs the monitor collector and serves the API + UI.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/delangetimm/beholdr/internal/api"
	"github.com/delangetimm/beholdr/internal/collect"
	"github.com/delangetimm/beholdr/internal/config"
	"github.com/delangetimm/beholdr/internal/k8s"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg := config.Load()

	client, err := k8s.New(cfg.KubeMode, cfg.Kubeconfig, cfg.Namespaces, log)
	if err != nil {
		log.Error("kubernetes client init failed", "err", err)
		os.Exit(1)
	}

	col := collect.New(
		client, cfg.PollInterval, cfg.RequestTimout, cfg.HistorySize,
		func() bool { return client.MetricsAvailable }, log,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go col.Run(ctx)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.NewServer(col, cfg.AllowCORS, log).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Info("listening", "addr", cfg.Addr, "poll", cfg.PollInterval.String())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}
