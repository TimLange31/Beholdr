// Package integrations monitors the external telemetry systems Beholdr uses.
// It deliberately owns no telemetry storage: Prometheus remains the metrics
// source, Elasticsearch the logs/traces source, and an OpenTelemetry Collector
// the application telemetry ingress.
package integrations

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Config struct {
	PrometheusURL         string
	PrometheusBearerToken string
	ElasticsearchURL      string
	ElasticsearchAPIKey   string
	CollectorHealthURL    string
	Interval              time.Duration
	Timeout               time.Duration
}

type ProviderStatus struct {
	Name       string  `json:"name"`
	Signal     string  `json:"signal"`
	Configured bool    `json:"configured"`
	Reachable  bool    `json:"reachable"`
	CheckedAt  float64 `json:"checked_at"`
	LatencyMS  int64   `json:"latency_ms"`
	Error      string  `json:"error,omitempty"`
}

type Snapshot struct {
	UpdatedAt float64          `json:"updated_at"`
	Providers []ProviderStatus `json:"providers"`
}

type provider struct {
	name       string
	signal     string
	endpoint   string
	authHeader string
}

type Monitor struct {
	providers []provider
	interval  time.Duration
	client    *http.Client
	log       *slog.Logger

	mu   sync.RWMutex
	snap Snapshot
}

func New(cfg Config, log *slog.Logger) *Monitor {
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}

	providers := []provider{
		{
			name:       "prometheus",
			signal:     "metrics",
			endpoint:   endpoint(cfg.PrometheusURL, "/-/ready"),
			authHeader: bearer(cfg.PrometheusBearerToken),
		},
		{
			name:       "elasticsearch",
			signal:     "logs and traces",
			endpoint:   endpoint(cfg.ElasticsearchURL, "/_cluster/health?local=true"),
			authHeader: apiKey(cfg.ElasticsearchAPIKey),
		},
		{
			name:     "otel-collector",
			signal:   "OTLP ingress",
			endpoint: strings.TrimSpace(cfg.CollectorHealthURL),
		},
	}

	initial := Snapshot{Providers: make([]ProviderStatus, 0, len(providers))}
	for _, p := range providers {
		initial.Providers = append(initial.Providers, ProviderStatus{
			Name: p.name, Signal: p.signal, Configured: p.endpoint != "",
		})
	}

	return &Monitor{
		providers: providers,
		interval:  cfg.Interval,
		client: &http.Client{
			Timeout: cfg.Timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		log:  log,
		snap: initial,
	}
}

// Run checks immediately and then at the configured interval until cancellation.
func (m *Monitor) Run(ctx context.Context) {
	m.Check(ctx)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.Check(ctx)
		}
	}
}

// Check refreshes every provider status. Providers are checked concurrently so
// one slow backend does not delay the others.
func (m *Monitor) Check(ctx context.Context) {
	statuses := make([]ProviderStatus, len(m.providers))
	var wg sync.WaitGroup
	for i, p := range m.providers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = m.check(ctx, p)
		}()
	}
	wg.Wait()

	now := unixSeconds(time.Now())
	m.mu.Lock()
	m.snap = Snapshot{UpdatedAt: now, Providers: statuses}
	m.mu.Unlock()
}

func (m *Monitor) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	providers := make([]ProviderStatus, len(m.snap.Providers))
	copy(providers, m.snap.Providers)
	return Snapshot{UpdatedAt: m.snap.UpdatedAt, Providers: providers}
}

func (m *Monitor) check(ctx context.Context, p provider) ProviderStatus {
	status := ProviderStatus{Name: p.name, Signal: p.signal, Configured: p.endpoint != ""}
	if !status.Configured {
		return status
	}

	checked := time.Now()
	status.CheckedAt = unixSeconds(checked)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint, nil)
	if err != nil {
		status.Error = "invalid endpoint"
		return status
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		status.Error = "endpoint must use http or https"
		return status
	}
	if req.URL.User != nil {
		status.Error = "endpoint must not contain credentials"
		return status
	}
	if p.authHeader != "" {
		req.Header.Set("Authorization", p.authHeader)
	}

	start := time.Now()
	resp, err := m.client.Do(req)
	status.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		status.Error = "connection failed"
		m.log.Warn("integration health check failed", "provider", p.name, "err", err)
		return status
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status.Error = fmt.Sprintf("unexpected HTTP status %d", resp.StatusCode)
		return status
	}
	status.Reachable = true
	return status
}

func endpoint(base, path string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	ref, err := url.Parse(path)
	if err != nil {
		return base
	}
	u.Path = strings.TrimRight(u.Path, "/") + ref.Path
	u.RawPath = ""
	u.RawQuery = ref.RawQuery
	u.Fragment = ""
	return u.String()
}

func bearer(token string) string {
	if token = strings.TrimSpace(token); token != "" {
		return "Bearer " + token
	}
	return ""
}

func apiKey(key string) string {
	if key = strings.TrimSpace(key); key != "" {
		return "ApiKey " + key
	}
	return ""
}

func unixSeconds(t time.Time) float64 {
	return float64(t.UnixNano()) / 1e9
}
