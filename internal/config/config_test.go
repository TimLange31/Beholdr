package config

import (
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
