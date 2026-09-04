package integrations

import (
	"context"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestEndpointJoinsBaseAndPath(t *testing.T) {
	cases := []struct{ base, path, want string }{
		{"https://p.example.com", "/-/ready", "https://p.example.com/-/ready"},
		{"https://p.example.com/", "/-/ready", "https://p.example.com/-/ready"},
		{"  https://p.example.com  ", "/-/ready", "https://p.example.com/-/ready"},
		{"https://p.example.com/prometheus", "/-/ready", "https://p.example.com/prometheus/-/ready"},
		{"https://p.example.com:9090", "/-/ready", "https://p.example.com:9090/-/ready"},
		{"https://es.example.com", "/_cluster/health?local=true", "https://es.example.com/_cluster/health?local=true"},
		{"https://es.example.com/?pretty=1", "/_cluster/health?local=true", "https://es.example.com/_cluster/health?local=true"},
		{"", "/-/ready", ""},
	}
	for _, c := range cases {
		if got := endpoint(c.base, c.path); got != c.want {
			t.Errorf("endpoint(%q, %q) = %q, want %q", c.base, c.path, got, c.want)
		}
	}
}

// A schemeless or credential-bearing endpoint must fail closed rather than be
// silently coerced into a request.
func TestMalformedEndpointsFailClosed(t *testing.T) {
	cases := []struct{ url, want string }{
		{"es.example.com:9200", "endpoint must use http or https"},
		{"ftp://es.example.com", "endpoint must use http or https"},
		{"https://user:pw@es.example.com", "endpoint must not contain credentials"},
	}
	for _, c := range cases {
		m := New(Config{ElasticsearchURL: c.url}, discardLogger())
		m.Check(context.Background())
		if got := m.Snapshot().Providers[1].Error; got != c.want {
			t.Errorf("%s: got %q, want %q", c.url, got, c.want)
		}
	}
}

func writeCA(t *testing.T, server *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	bundle := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(path, bundle, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrivateCAIsTrustedWhenBundleIsSupplied(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	m := New(Config{
		CollectorHealthURL: server.URL,
		CollectorTLS:       TLS{CAFile: writeCA(t, server)},
		Timeout:            5 * time.Second,
	}, discardLogger())
	m.Check(context.Background())

	status := m.Snapshot().Providers[2]
	if !status.Reachable {
		t.Fatalf("want reachable over a privately-signed cert, got %+v", status)
	}
	if status.TLSSkipVerify {
		t.Error("verification was on; the status must not claim otherwise")
	}
}

func TestUnknownAuthorityErrorTellsTheOperatorWhatToDo(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer server.Close()

	m := New(Config{CollectorHealthURL: server.URL, Timeout: 5 * time.Second}, discardLogger())
	m.Check(context.Background())

	status := m.Snapshot().Providers[2]
	if status.Reachable {
		t.Fatal("an untrusted certificate must not read as reachable")
	}
	if !strings.Contains(status.Error, "unknown authority") {
		t.Fatalf("want an actionable CA error, got %q", status.Error)
	}
	if strings.Contains(status.Error, server.URL) {
		t.Fatalf("error leaked the endpoint: %q", status.Error)
	}
}

func TestInsecureSkipVerifyWorksButIsAdvertised(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	m := New(Config{
		CollectorHealthURL: server.URL,
		CollectorTLS:       TLS{Insecure: true},
		Timeout:            5 * time.Second,
	}, discardLogger())
	m.Check(context.Background())

	status := m.Snapshot().Providers[2]
	if !status.Reachable {
		t.Fatalf("skip-verify should connect, got %+v", status)
	}
	if !status.TLSSkipVerify {
		t.Fatal("unverified TLS must be visible in the API, not silent")
	}
}

// One provider's trust settings must never leak into another's client.
func TestTLSSettingsAreScopedToOneProvider(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	m := New(Config{
		PrometheusURL:      server.URL,
		CollectorHealthURL: server.URL,
		CollectorTLS:       TLS{Insecure: true},
		Timeout:            5 * time.Second,
	}, discardLogger())
	m.Check(context.Background())

	snap := m.Snapshot()
	if snap.Providers[0].Reachable {
		t.Error("Prometheus verifies certificates and must not borrow the Collector's skip-verify")
	}
	if !snap.Providers[2].Reachable {
		t.Error("the Collector's own skip-verify should still apply")
	}
}

func TestUnreadableCABundleIsNotReportedAsANetworkFault(t *testing.T) {
	m := New(Config{
		CollectorHealthURL: "https://collector.example.com",
		CollectorTLS:       TLS{CAFile: filepath.Join(t.TempDir(), "missing.pem")},
	}, discardLogger())
	m.Check(context.Background())

	status := m.Snapshot().Providers[2]
	if status.Error != "TLS trust store could not be loaded" {
		t.Fatalf("want a distinct configuration error, got %q", status.Error)
	}
}

func TestElasticsearchReportsClusterHealthNotJustReachability(t *testing.T) {
	cases := []struct {
		body         string
		wantDetail   string
		wantDegraded bool
	}{
		{`{"status":"green"}`, "cluster status green", false},
		{`{"status":"yellow"}`, "cluster status yellow", false},
		{`{"status":"red"}`, "cluster status red", true},
		// Anything outside the documented vocabulary is dropped rather than
		// echoed: the API must never relay upstream-controlled text.
		{`{"status":"<script>alert(1)</script>"}`, "", false},
		{`not json at all`, "", false},
	}
	for _, c := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, c.body)
		}))
		m := New(Config{ElasticsearchURL: server.URL, Timeout: time.Second}, discardLogger())
		m.Check(context.Background())
		server.Close()

		status := m.Snapshot().Providers[1]
		if !status.Reachable {
			t.Errorf("%s: a 200 is still reachable", c.body)
		}
		if status.Detail != c.wantDetail || status.Degraded != c.wantDegraded {
			t.Errorf("%s: got detail %q degraded %v, want %q %v",
				c.body, status.Detail, status.Degraded, c.wantDetail, c.wantDegraded)
		}
	}
}

// Shutdown cancels the context mid-flight; the failures that produces are an
// artefact of stopping, not an outage, and must not reach the UI during the
// server's graceful-shutdown window.
func TestCancelledCheckKeepsTheLastGoodSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	m := New(Config{CollectorHealthURL: server.URL, Timeout: time.Second}, discardLogger())
	m.Check(context.Background())
	good := m.Snapshot()
	if !good.Providers[2].Reachable {
		t.Fatal("precondition: provider should be reachable")
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	m.Check(cancelled)

	after := m.Snapshot()
	if after.UpdatedAt != good.UpdatedAt || !after.Providers[2].Reachable {
		t.Fatalf("a cancelled check overwrote the last good snapshot: %+v", after.Providers[2])
	}
}

func TestRunChecksImmediatelyAndStopsOnCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	m := New(Config{CollectorHealthURL: server.URL, Interval: time.Hour, Timeout: time.Second}, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()

	deadline := time.After(5 * time.Second)
	for m.Snapshot().UpdatedAt == 0 {
		select {
		case <-deadline:
			t.Fatal("Run did not perform its first check before the interval elapsed")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}
