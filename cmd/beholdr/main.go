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
	"github.com/delangetimm/beholdr/internal/integrations"
	"github.com/delangetimm/beholdr/internal/k8s"
	"github.com/delangetimm/beholdr/internal/servicehealth"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Error("configuration", "err", err)
		os.Exit(1)
	}

	client, err := k8s.New(cfg.KubeMode, cfg.Kubeconfig, cfg.Namespaces, log)
	if err != nil {
		log.Error("kubernetes client init failed", "err", err)
		os.Exit(1)
	}

	col := collect.New(
		client, cfg.PollInterval, cfg.RequestTimout, cfg.HistorySize,
		func() bool { return client.MetricsAvailable }, log,
	)
	integrationMonitor := integrations.New(integrations.Config{
		PrometheusURL:         cfg.PrometheusURL,
		PrometheusBearerToken: cfg.PrometheusBearerToken,
		PrometheusTLS:         integrationTLS(cfg.PrometheusTLS),
		ElasticsearchURL:      cfg.ElasticsearchURL,
		ElasticsearchAPIKey:   cfg.ElasticsearchAPIKey,
		ElasticsearchTLS:      integrationTLS(cfg.ElasticsearchTLS),
		CollectorHealthURL:    cfg.OTelCollectorHealthURL,
		CollectorTLS:          integrationTLS(cfg.OTelCollectorTLS),
		Interval:              cfg.IntegrationCheckInterval,
		Timeout:               cfg.IntegrationRequestTimeout,
		QueryTimeout:          cfg.PrometheusQueryTimeout,
	}, log)

	// Built here rather than inside the API server so an unusable metric
	// profile stops the process at startup, where an operator will see it,
	// instead of turning every service-health request into a 502.
	health, err := servicehealth.New(integrationMonitor, serviceHealthConfig(cfg.ServiceHealth))
	if err != nil {
		log.Error("service health configuration", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go col.Run(ctx)
	go integrationMonitor.Run(ctx)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.NewServer(col, integrationMonitor, health, cfg.CORSOrigins, log).Handler(),
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

// serviceHealthConfig adapts the config package's transport-agnostic settings
// to the servicehealth package, keeping config free of any dependency on it.
// Empty and zero fields are left alone: servicehealth owns the defaults and
// rejects anything set but unusable.
func serviceHealthConfig(c config.ServiceHealthConfig) servicehealth.Config {
	return servicehealth.Config{
		HTTPRequestsMetric: c.HTTPRequestsMetric,
		HTTPErrorsMetric:   c.HTTPErrorsMetric,
		HTTPStatusLabel:    c.HTTPStatusLabel,
		AppNamespaceLabel:  c.AppNamespaceLabel,
		AppServiceLabel:    c.AppServiceLabel,
		AppPodLabel:        c.AppPodLabel,
		KubeNamespaceLabel: c.KubeNamespaceLabel,
		KubePodLabel:       c.KubePodLabel,
		CPUBasis:           servicehealth.CPUBasis(c.CPUBasis),
		Thresholds: servicehealth.Thresholds{
			ErrorRateWarning:      c.ErrorRateWarning,
			ErrorRateCritical:     c.ErrorRateCritical,
			ErrorIncreaseWarning:  c.ErrorIncreaseWarning,
			ErrorIncreaseCritical: c.ErrorIncreaseCritical,
			CPUWarning:            c.CPUWarning,
			CPUCritical:           c.CPUCritical,
			MemoryWarning:         c.MemoryWarning,
			MemoryCritical:        c.MemoryCritical,
			FailingPodsWarning:    c.FailingPodsWarning,
			FailingPodsCritical:   c.FailingPodsCritical,
		},
		CacheTTL:             c.CacheTTL,
		MaxConcurrentQueries: c.MaxConcurrentQueries,
	}
}

// integrationTLS adapts the config package's transport-agnostic TLS settings to
// the integrations package, keeping config free of any dependency on it.
func integrationTLS(t config.TLSConfig) integrations.TLS {
	return integrations.TLS{CAFile: t.CAFile, Insecure: t.Insecure}
}
