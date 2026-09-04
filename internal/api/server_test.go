package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/delangetimm/beholdr/internal/collect"
	"github.com/delangetimm/beholdr/internal/integrations"
	"github.com/delangetimm/beholdr/internal/k8s"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeSource is an empty-but-successful (or erroring) Source: enough to
// drive the collector through a poll without a real cluster.
type fakeSource struct{ err error }

func (f *fakeSource) Nodes(context.Context) ([]corev1.Node, error) { return nil, f.err }
func (f *fakeSource) Pods(context.Context) ([]corev1.Pod, error)   { return nil, f.err }
func (f *fakeSource) Deployments(context.Context) ([]appsv1.Deployment, error) {
	return nil, f.err
}
func (f *fakeSource) HPAs(context.Context) ([]autoscalingv1.HorizontalPodAutoscaler, error) {
	return nil, nil
}
func (f *fakeSource) NodeMetrics(context.Context) map[string]k8s.Usage { return nil }
func (f *fakeSource) PodMetrics(context.Context) map[string]k8s.Usage  { return nil }

// newTestServer builds a Server around a collector that has run zero or one
// collection cycles. Run(ctx) with an already-cancelled ctx still primes
// exactly one collect() call before observing ctx.Done(), so this triggers a
// single deterministic poll without starting the background ticker.
func newTestServer(t *testing.T, corsOrigins []string, collected bool, srcErr error) *Server {
	t.Helper()
	src := &fakeSource{err: srcErr}
	col := collect.New(src, time.Hour, 5*time.Second, 10, func() bool { return true }, testLogger())
	if collected {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		col.Run(ctx)
	}
	return NewServer(col, nil, corsOrigins, testLogger())
}

// newTestServerWithMonitor wires a real integration Monitor, already checked
// once, so the endpoint is exercised end-to-end rather than through its
// nil-monitor shortcut.
func newTestServerWithMonitor(t *testing.T, cfg integrations.Config) *Server {
	t.Helper()
	col := collect.New(&fakeSource{}, time.Hour, 5*time.Second, 10, func() bool { return true }, testLogger())
	mon := integrations.New(cfg, testLogger())
	mon.Check(context.Background())
	return NewServer(col, mon, nil, testLogger())
}

