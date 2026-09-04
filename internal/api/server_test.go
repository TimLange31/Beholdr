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
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/delangetimm/beholdr/internal/collect"
	"github.com/delangetimm/beholdr/internal/integrations"
	"github.com/delangetimm/beholdr/internal/k8s"
	"github.com/delangetimm/beholdr/internal/servicehealth"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeSource is an empty-but-successful (or erroring) Source: enough to
// drive the collector through a poll without a real cluster.
type fakeSource struct {
	err         error
	deployments []appsv1.Deployment
	pods        []corev1.Pod
}

func (f *fakeSource) Nodes(context.Context) ([]corev1.Node, error) { return nil, f.err }
func (f *fakeSource) Pods(context.Context) ([]corev1.Pod, error)   { return f.pods, f.err }
func (f *fakeSource) Deployments(context.Context) ([]appsv1.Deployment, error) {
	return f.deployments, f.err
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
	return NewServer(col, nil, nil, corsOrigins, testLogger())
}

// newTestServerWithMonitor wires a real integration Monitor, already checked
// once, so the endpoint is exercised end-to-end rather than through its
// nil-monitor shortcut.
func newTestServerWithMonitor(t *testing.T, cfg integrations.Config) *Server {
	t.Helper()
	col := collect.New(&fakeSource{}, time.Hour, 5*time.Second, 10, func() bool { return true }, testLogger())
	mon := integrations.New(cfg, testLogger())
	mon.Check(context.Background())
	return NewServer(col, mon, nil, nil, testLogger())
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
		_, _ = io.WriteString(w, `{"status":"green","cluster_name":"production-cluster"}`)
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
		"production-cluster",
	} {
		if strings.Contains(body, secret) {
			t.Fatalf("response leaked %q: %s", secret, body)
		}
	}
	if !strings.Contains(body, "cluster status green") {
		t.Fatalf("expected the sanitized health detail to survive: %s", body)
	}
}

type apiMetricsQuerier struct {
	mu       sync.Mutex
	instants []string
	err      error
}

func (q *apiMetricsQuerier) QueryPrometheusRange(_ context.Context, _ string, start, _ time.Time, _ time.Duration) ([]integrations.TimeSeries, error) {
	if q.err != nil {
		return nil, q.err
	}
	return []integrations.TimeSeries{{Values: []integrations.Sample{{Timestamp: float64(start.Unix()), Value: 0.5}}}}, nil
}

func (q *apiMetricsQuerier) QueryPrometheusInstant(_ context.Context, query string, _ time.Time) ([]integrations.InstantSample, error) {
	q.mu.Lock()
	q.instants = append(q.instants, query)
	q.mu.Unlock()
	if q.err != nil {
		return nil, q.err
	}
	return []integrations.InstantSample{{Sample: integrations.Sample{Value: 0.5}}}, nil
}

func newServiceMetricsServer(t *testing.T, querier *apiMetricsQuerier) *Server {
	t.Helper()
	replicas := int32(2)
	src := &fakeSource{
		deployments: []appsv1.Deployment{{
			ObjectMeta: metav1.ObjectMeta{Name: "video", Namespace: "production"},
			Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		}},
		pods: []corev1.Pod{
			pod("production", "video-7d9f8c6b54-x2k9p", "video"),
			pod("production", "video-7d9f8c6b54-b1n4q", "video"),
			pod("production", "video-gateway-7d9f8c6b54-zzzzz", "video-gateway"),
		},
	}
	col := collect.New(src, time.Hour, 5*time.Second, 10, func() bool { return true }, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	col.Run(ctx)
	health, err := servicehealth.New(querier, servicehealth.Config{CacheTTL: -1})
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(col, nil, health, nil, testLogger())
}

func pod(namespace, name, workload string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: namespace,
			Labels: map[string]string{"app": workload},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func TestMicroserviceMetricsEndpoint(t *testing.T) {
	s := newServiceMetricsServer(t, &apiMetricsQuerier{})
	w := do(s, http.MethodGet, "/api/microservices/production/video/metrics?range=21d", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("severity must not be cached by the browser, got %q", got)
	}
	var report servicehealth.Report
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Namespace != "production" || report.Service != "video" || report.Window != "21d" || len(report.Signals) != 4 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Compared {
		t.Error("the 21d window must not claim a week-before comparison")
	}
}

// The scoring queries must be pinned to the pods this workload owns, never to
// a name-shaped guess that can pick up a sibling service.
func TestMicroserviceMetricsScoresOnlyItsOwnPods(t *testing.T) {
	querier := &apiMetricsQuerier{}
	s := newServiceMetricsServer(t, querier)
	if w := do(s, http.MethodGet, "/api/microservices/production/video/metrics?range=1h", ""); w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	querier.mu.Lock()
	defer querier.mu.Unlock()
	if len(querier.instants) == 0 {
		t.Fatal("no instant queries were issued")
	}
	for _, query := range querier.instants {
		if !strings.Contains(query, "kube_pod_info") {
			continue
		}
		if !strings.Contains(query, "video-7d9f8c6b54-x2k9p") || !strings.Contains(query, "video-7d9f8c6b54-b1n4q") {
			t.Errorf("scoring query is not pinned to the workload's pods: %s", query)
		}
		if strings.Contains(query, "video-gateway") {
			t.Errorf("scoring query picked up a sibling workload's pod: %s", query)
		}
	}
}

func TestMicroserviceMetricsRejectsUnsupportedRange(t *testing.T) {
	s := newServiceMetricsServer(t, &apiMetricsQuerier{})
	w := do(s, http.MethodGet, "/api/microservices/production/video/metrics?range=90d", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestMicroserviceMetricsUnknownWorkloadIs404(t *testing.T) {
	s := newServiceMetricsServer(t, &apiMetricsQuerier{})
	w := do(s, http.MethodGet, "/api/microservices/production/nope/metrics?range=1h", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestMicroserviceMetricsWithoutPrometheusIs503(t *testing.T) {
	querier := &apiMetricsQuerier{err: integrations.ErrPrometheusNotConfigured}
	s := newServiceMetricsServer(t, querier)
	w := do(s, http.MethodGet, "/api/microservices/production/video/metrics?range=1h", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d: %s", w.Code, w.Body.String())
	}
}

// A backend failure is reported through the signal states, not as a dead
// endpoint: the operator still gets the charts and an actionable message.
func TestMicroserviceMetricsSurfacesQueryFailuresPerSignal(t *testing.T) {
	querier := &apiMetricsQuerier{err: integrations.ErrPrometheusQueryRejected}
	s := newServiceMetricsServer(t, querier)
	w := do(s, http.MethodGet, "/api/microservices/production/video/metrics?range=1h", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var report servicehealth.Report
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Severity != servicehealth.SeverityUnknown {
		t.Fatalf("failed queries must not read as healthy: %s", report.Severity)
	}
	body := w.Body.String()
	for _, secret := range []string{"kube_pod_info", "aspnetcore_", "namespace="} {
		if strings.Contains(body, secret) {
			t.Errorf("response leaked query internals (%q): %s", secret, body)
		}
	}
}
