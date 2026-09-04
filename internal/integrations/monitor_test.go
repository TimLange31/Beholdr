package integrations

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestCheckReportsConfiguredProvidersAndUsesCredentials(t *testing.T) {
	requests := make(chan *http.Request, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Clone(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	monitor := New(Config{
		PrometheusURL:         server.URL,
		PrometheusBearerToken: "prom-token",
		ElasticsearchURL:      server.URL,
		ElasticsearchAPIKey:   "elastic-key",
		CollectorHealthURL:    server.URL + "/collector-health",
		Timeout:               time.Second,
	}, discardLogger())
	monitor.Check(context.Background())

	snapshot := monitor.Snapshot()
	if snapshot.UpdatedAt == 0 {
		t.Fatal("expected snapshot update timestamp")
	}
	if len(snapshot.Providers) != 3 {
		t.Fatalf("want 3 providers, got %d", len(snapshot.Providers))
	}
	for _, status := range snapshot.Providers {
		if !status.Configured || !status.Reachable {
			t.Errorf("provider %s should be configured and reachable: %+v", status.Name, status)
		}
	}

	seen := map[string]string{}
	for range 3 {
		r := <-requests
		seen[r.URL.Path] = r.Header.Get("Authorization")
	}
	if seen["/-/ready"] != "Bearer prom-token" {
		t.Errorf("unexpected Prometheus authorization header %q", seen["/-/ready"])
	}
	if seen["/_cluster/health"] != "ApiKey elastic-key" {
		t.Errorf("unexpected Elasticsearch authorization header %q", seen["/_cluster/health"])
	}
	if seen["/collector-health"] != "" {
		t.Error("Collector health check must not receive backend credentials")
	}
}

func TestCheckDoesNotExposeConnectionDetails(t *testing.T) {
	monitor := New(Config{
		PrometheusURL:         "not a URL",
		PrometheusBearerToken: "top-secret",
	}, discardLogger())
	monitor.Check(context.Background())

	status := monitor.Snapshot().Providers[0]
	if !status.Configured {
		t.Fatal("invalid but non-empty endpoint should be reported as configured")
	}
	if status.Reachable {
		t.Fatal("invalid endpoint cannot be reachable")
	}
	if status.Error == "" || strings.Contains(status.Error, "top-secret") || strings.Contains(status.Error, "not a URL") {
		t.Fatalf("want a sanitized endpoint error, got %q", status.Error)
	}
}

func TestUnconfiguredProvidersAreExplicit(t *testing.T) {
	monitor := New(Config{}, discardLogger())
	monitor.Check(context.Background())

	for _, status := range monitor.Snapshot().Providers {
		if status.Configured || status.Reachable || status.CheckedAt != 0 || status.Error != "" {
			t.Errorf("unexpected state for unconfigured provider: %+v", status)
		}
	}
}

func TestUnexpectedStatusIsReportedWithoutResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "sensitive backend response", http.StatusUnauthorized)
	}))
	defer server.Close()

	monitor := New(Config{CollectorHealthURL: server.URL}, discardLogger())
	monitor.Check(context.Background())

	status := monitor.Snapshot().Providers[2]
	if status.Error != "unexpected HTTP status 401" {
		t.Fatalf("unexpected sanitized error %q", status.Error)
	}
}
