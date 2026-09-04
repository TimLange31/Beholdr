package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaultsAreSecure(t *testing.T) {
	t.Setenv("BEHOLDR_CORS_ORIGINS", "")
	cfg := Load()
	if len(cfg.CORSOrigins) != 0 {
		t.Fatalf("CORS must be disabled by default, got origins=%v", cfg.CORSOrigins)
	}
	if cfg.Addr != ":8000" {
		t.Errorf("want default addr :8000, got %q", cfg.Addr)
	}
}

func TestLoadCORSOriginsParsesTrimmedCSV(t *testing.T) {
	t.Setenv("BEHOLDR_CORS_ORIGINS", "https://a.example.com, https://b.example.com ,")
	cfg := Load()
	want := []string{"https://a.example.com", "https://b.example.com"}
	if len(cfg.CORSOrigins) != len(want) {
		t.Fatalf("want %v, got %v", want, cfg.CORSOrigins)
	}
	for i := range want {
		if cfg.CORSOrigins[i] != want[i] {
			t.Errorf("origin %d: want %q, got %q", i, want[i], cfg.CORSOrigins[i])
		}
	}
}

func TestEnvIntFallsBackOnInvalidValue(t *testing.T) {
	t.Setenv("BEHOLDR_POLL_INTERVAL", "not-a-number")
	cfg := Load()
	if cfg.PollInterval.Seconds() != 15 {
		t.Errorf("want default poll interval on invalid input, got %v", cfg.PollInterval)
	}
}

func TestLoadObservabilityIntegrations(t *testing.T) {
	t.Setenv("BEHOLDR_PROMETHEUS_URL", "https://prometheus.example.com")
	t.Setenv("BEHOLDR_PROMETHEUS_BEARER_TOKEN", "prom-secret")
	t.Setenv("BEHOLDR_ELASTICSEARCH_URL", "https://elastic.example.com")
	t.Setenv("BEHOLDR_ELASTICSEARCH_API_KEY", "elastic-secret")
	t.Setenv("BEHOLDR_OTEL_COLLECTOR_HEALTH_URL", "http://otel-collector:13133/")
	t.Setenv("BEHOLDR_INTEGRATION_CHECK_INTERVAL", "45")
	t.Setenv("BEHOLDR_INTEGRATION_REQUEST_TIMEOUT", "7")

	cfg := Load()
	if cfg.PrometheusURL != "https://prometheus.example.com" {
		t.Errorf("unexpected Prometheus URL %q", cfg.PrometheusURL)
	}
	if cfg.PrometheusBearerToken != "prom-secret" {
		t.Error("Prometheus bearer token was not loaded")
	}
	if cfg.ElasticsearchURL != "https://elastic.example.com" {
		t.Errorf("unexpected Elasticsearch URL %q", cfg.ElasticsearchURL)
	}
	if cfg.ElasticsearchAPIKey != "elastic-secret" {
		t.Error("Elasticsearch API key was not loaded")
	}
	if cfg.OTelCollectorHealthURL != "http://otel-collector:13133/" {
		t.Errorf("unexpected Collector health URL %q", cfg.OTelCollectorHealthURL)
	}
	if cfg.IntegrationCheckInterval != 45*time.Second {
		t.Errorf("unexpected integration interval %s", cfg.IntegrationCheckInterval)
	}
	if cfg.IntegrationRequestTimeout != 7*time.Second {
		t.Errorf("unexpected integration timeout %s", cfg.IntegrationRequestTimeout)
	}
}

func TestLoadIntegrationTLSSettings(t *testing.T) {
	t.Setenv("BEHOLDR_ELASTICSEARCH_CA_FILE", "/etc/beholdr/ca/elastic.pem")
	t.Setenv("BEHOLDR_ELASTICSEARCH_TLS_INSECURE", "true")
	t.Setenv("BEHOLDR_PROMETHEUS_CA_FILE", "/etc/beholdr/ca/prom.pem")

	cfg := Load()
	if cfg.ElasticsearchTLS.CAFile != "/etc/beholdr/ca/elastic.pem" {
		t.Errorf("unexpected Elasticsearch CA file %q", cfg.ElasticsearchTLS.CAFile)
	}
	if !cfg.ElasticsearchTLS.Insecure {
		t.Error("BEHOLDR_ELASTICSEARCH_TLS_INSECURE was not honoured")
	}
	if cfg.PrometheusTLS.CAFile != "/etc/beholdr/ca/prom.pem" {
		t.Errorf("unexpected Prometheus CA file %q", cfg.PrometheusTLS.CAFile)
	}
	if cfg.PrometheusTLS.Insecure || cfg.OTelCollectorTLS.Insecure {
		t.Error("TLS verification must stay on unless explicitly disabled")
	}
}

