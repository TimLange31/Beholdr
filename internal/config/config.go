// Package config holds runtime configuration, all overridable via env vars.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr                      string        // listen address
	PollInterval              time.Duration // how often to poll the cluster
	HistorySize               int           // samples retained per series
	Namespaces                []string      // restrict monitoring; empty = all
	KubeMode                  string        // "auto" | "in-cluster" | "kubeconfig"
	Kubeconfig                string        // path when KubeMode != in-cluster
	CORSOrigins               []string      // explicit CORS allowlist; empty = CORS disabled (the default)
	RequestTimout             time.Duration // per-call timeout to the API server
	PrometheusURL             string        // Prometheus base URL; the check calls /-/ready beneath it
	PrometheusBearerToken     string        // optional bearer token; never exposed through the API
	ElasticsearchURL          string        // Elasticsearch base URL; the check calls /_cluster/health beneath it
	ElasticsearchAPIKey       string        // optional API key; never exposed through the API
	OTelCollectorHealthURL    string        // Collector health_check extension URL
	IntegrationCheckInterval  time.Duration // how often external systems are checked
	IntegrationRequestTimeout time.Duration // per-call timeout for external systems

	// Outbound TLS trust, per integration. CAFile extends the system trust
	// store (private CA, ECK-issued cert, Cloudflare Origin CA); Insecure
	// disables verification entirely and is logged loudly at startup.
	PrometheusTLS    TLSConfig
	ElasticsearchTLS TLSConfig
	OTelCollectorTLS TLSConfig
}

// TLSConfig is how one outbound integration verifies its backend.
type TLSConfig struct {
	CAFile   string // PEM bundle appended to the system roots; empty = system roots only
	Insecure bool   // skip verification entirely — never in production
}

func Load() Config {
	return Config{
		Addr:         env("BEHOLDR_ADDR", ":8000"),
		PollInterval: time.Duration(envInt("BEHOLDR_POLL_INTERVAL", 15)) * time.Second,
		HistorySize:  envInt("BEHOLDR_HISTORY_SIZE", 240),
		Namespaces:   splitCSV(env("BEHOLDR_NAMESPACES", "")),
		KubeMode:     env("BEHOLDR_KUBE_MODE", "auto"),
		Kubeconfig:   env("KUBECONFIG", ""),
		// No default origins: Beholdr exposes cluster topology, pod names,
		// and resource usage, so cross-origin access must be opted into
		// explicitly. Set to "*" only for local development.
		CORSOrigins:               splitCSV(env("BEHOLDR_CORS_ORIGINS", "")),
		RequestTimout:             time.Duration(envInt("BEHOLDR_REQUEST_TIMEOUT", 10)) * time.Second,
		PrometheusURL:             env("BEHOLDR_PROMETHEUS_URL", ""),
		PrometheusBearerToken:     env("BEHOLDR_PROMETHEUS_BEARER_TOKEN", ""),
		ElasticsearchURL:          env("BEHOLDR_ELASTICSEARCH_URL", ""),
		ElasticsearchAPIKey:       env("BEHOLDR_ELASTICSEARCH_API_KEY", ""),
		OTelCollectorHealthURL:    env("BEHOLDR_OTEL_COLLECTOR_HEALTH_URL", ""),
		IntegrationCheckInterval:  time.Duration(envInt("BEHOLDR_INTEGRATION_CHECK_INTERVAL", 30)) * time.Second,
		IntegrationRequestTimeout: time.Duration(envInt("BEHOLDR_INTEGRATION_REQUEST_TIMEOUT", 5)) * time.Second,
		PrometheusTLS:             tlsConfig("BEHOLDR_PROMETHEUS"),
		ElasticsearchTLS:          tlsConfig("BEHOLDR_ELASTICSEARCH"),
		OTelCollectorTLS:          tlsConfig("BEHOLDR_OTEL_COLLECTOR"),
	}
}

func tlsConfig(prefix string) TLSConfig {
	return TLSConfig{
		CAFile:   env(prefix+"_CA_FILE", ""),
		Insecure: envBool(prefix+"_TLS_INSECURE", false),
	}
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, ok := os.LookupEnv(k); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// envBool accepts the strconv.ParseBool vocabulary (1/t/T/true/TRUE, 0/f/false,
// ...). Anything unparseable falls back to the default rather than silently
// reading as true, so a typo can never switch verification off.
func envBool(k string, def bool) bool {
	if v, ok := os.LookupEnv(k); ok {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	return def
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