func do(s *Server, method, path, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

func TestLiveAlwaysOK(t *testing.T) {
	s := newTestServer(t, nil, false, nil)
	w := do(s, http.MethodGet, "/live", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestReadyBeforeAndAfterCollection(t *testing.T) {
	before := newTestServer(t, nil, false, nil)
	w := do(before, http.MethodGet, "/ready", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 before any collection, got %d", w.Code)
	}

	after := newTestServer(t, nil, true, nil)
	w = do(after, http.MethodGet, "/ready", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 after a successful collection, got %d", w.Code)
	}
}

func TestReadyFailsWhenLastPollErrored(t *testing.T) {
	s := newTestServer(t, nil, true, errors.New("api-server unreachable"))
	w := do(s, http.MethodGet, "/ready", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 when the only poll failed, got %d", w.Code)
	}
}

func TestHealthAlways200AndReportsError(t *testing.T) {
	s := newTestServer(t, nil, true, errors.New("api-server unreachable"))
	w := do(s, http.MethodGet, "/api/health", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 even when not ready, got %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ready"] != false {
		t.Errorf("want ready=false, got %v", body["ready"])
	}
	if errMsg, _ := body["last_error"].(string); errMsg == "" {
		t.Error("want last_error to be surfaced in the health payload")
	}
}

func TestIntegrationsAvailableBeforeClusterCollection(t *testing.T) {
	s := newTestServer(t, nil, false, nil)
	w := do(s, http.MethodGet, "/api/integrations", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 before the Kubernetes collector is ready, got %d", w.Code)
	}
	var body struct {
		Providers []any `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Providers == nil {
		t.Fatal("providers must be an empty array, not null")
	}
}

func TestDataEndpointsGatedUntilFirstCollection(t *testing.T) {
	before := newTestServer(t, nil, false, nil)
	w := do(before, http.MethodGet, "/api/cluster", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 before ready, got %d", w.Code)
	}

	after := newTestServer(t, nil, true, nil)
	w = do(after, http.MethodGet, "/api/cluster", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 once ready, got %d", w.Code)
	}
}

func TestNodeDetailNotFound(t *testing.T) {
	s := newTestServer(t, nil, true, nil)
	w := do(s, http.MethodGet, "/api/nodes/does-not-exist", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestCORSDisabledByDefault(t *testing.T) {
	s := newTestServer(t, nil, true, nil)
	w := do(s, http.MethodGet, "/api/cluster", "https://evil.example.com")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("want no CORS header when no allowlist is configured, got %q", got)
	}
}

func TestCORSAllowlistedOriginIsEchoed(t *testing.T) {
	s := newTestServer(t, []string{"https://dashboard.example.com"}, true, nil)
	w := do(s, http.MethodGet, "/api/cluster", "https://dashboard.example.com")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://dashboard.example.com" {
		t.Fatalf("want the allowlisted origin echoed back, got %q", got)
	}
	if got := w.Header().Get("Vary"); got != "Origin" {
		t.Errorf("want Vary: Origin on a per-origin CORS response, got %q", got)
	}
}

func TestCORSRejectsNonAllowlistedOrigin(t *testing.T) {
	s := newTestServer(t, []string{"https://dashboard.example.com"}, true, nil)
	w := do(s, http.MethodGet, "/api/cluster", "https://evil.example.com")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("want no CORS header for a non-allowlisted origin, got %q", got)
	}
}

func TestCORSWildcardAllowsAnyOrigin(t *testing.T) {
	s := newTestServer(t, []string{"*"}, true, nil)
	w := do(s, http.MethodGet, "/api/cluster", "https://anything.example.com")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://anything.example.com" {
		t.Fatalf("want the origin echoed back under a wildcard allowlist, got %q", got)
	}
}

func TestOptionsPreflightReturnsNoContent(t *testing.T) {
	s := newTestServer(t, []string{"https://dashboard.example.com"}, true, nil)
	w := do(s, http.MethodOptions, "/api/cluster", "https://dashboard.example.com")
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204 for an OPTIONS preflight, got %d", w.Code)
	}
}

func TestIntegrationsEndpointServesTheMonitorSnapshot(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	s := newTestServerWithMonitor(t, integrations.Config{
		CollectorHealthURL: backend.URL,
		Timeout:            5 * time.Second,
	})
	w := do(s, http.MethodGet, "/api/integrations", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	var snap integrations.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Providers) != 3 || snap.UpdatedAt == 0 {
		t.Fatalf("endpoint did not serve a completed snapshot: %+v", snap)
	}
	var collector integrations.ProviderStatus
	for _, p := range snap.Providers {
		if p.Name == "otel-collector" {
			collector = p
		}
	}
	if !collector.Reachable {
		t.Fatalf("the configured provider should be reachable: %+v", collector)
	}
}

// The response is the only thing that leaves the process, so assert on the
// serialized bytes: no endpoint, credential or upstream body may appear in it,
// whatever fields are added to ProviderStatus later.
func TestIntegrationsResponseNeverCarriesEndpointsOrCredentials(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"green","cluster_name":"nlziet-prod"}`)
	}))
	defer backend.Close()

	s := newTestServerWithMonitor(t, integrations.Config{
		PrometheusURL:         backend.URL,
		PrometheusBearerToken: "prom-token-should-never-appear",
		ElasticsearchURL:      backend.URL,
		ElasticsearchAPIKey:   "elastic-key-should-never-appear",
		Timeout:               5 * time.Second,
	})
	body := do(s, http.MethodGet, "/api/integrations", "").Body.String()

	for _, secret := range []string{
		"prom-token-should-never-appear",
		"elastic-key-should-never-appear",
		backend.URL,
		"nlziet-prod",
	} {
		if strings.Contains(body, secret) {
			t.Fatalf("response leaked %q: %s", secret, body)
		}
	}
	if !strings.Contains(body, "cluster status green") {
		t.Fatalf("expected the sanitized health detail to survive: %s", body)
	}
}