// A typo must never be the thing that switches certificate verification off.
func TestInsecureTLSDefaultsToOffOnGarbageInput(t *testing.T) {
	for _, v := range []string{"", "yes", "TRUE!", "on", "enabled"} {
		t.Setenv("BEHOLDR_ELASTICSEARCH_TLS_INSECURE", v)
		if Load().ElasticsearchTLS.Insecure {
			t.Errorf("%q must not enable insecure TLS", v)
		}
	}
	// Surrounding whitespace is tolerated: YAML block scalars add it easily.
	for _, v := range []string{"true", "TRUE", "1", "t", " true "} {
		t.Setenv("BEHOLDR_ELASTICSEARCH_TLS_INSECURE", v)
		if !Load().ElasticsearchTLS.Insecure {
			t.Errorf("%q should enable insecure TLS", v)
		}
	}
}

func TestServiceHealthDefaultsAreUnsetSoThePackageOwnsThem(t *testing.T) {
	cfg := Load()
	sh := cfg.ServiceHealth
	if sh.HTTPRequestsMetric != "" || sh.KubePodLabel != "" || sh.CPUBasis != "" {
		t.Fatalf("unset service-health fields must stay empty so servicehealth applies its own defaults: %+v", sh)
	}
	if sh.ErrorRateWarning != 0 || sh.CPUCritical != 0 {
		t.Fatalf("unset thresholds must be zero: %+v", sh)
	}
	if sh.CacheTTL != 30*time.Second || sh.MaxConcurrentQueries != 6 {
		t.Fatalf("unexpected query-shaping defaults: %+v", sh)
	}
	if cfg.PrometheusQueryTimeout != 30*time.Second {
		t.Fatalf("query timeout must not inherit the 5s health-check timeout: %v", cfg.PrometheusQueryTimeout)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default configuration must be valid: %v", err)
	}
}

func TestServiceHealthOverridesAreRead(t *testing.T) {
	t.Setenv("BEHOLDR_SERVICE_HTTP_REQUESTS_METRIC", "http_server_request_duration_seconds_count")
	t.Setenv("BEHOLDR_SERVICE_HTTP_STATUS_LABEL", "http_response_status_code")
	t.Setenv("BEHOLDR_SERVICE_APP_SERVICE_LABEL", "service_name")
	t.Setenv("BEHOLDR_SERVICE_CPU_BASIS", "requests")
	t.Setenv("BEHOLDR_SERVICE_CPU_WARNING", "150")
	t.Setenv("BEHOLDR_SERVICE_CPU_CRITICAL", "300")
	t.Setenv("BEHOLDR_PROMETHEUS_QUERY_TIMEOUT", "45")

	cfg := Load()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	sh := cfg.ServiceHealth
	if sh.HTTPRequestsMetric != "http_server_request_duration_seconds_count" ||
		sh.HTTPStatusLabel != "http_response_status_code" ||
		sh.AppServiceLabel != "service_name" || sh.CPUBasis != "requests" {
		t.Fatalf("metric profile not read: %+v", sh)
	}
	if sh.CPUWarning != 150 || sh.CPUCritical != 300 {
		t.Fatalf("thresholds not read: %+v", sh)
	}
	if cfg.PrometheusQueryTimeout != 45*time.Second {
		t.Fatalf("query timeout not read: %v", cfg.PrometheusQueryTimeout)
	}
}

// A mistyped threshold must stop the process. Falling back to the default
// would leave an operator believing they had set a threshold they had not.
func TestUnparseableThresholdIsRefusedNotIgnored(t *testing.T) {
	t.Setenv("BEHOLDR_SERVICE_MEMORY_CRITICAL", "ninety-five")
	cfg := Load()
	err := cfg.Validate()
	if err == nil {
		t.Fatal("want a configuration error")
	}
	if !strings.Contains(err.Error(), "BEHOLDR_SERVICE_MEMORY_CRITICAL") {
		t.Fatalf("error should name the variable: %v", err)
	}
}
