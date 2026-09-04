package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/delangetimm/beholdr/internal/collect"
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
	return NewServer(col, corsOrigins, testLogger())
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
