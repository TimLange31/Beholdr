// Package config holds runtime configuration, all overridable via env vars.
package config

import (
	"fmt"
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
	PrometheusQueryTimeout    time.Duration // per-call timeout for Prometheus range/instant queries
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

	// ServiceHealth configures the Prometheus-backed per-service signals.
	ServiceHealth ServiceHealthConfig

	// Problems collects configuration that was supplied but unusable — an
	// unparseable threshold, say. Load never guesses what was meant: the
	// process refuses to start instead, because a silently ignored threshold
	// is a health badge that is quietly wrong.
	Problems []string
}

// ServiceHealthConfig names the metrics, labels and thresholds the per-service
// health signals are built from. It is transport- and package-agnostic so
// config keeps no dependency on the servicehealth package; cmd/beholdr adapts
// it, the same way it does for TLSConfig.
type ServiceHealthConfig struct {
	HTTPRequestsMetric string
	HTTPErrorsMetric   string
	HTTPStatusLabel    string
	AppNamespaceLabel  string
	AppServiceLabel    string
	AppPodLabel        string
	KubeNamespaceLabel string
	KubePodLabel       string
	// CPUBasis is "limits" or "requests" — what CPU usage is scored against.
	CPUBasis string

	ErrorRateWarning      float64
	ErrorRateCritical     float64
	ErrorIncreaseWarning  float64
	ErrorIncreaseCritical float64
	CPUWarning            float64
	CPUCritical           float64
	MemoryWarning         float64
	MemoryCritical        float64
	FailingPodsWarning    float64
	FailingPodsCritical   float64

	CacheTTL             time.Duration
	MaxConcurrentQueries int
}

// Validate reports configuration that was supplied but could not be used.
func (c Config) Validate() error {
	if len(c.Problems) == 0 {
		return nil
	}
	return fmt.Errorf("invalid configuration: %s", strings.Join(c.Problems, "; "))
}

// TLSConfig is how one outbound integration verifies its backend.
type TLSConfig struct {
	CAFile   string // PEM bundle appended to the system roots; empty = system roots only
	Insecure bool   // skip verification entirely — never in production
}

func Load() Config {
	var problems []string
	cfg := Config{
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
		PrometheusQueryTimeout:    time.Duration(envInt("BEHOLDR_PROMETHEUS_QUERY_TIMEOUT", 30)) * time.Second,
		PrometheusTLS:             tlsConfig("BEHOLDR_PROMETHEUS"),
		ElasticsearchTLS:          tlsConfig("BEHOLDR_ELASTICSEARCH"),
		OTelCollectorTLS:          tlsConfig("BEHOLDR_OTEL_COLLECTOR"),
	}
	cfg.ServiceHealth = ServiceHealthConfig{
		HTTPRequestsMetric: env("BEHOLDR_SERVICE_HTTP_REQUESTS_METRIC", ""),
		HTTPErrorsMetric:   env("BEHOLDR_SERVICE_HTTP_ERRORS_METRIC", ""),
		HTTPStatusLabel:    env("BEHOLDR_SERVICE_HTTP_STATUS_LABEL", ""),
		AppNamespaceLabel:  env("BEHOLDR_SERVICE_APP_NAMESPACE_LABEL", ""),
		AppServiceLabel:    env("BEHOLDR_SERVICE_APP_SERVICE_LABEL", ""),
		AppPodLabel:        env("BEHOLDR_SERVICE_APP_POD_LABEL", ""),
		KubeNamespaceLabel: env("BEHOLDR_SERVICE_KUBE_NAMESPACE_LABEL", ""),
		KubePodLabel:       env("BEHOLDR_SERVICE_KUBE_POD_LABEL", ""),
		CPUBasis:           env("BEHOLDR_SERVICE_CPU_BASIS", ""),

		ErrorRateWarning:      envFloat("BEHOLDR_SERVICE_ERROR_RATE_WARNING", &problems),
		ErrorRateCritical:     envFloat("BEHOLDR_SERVICE_ERROR_RATE_CRITICAL", &problems),
		ErrorIncreaseWarning:  envFloat("BEHOLDR_SERVICE_ERROR_INCREASE_WARNING", &problems),
		ErrorIncreaseCritical: envFloat("BEHOLDR_SERVICE_ERROR_INCREASE_CRITICAL", &problems),
		CPUWarning:            envFloat("BEHOLDR_SERVICE_CPU_WARNING", &problems),
		CPUCritical:           envFloat("BEHOLDR_SERVICE_CPU_CRITICAL", &problems),
		MemoryWarning:         envFloat("BEHOLDR_SERVICE_MEMORY_WARNING", &problems),
		MemoryCritical:        envFloat("BEHOLDR_SERVICE_MEMORY_CRITICAL", &problems),
		FailingPodsWarning:    envFloat("BEHOLDR_SERVICE_FAILING_PODS_WARNING", &problems),
		FailingPodsCritical:   envFloat("BEHOLDR_SERVICE_FAILING_PODS_CRITICAL", &problems),

		CacheTTL:             time.Duration(envInt("BEHOLDR_SERVICE_METRICS_CACHE_TTL", 30)) * time.Second,
		MaxConcurrentQueries: envInt("BEHOLDR_SERVICE_MAX_CONCURRENT_QUERIES", 6),
	}
	cfg.Problems = problems
	return cfg
}

// envFloat returns 0 when the variable is unset — "0" means "take the default"
// throughout ServiceHealthConfig. A value that is set but unparseable is
// recorded as a problem rather than falling back, so a mistyped threshold stops
// the process instead of quietly reverting to a different one.
func envFloat(k string, problems *[]string) float64 {
	v, ok := os.LookupEnv(k)
	if !ok || strings.TrimSpace(v) == "" {
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s is not a number: %q", k, v))
		return 0
	}
	return f
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
