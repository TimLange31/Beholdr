// Package integrations monitors the external telemetry systems Beholdr uses.
// It deliberately owns no telemetry storage: Prometheus remains the metrics
// source, Elasticsearch the logs/traces source, and an OpenTelemetry Collector
// the application telemetry ingress.
//
// Nothing an upstream returns is ever echoed through Beholdr's own API. Errors
// are mapped onto a closed vocabulary (see classify) and upstream response
// bodies are only ever inspected for a known set of literal values, so a
// hostile or misconfigured backend cannot use Beholdr as a reflector.
package integrations

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// TLS describes how a single provider's certificate should be verified.
//
// CAFile covers the common in-cluster case where the backend presents a
// certificate from a private CA (an ECK-issued Elasticsearch cert, a Cloudflare
// Origin CA cert, a corporate PKI). Insecure disables verification entirely and
// exists only for environments where the CA genuinely cannot be obtained; it is
// logged at startup and surfaced in the API so it cannot quietly become
// permanent.
type TLS struct {
	CAFile   string
	Insecure bool
}

type Config struct {
	PrometheusURL         string
	PrometheusBearerToken string
	PrometheusTLS         TLS

	ElasticsearchURL    string
	ElasticsearchAPIKey string
	ElasticsearchTLS    TLS

	CollectorHealthURL string
	CollectorTLS       TLS

	Interval time.Duration
	Timeout  time.Duration
}

type ProviderStatus struct {
	Name       string `json:"name"`
	Signal     string `json:"signal"`
	Configured bool   `json:"configured"`
	Reachable  bool   `json:"reachable"`
	// Degraded means the backend answered but reported itself unhealthy —
	// reachable and healthy are not the same question.
	Degraded bool `json:"degraded,omitempty"`
	// TLSSkipVerify reports that certificate verification is switched off for
	// this provider. It is deliberately visible in the UI.
	TLSSkipVerify bool    `json:"tls_skip_verify,omitempty"`
	CheckedAt     float64 `json:"checked_at"`
	LatencyMS     int64   `json:"latency_ms"`
	Detail        string  `json:"detail,omitempty"`
	Error         string  `json:"error,omitempty"`
}

type Snapshot struct {
	UpdatedAt float64          `json:"updated_at"`
	Providers []ProviderStatus `json:"providers"`
}

// inspector maps a known-good response body onto a short detail string and
// whether the backend called itself unhealthy. It must only ever return
// literals, never anything derived from the response body.
type inspector func(body []byte) (detail string, degraded bool)

type provider struct {
	name       string
	signal     string
	endpoint   string
	authHeader string
	client     *http.Client
	skipVerify bool
	// setupErr is set when the provider is configured but could not be
	// initialised (an unreadable CA bundle, for example). It short-circuits the
	// check so a broken trust store does not look like a network failure.
	setupErr string
	inspect  inspector
}

type Monitor struct {
	providers       []provider
	prometheusQuery provider
	interval        time.Duration
	log             *slog.Logger

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
			inspect:    elasticsearchHealth,
		},
		{
			name:     "otel-collector",
			signal:   "OTLP ingress",
			endpoint: strings.TrimSpace(cfg.CollectorHealthURL),
		},
	}
	tlsCfg := []TLS{cfg.PrometheusTLS, cfg.ElasticsearchTLS, cfg.CollectorTLS}

	initial := Snapshot{Providers: make([]ProviderStatus, 0, len(providers))}
	for i := range providers {
		p := &providers[i]
		p.skipVerify = tlsCfg[i].Insecure
		client, err := newClient(tlsCfg[i], cfg.Timeout)
		if err != nil {
			p.setupErr = "TLS trust store could not be loaded"
			log.Error("integration TLS configuration is unusable",
				"provider", p.name, "ca_file", tlsCfg[i].CAFile, "err", err)
		}
		p.client = client
		if p.skipVerify && p.endpoint != "" {
			log.Warn("integration TLS verification is DISABLED — traffic to this backend is not authenticated",
				"provider", p.name)
		}
		initial.Providers = append(initial.Providers, ProviderStatus{
			Name: p.name, Signal: p.signal, Configured: p.endpoint != "",
			TLSSkipVerify: p.skipVerify,
		})
	}

	prometheusQuery := providers[0]
	prometheusQuery.endpoint = endpoint(cfg.PrometheusURL, "/api/v1/query_range")

	return &Monitor{
		providers:       providers,
		prometheusQuery: prometheusQuery,
		interval:        cfg.Interval,
		log:             log,
		snap:            initial,
	}
}

// newClient builds a provider-scoped client. Each provider gets its own so one
// backend's trust settings can never apply to another.
func newClient(t TLS, timeout time.Duration) (*http.Client, error) {
	tlsConf := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: t.Insecure} //nolint:gosec // opt-in, surfaced in the API
	var err error
	if ca := strings.TrimSpace(t.CAFile); ca != "" {
		pool, poolErr := caPool(ca)
		if poolErr != nil {
			err = poolErr
		} else {
			tlsConf.RootCAs = pool
		}
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConf
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		// Never follow redirects: a redirect would replay the provider's
		// credentials to whatever host the response points at.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, err
}

// caPool returns the system trust store extended with the supplied bundle, so a
// private CA adds to public trust rather than replacing it.
func caPool(file string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no PEM certificates found in %s", file)
	}
	return pool, nil
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

	// A cancelled context means shutdown, not an outage. Publishing these
	// results would paint every provider red in the UI for the length of the
	// server's graceful-shutdown window.
	if ctx.Err() != nil {
		return
	}

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
	status := ProviderStatus{
		Name: p.name, Signal: p.signal,
		Configured: p.endpoint != "", TLSSkipVerify: p.skipVerify,
	}
	if !status.Configured {
		return status
	}
	if p.setupErr != "" {
		status.CheckedAt = unixSeconds(time.Now())
		status.Error = p.setupErr
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
	resp, err := p.client.Do(req)
	status.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		status.Error = classify(err)
		m.log.Warn("integration health check failed",
			"provider", p.name, "reason", status.Error, "err", err)
		return status
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status.Error = fmt.Sprintf("unexpected HTTP status %d", resp.StatusCode)
		return status
	}
	status.Reachable = true
	if p.inspect != nil {
		status.Detail, status.Degraded = p.inspect(body)
	}
	return status
}

// classify maps a transport error onto a fixed set of operator-actionable
// phrases. The strings are literals: no host, URL, credential or upstream
// detail is ever interpolated. The underlying error goes to the log instead.
func classify(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "check cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	}

	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		var hostErr x509.HostnameError
		if errors.As(certErr.Err, &hostErr) {
			return "TLS certificate does not match the endpoint host"
		}
		var authErr x509.UnknownAuthorityError
		if errors.As(certErr.Err, &authErr) {
			return "TLS certificate signed by an unknown authority — set the CA bundle"
		}
		return "TLS verification failed"
	}
	var recErr tls.RecordHeaderError
	if errors.As(err, &recErr) {
		return "TLS handshake failed — is this endpoint really HTTPS?"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "DNS lookup failed"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "connection refused"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timed out"
	}
	return "connection failed"
}

// elasticsearchHealth reads the cluster status out of a /_cluster/health
// response. Only the three documented values are recognised, so nothing the
// backend sends can reach Beholdr's API verbatim.
func elasticsearchHealth(body []byte) (string, bool) {
	var doc struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", false
	}
	switch strings.ToLower(doc.Status) {
	case "green":
		return "cluster status green", false
	case "yellow":
		return "cluster status yellow", false
	case "red":
		return "cluster status red", true
	}
	return "", false
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
