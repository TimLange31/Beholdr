package servicehealth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delangetimm/beholdr/internal/collect"
	"github.com/delangetimm/beholdr/internal/integrations"
)

// valueFor is the shared scoring fixture: it maps a query template onto the
// value both the range and the instant evaluation should report.
func valueFor(query string) float64 {
	switch {
	case strings.Contains(query, "offset 1w"):
		return 1
	case strings.Contains(query, `code=~"5.."`):
		return 3
	case strings.Contains(query, "container_cpu_usage_seconds_total"):
		return 85
	case strings.Contains(query, "container_memory_working_set_bytes"):
		return 96
	case strings.Contains(query, "waiting_reason"):
		return 2
	case strings.Contains(query, "pod_status_phase"):
		return 1
	}
	return 0
}

type fakeQuerier struct {
	mu       sync.Mutex
	queries  []string
	instants []string
	err      error
	// rangeTail, when set, is appended to the range series so the last range
	// point can be made to disagree with the instant value.
	rangeTail *float64
}

func (f *fakeQuerier) QueryPrometheusRange(_ context.Context, query string, start, _ time.Time, _ time.Duration) ([]integrations.TimeSeries, error) {
	f.mu.Lock()
	f.queries = append(f.queries, query)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	value := valueFor(query)
	values := []integrations.Sample{
		{Timestamp: unixSeconds(start), Value: value / 2},
		{Timestamp: unixSeconds(start.Add(time.Minute)), Value: value},
	}
	if f.rangeTail != nil {
		values = append(values, integrations.Sample{Timestamp: unixSeconds(start.Add(2 * time.Minute)), Value: *f.rangeTail})
	}
	return []integrations.TimeSeries{{Values: values}}, nil
}

func (f *fakeQuerier) QueryPrometheusInstant(_ context.Context, query string, _ time.Time) ([]integrations.InstantSample, error) {
	f.mu.Lock()
	f.instants = append(f.instants, query)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return []integrations.InstantSample{{Sample: integrations.Sample{Value: valueFor(query)}}}, nil
}

func newService(t *testing.T, querier Querier, cfg Config) *Service {
	t.Helper()
	// Caching is off by default in tests so each case controls its own calls.
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = -1
	}
	service, err := New(querier, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestParseWindowIsBounded(t *testing.T) {
	for _, value := range []string{"1h", "6h", "24h", "7d", "21d"} {
		if window, ok := ParseWindow(value); !ok || window.Duration <= 0 || window.Step <= 0 {
			t.Errorf("expected supported window %q", value)
		}
	}
	if _, ok := ParseWindow("90d"); ok {
		t.Fatal("unbounded range must be rejected")
	}
	if window, ok := ParseWindow(""); !ok || window.Name != "24h" {
		t.Fatal("empty range should select the 24h default")
	}
}

func TestEveryWindowStaysUnderThePointCap(t *testing.T) {
	for name, window := range windows {
		if points := window.Duration / window.Step; points > 2_000 {
			t.Errorf("window %q needs %d points, above the query cap", name, points)
		}
	}
}

// A week-before overlay is only a comparison while the offset series does not
// overlap the current one. Past seven days it is the same wall-clock data drawn
// twice.
func TestComparisonIsSuppressedBeyondOneWeek(t *testing.T) {
	for _, tc := range []struct {
		window string
		want   bool
	}{{"1h", true}, {"6h", true}, {"24h", true}, {"7d", true}, {"21d", false}} {
		window, _ := ParseWindow(tc.window)
		if window.Comparable() != tc.want {
			t.Errorf("window %s: comparable = %v, want %v", tc.window, window.Comparable(), tc.want)
		}
	}
}

func TestComparisonQueriesAreOnlyIssuedWhenMeaningful(t *testing.T) {
	for _, tc := range []struct {
		window     string
		wantSeries int
	}{{"7d", 6}, {"21d", 5}} {
		querier := &fakeQuerier{}
		service := newService(t, querier, Config{})
		window, _ := ParseWindow(tc.window)
		report, err := service.Query(context.Background(), deployment("production", "video-api", 10), nil, window, time.Unix(2_000_000, 0))
		if err != nil {
			t.Fatal(err)
		}
		querier.mu.Lock()
		got := len(querier.queries)
		querier.mu.Unlock()
		if got != tc.wantSeries {
			t.Errorf("window %s: %d range queries, want %d", tc.window, got, tc.wantSeries)
		}
		if report.Compared != (tc.window == "7d") {
			t.Errorf("window %s: compared = %v", tc.window, report.Compared)
		}
		lines := report.Signals[0].Lines
		if tc.window == "21d" && len(lines) != 1 {
			t.Errorf("21d must not promise a week-before line: %+v", lines)
		}
	}
}

func TestQueryBuildsPerServiceSignalsAndSeverity(t *testing.T) {
	querier := &fakeQuerier{}
	service := newService(t, querier, Config{})
	window, _ := ParseWindow("7d")
	report, err := service.Query(context.Background(), deployment("production", "video-api", 10), nil, window, time.Unix(2_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if report.Window != "7d" || report.Step != (30*time.Minute).Seconds() {
		t.Fatalf("unexpected range metadata: %+v", report)
	}
	if len(report.Signals) != 4 {
		t.Fatalf("want 4 health signals, got %d", len(report.Signals))
	}
	if report.Severity != SeverityCritical {
		t.Fatalf("want critical overall severity, got %s", report.Severity)
	}

	errorRate := report.Signals[0]
	if errorRate.Current == nil || *errorRate.Current != 3 || errorRate.Previous == nil || *errorRate.Previous != 1 {
		t.Fatalf("unexpected error comparison: %+v", errorRate)
	}
	if errorRate.Difference == nil || *errorRate.Difference != 2 || errorRate.Severity != SeverityCritical {
		t.Fatalf("error-rate increase should be critical: %+v", errorRate)
	}
	if report.Signals[2].Severity != SeverityCritical {
		t.Fatalf("96%% memory should be critical: %+v", report.Signals[2])
	}
	if report.Signals[3].Current == nil || *report.Signals[3].Current != 3 || report.Signals[3].Severity != SeverityCritical {
		t.Fatalf("three failing pods out of ten should be critical: %+v", report.Signals[3])
	}
}

// The score must come from the instant evaluation. Taking it from the last
// range point makes severity depend on which chart window is selected: at a
// one-hour step the newest point can be an hour old, so a spike that started
// ten minutes ago would be invisible on exactly the view an operator leaves
// open.
func TestSeverityComesFromTheInstantQueryNotTheChart(t *testing.T) {
	quiet := 0.0
	querier := &fakeQuerier{rangeTail: &quiet}
	service := newService(t, querier, Config{})
	for _, name := range []string{"1h", "21d"} {
		window, _ := ParseWindow(name)
		report, err := service.Query(context.Background(), deployment("production", "video-api", 10), nil, window, time.Unix(2_000_000, 0))
		if err != nil {
			t.Fatal(err)
		}
		if report.Signals[1].Current == nil || *report.Signals[1].Current != 85 {
			t.Fatalf("window %s: CPU should score from the instant value, got %+v", name, report.Signals[1].Current)
		}
		if report.Signals[1].Severity != SeverityWarning {
			t.Fatalf("window %s: 85%% CPU should be warning, got %s", name, report.Signals[1].Severity)
		}
		// The chart still ends on the quiet tail; only the score ignores it.
		points := report.Signals[1].Points
		if len(points) == 0 || points[len(points)-1]["current"] != 0 {
			t.Fatalf("window %s: chart should keep every range point: %+v", name, points)
		}
	}
}

func TestGeneratedQueriesAreStable(t *testing.T) {
	service := newService(t, &fakeQuerier{}, Config{})
	window, _ := ParseWindow("24h")
	got := service.queries(deployment("production", "video", 3), window)

	want := map[string]string{
		"errors":          `100 * sum(rate(aspnetcore_requests_duration_seconds_count{kubernetes_namespace="production",app_kubernetes_io_name="video",code=~"5.."}[5m])) / clamp_min(sum(rate(aspnetcore_requests_duration_seconds_count{kubernetes_namespace="production",app_kubernetes_io_name="video"}[5m])), 0.000001)`,
		"errors_previous": `100 * sum(rate(aspnetcore_requests_duration_seconds_count{kubernetes_namespace="production",app_kubernetes_io_name="video",code=~"5.."}[5m] offset 1w)) / clamp_min(sum(rate(aspnetcore_requests_duration_seconds_count{kubernetes_namespace="production",app_kubernetes_io_name="video"}[5m] offset 1w)), 0.000001)`,
		"cpu":             `100 * sum(rate(container_cpu_usage_seconds_total{namespace="production",pod=~"^video-[a-z0-9]+-[a-z0-9]{5}$",container!="",container!="POD"}[5m])) / clamp_min(sum(kube_pod_container_resource_limits{namespace="production",pod=~"^video-[a-z0-9]+-[a-z0-9]{5}$",container!="",resource="cpu",unit="core"} > 0), 0.001)`,
		"memory":          `100 * sum(container_memory_working_set_bytes{namespace="production",pod=~"^video-[a-z0-9]+-[a-z0-9]{5}$",container!="",container!="POD"}) / clamp_min(sum(kube_pod_container_resource_limits{namespace="production",pod=~"^video-[a-z0-9]+-[a-z0-9]{5}$",container!="",resource="memory",unit="byte"} > 0), 1)`,
		"waiting":         `(sum(max by (namespace,pod) (kube_pod_container_status_waiting_reason{namespace="production",pod=~"^video-[a-z0-9]+-[a-z0-9]{5}$",reason=~"CrashLoopBackOff|ImagePullBackOff|ErrImagePull|CreateContainerConfigError"} == 1)) or 0 * count(kube_pod_info{namespace="production",pod=~"^video-[a-z0-9]+-[a-z0-9]{5}$"}))`,
		"failed_phase":    `(sum(kube_pod_status_phase{namespace="production",pod=~"^video-[a-z0-9]+-[a-z0-9]{5}$",phase=~"Failed|Unknown"} == 1 unless on (namespace,pod) kube_pod_owner{namespace="production",pod=~"^video-[a-z0-9]+-[a-z0-9]{5}$",owner_kind="Job"}) or 0 * count(kube_pod_info{namespace="production",pod=~"^video-[a-z0-9]+-[a-z0-9]{5}$"}))`,
	}
	if len(got) != len(want) {
		t.Fatalf("want %d queries, got %d: %v", len(want), len(got), keys(got))
	}
	for key, expected := range want {
		if got[key] != expected {
			t.Errorf("query %q:\n got: %s\nwant: %s", key, got[key], expected)
		}
	}
}

func TestCPUBasisSelectsTheDenominator(t *testing.T) {
	service := newService(t, &fakeQuerier{}, Config{CPUBasis: CPUBasisRequests})
	window, _ := ParseWindow("1h")
	query := service.queries(deployment("ns", "app", 1), window)["cpu"]
	if !strings.Contains(query, "kube_pod_container_resource_requests") {
		t.Fatalf("requests basis not applied: %s", query)
	}
	if service.cpuUnit() != "% of requests" {
		t.Fatalf("unit should follow the basis, got %q", service.cpuUnit())
	}
}

// The collector only ever labels workloads "Deployment" or "Other", so the
// default branch has to cover every other shape without becoming a wildcard
// that absorbs a sibling service's pods.
func TestPodRegexNeverMatchesASiblingWorkload(t *testing.T) {
	for _, kind := range []string{"Deployment", "StatefulSet", "DaemonSet", "Other", "Job"} {
		re := regexp.MustCompile(workloadPodRegex(kind, "api"))
		// "api-gateway-abcde" is deliberately absent: it is a valid pod name
		// for both a DaemonSet called "api-gateway" and a Deployment called
		// "api", so no name-shape regex can separate them. Scoring uses the
		// exact current pod names instead — see the test below.
		for _, pod := range []string{
			"api-gateway-7d9f8c6b54-x2k9p",
			"api-worker-0",
			"apiserver-7d9f8c6b54-x2k9p",
			"api-gateway-7d9f8c6b54-abcde",
		} {
			if re.MatchString(pod) {
				t.Errorf("kind %s: regex %q must not match sibling pod %q", kind, re, pod)
			}
		}
	}
}

func TestPodRegexMatchesItsOwnPods(t *testing.T) {
	cases := []struct{ kind, pod string }{
		{"Deployment", "api-7d9f8c6b54-x2k9p"},
		{"Deployment", "api-6b9-x2k9p"},
		{"StatefulSet", "api-0"},
		{"StatefulSet", "api-12"},
		{"DaemonSet", "api-x2k9p"},
		{"Other", "api-7d9f8c6b54-x2k9p"},
		{"Other", "api-0"},
		{"Other", "api-x2k9p"},
	}
	for _, tc := range cases {
		if !regexp.MustCompile(workloadPodRegex(tc.kind, "api")).MatchString(tc.pod) {
			t.Errorf("kind %s should match its own pod %q", tc.kind, tc.pod)
		}
	}
}

// Severity must not be computed from pods the workload does not own. When the
// collector knows the live pod names they are matched exactly, which closes the
// one ambiguity name-shape matching cannot.
func TestScoringUsesExactPodNamesWhenKnown(t *testing.T) {
	querier := &fakeQuerier{}
	service := newService(t, querier, Config{})
	window, _ := ParseWindow("1h")
	pods := []string{"api-7d9f8c6b54-x2k9p", "api-7d9f8c6b54-b1n4q"}
	if _, err := service.Query(context.Background(), deployment("ns", "api", 2), pods, window, time.Now()); err != nil {
		t.Fatal(err)
	}
	querier.mu.Lock()
	defer querier.mu.Unlock()
	for _, query := range querier.instants {
		if strings.Contains(query, "kube_pod_info") && !strings.Contains(query, `pod=~"^(api-7d9f8c6b54-b1n4q|api-7d9f8c6b54-x2k9p)$"`) {
			t.Errorf("scoring query should pin the live pods: %s", query)
		}
	}
	for _, query := range querier.queries {
		if strings.Contains(query, "kube_pod_info") && !strings.Contains(query, `pod=~"^api-[a-z0-9]+-[a-z0-9]{5}$"`) {
			t.Errorf("chart query should keep the name-shape regex for history: %s", query)
		}
	}
}

func TestScoringFallsBackToTheShapeWhenNoPodsAreKnown(t *testing.T) {
	querier := &fakeQuerier{}
	service := newService(t, querier, Config{})
	window, _ := ParseWindow("1h")
	if _, err := service.Query(context.Background(), deployment("ns", "api", 0), nil, window, time.Now()); err != nil {
		t.Fatal(err)
	}
	querier.mu.Lock()
	defer querier.mu.Unlock()
	for _, query := range querier.instants {
		if strings.Contains(query, "kube_pod_info") && !strings.Contains(query, `pod=~"^api-[a-z0-9]+-[a-z0-9]{5}$"`) {
			t.Errorf("scoring should fall back to the shape regex: %s", query)
		}
	}
}

func TestWorkloadPodRegexEscapesNames(t *testing.T) {
	got := workloadPodRegex("Deployment", "video.api")
	if got != `^video\.api-[a-z0-9]+-[a-z0-9]{5}$` {
		t.Fatalf("unexpected escaped pod regex %q", got)
	}
}

// Names reaching the templates come from the collector's snapshot of real
// Kubernetes objects, but the quoting must hold regardless: this is the last
// line of defence against a crafted name reshaping a query.
func TestHostileNamesCannotEscapeTheSelector(t *testing.T) {
	service := newService(t, &fakeQuerier{}, Config{})
	window, _ := ParseWindow("1h")
	hostile := collect.Microservice{
		Namespace: `prod"} or up{job="admin`,
		Name:      `x"}[5m])) or vector(1) #`,
		Kind:      "Deployment",
	}
	for key, query := range service.queries(hostile, window) {
		// Every namespace occurrence must be the quoted literal — that is what
		// proves the value was escaped rather than concatenated in raw.
		if strings.Contains(query, hostile.Namespace) {
			t.Errorf("query %q carries the namespace unescaped: %s", key, query)
		}
		if !strings.Contains(query, strconv.Quote(hostile.Namespace)) {
			t.Errorf("query %q should embed the namespace as a quoted literal: %s", key, query)
		}
		// The name reaches a regex operand, so it must be regex-escaped too.
		if strings.Contains(query, "pod=~") && !strings.Contains(query, strconv.Quote("^"+regexp.QuoteMeta(hostile.Name)+"-[a-z0-9]+-[a-z0-9]{5}$")) {
			t.Errorf("query %q should regex-escape the workload name: %s", key, query)
		}
	}
}

type erroringQuerier struct{ err error }

func (e erroringQuerier) QueryPrometheusRange(context.Context, string, time.Time, time.Time, time.Duration) ([]integrations.TimeSeries, error) {
	return nil, e.err
}

func (e erroringQuerier) QueryPrometheusInstant(context.Context, string, time.Time) ([]integrations.InstantSample, error) {
	return nil, e.err
}

func TestQueryFailuresAreUnknownWithAnActionableMessage(t *testing.T) {
	service := newService(t, erroringQuerier{err: integrations.ErrPrometheusQueryRejected}, Config{})
	window, _ := ParseWindow("1h")
	report, err := service.Query(context.Background(), deployment("ns", "app", 1), nil, window, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Severity != SeverityUnknown {
		t.Fatalf("a failed query must not read as healthy, got %s", report.Severity)
	}
	for _, signal := range report.Signals {
		if signal.State != StateError {
			t.Errorf("signal %s should be errored: %+v", signal.Key, signal)
		}
		if !strings.Contains(signal.Error, "metric and label names") {
			t.Errorf("signal %s should say what to fix, got %q", signal.Key, signal.Error)
		}
	}
}

func TestNotConfiguredPropagates(t *testing.T) {
	service := newService(t, erroringQuerier{err: integrations.ErrPrometheusNotConfigured}, Config{})
	window, _ := ParseWindow("1h")
	if _, err := service.Query(context.Background(), deployment("ns", "app", 1), nil, window, time.Now()); !errors.Is(err, integrations.ErrPrometheusNotConfigured) {
		t.Fatalf("want ErrPrometheusNotConfigured, got %v", err)
	}
}

type emptyQuerier struct{}

func (emptyQuerier) QueryPrometheusRange(context.Context, string, time.Time, time.Time, time.Duration) ([]integrations.TimeSeries, error) {
	return nil, nil
}

func (emptyQuerier) QueryPrometheusInstant(context.Context, string, time.Time) ([]integrations.InstantSample, error) {
	return nil, nil
}

func TestNoMetricsAtAllIsUnknownNotHealthy(t *testing.T) {
	service := newService(t, emptyQuerier{}, Config{})
	window, _ := ParseWindow("1h")
	report, err := service.Query(context.Background(), deployment("ns", "app", 1), nil, window, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Severity != SeverityUnknown {
		t.Fatalf("no telemetry at all must not be reported healthy, got %s", report.Severity)
	}
	for _, signal := range report.Signals {
		if signal.State != StateNoData || signal.Error == "" {
			t.Errorf("missing signal should be explicit: %+v", signal)
		}
	}
}

type cpuOnlyQuerier struct{}

func (cpuOnlyQuerier) QueryPrometheusRange(_ context.Context, query string, start, _ time.Time, _ time.Duration) ([]integrations.TimeSeries, error) {
	if !strings.Contains(query, "container_cpu_usage_seconds_total") {
		return nil, nil
	}
	return []integrations.TimeSeries{{Values: []integrations.Sample{{Timestamp: unixSeconds(start), Value: 25}}}}, nil
}

func (cpuOnlyQuerier) QueryPrometheusInstant(_ context.Context, query string, _ time.Time) ([]integrations.InstantSample, error) {
	if !strings.Contains(query, "container_cpu_usage_seconds_total") {
		return nil, nil
	}
	return []integrations.InstantSample{{Sample: integrations.Sample{Value: 25}}}, nil
}

// A metric that does not exist for this workload — no memory limit set, no HTTP
// traffic — says nothing about the service's health, so it is skipped rather
// than dragging the aggregate to unknown forever. A metric whose *query failed*
// is a different thing and still counts (see the test above).
func TestAbsentMetricsDoNotPinTheAggregateToUnknown(t *testing.T) {
	service := newService(t, cpuOnlyQuerier{}, Config{})
	window, _ := ParseWindow("1h")
	report, err := service.Query(context.Background(), deployment("ns", "app", 1), nil, window, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Signals[1].State != StateOK || report.Signals[1].Severity != SeverityHealthy {
		t.Fatalf("CPU should be measured and healthy: %+v", report.Signals[1])
	}
	if report.Severity != SeverityHealthy {
		t.Fatalf("aggregate should follow the measured signal, got %s", report.Severity)
	}
	for _, key := range []int{0, 2, 3} {
		if report.Signals[key].State != StateNoData {
			t.Errorf("signal %s should report no data: %+v", report.Signals[key].Key, report.Signals[key])
		}
	}
}

type multiSeriesQuerier struct{ emptyQuerier }

func (multiSeriesQuerier) QueryPrometheusInstant(context.Context, string, time.Time) ([]integrations.InstantSample, error) {
	return []integrations.InstantSample{
		{Metric: map[string]string{"pod": "a"}, Sample: integrations.Sample{Value: 1}},
		{Metric: map[string]string{"pod": "b"}, Sample: integrations.Sample{Value: 99}},
	}, nil
}

// Every template aggregates to a single series. If one ever stops doing so,
// silently scoring the first element would report a confident wrong number.
func TestUnexpectedExtraSeriesIsAnErrorNotTheFirstValue(t *testing.T) {
	service := newService(t, multiSeriesQuerier{}, Config{})
	window, _ := ParseWindow("1h")
	report, err := service.Query(context.Background(), deployment("ns", "app", 1), nil, window, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Severity != SeverityUnknown {
		t.Fatalf("ambiguous results must not be scored, got %s", report.Severity)
	}
	for _, signal := range report.Signals {
		if signal.State != StateError {
			t.Errorf("signal %s should be errored: %+v", signal.Key, signal)
		}
	}
}

func TestSingleReplicaFailureIsCriticalWithNoWarningBand(t *testing.T) {
	service := newService(t, &fakeQuerier{}, Config{})
	window, _ := ParseWindow("1h")
	report, err := service.Query(context.Background(), deployment("production", "singleton", 1), nil, window, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	signal := report.Signals[3]
	if signal.Critical != 1 || signal.Severity != SeverityCritical {
		t.Fatalf("single-replica failure should be critical: %+v", signal)
	}
	if signal.Warning != nil {
		t.Fatalf("a single-replica service has no warning band, got %v", *signal.Warning)
	}
}

// --- configuration validation ------------------------------------------------

func TestInvalidConfigurationIsRejectedNotSilentlyReplaced(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"metric name", Config{HTTPRequestsMetric: "not a metric!"}},
		{"label name", Config{KubePodLabel: "pod name"}},
		{"cpu basis", Config{CPUBasis: "guesses"}},
		{"inverted error thresholds", Config{Thresholds: Thresholds{ErrorRateWarning: 10, ErrorRateCritical: 8}}},
		{"equal cpu thresholds", Config{Thresholds: Thresholds{CPUWarning: 90, CPUCritical: 90}}},
		{"negative threshold", Config{Thresholds: Thresholds{MemoryWarning: -1}}},
	}
	for _, tc := range cases {
		if _, err := New(&fakeQuerier{}, tc.cfg); err == nil {
			t.Errorf("%s: want a configuration error, got none", tc.name)
		}
	}
}

func TestValidConfigurationIsAccepted(t *testing.T) {
	service, err := New(&fakeQuerier{}, Config{
		HTTPRequestsMetric: "http_server_request_duration_seconds_count",
		HTTPStatusLabel:    "http_response_status_code",
		AppServiceLabel:    "service_name",
		CPUBasis:           CPUBasisRequests,
		Thresholds:         Thresholds{CPUWarning: 150, CPUCritical: 300},
	})
	if err != nil {
		t.Fatal(err)
	}
	window, _ := ParseWindow("1h")
	query := service.queries(deployment("ns", "app", 1), window)["errors"]
	if !strings.Contains(query, "http_server_request_duration_seconds_count") ||
		!strings.Contains(query, `http_response_status_code=~"5.."`) ||
		!strings.Contains(query, `service_name="app"`) {
		t.Fatalf("configuration was not applied: %s", query)
	}
}

// --- caching and singleflight ------------------------------------------------

type countingQuerier struct {
	emptyQuerier
	rangeCalls atomic.Int64
	release    chan struct{}
}

func (c *countingQuerier) QueryPrometheusRange(context.Context, string, time.Time, time.Time, time.Duration) ([]integrations.TimeSeries, error) {
	c.rangeCalls.Add(1)
	if c.release != nil {
		<-c.release
	}
	return nil, nil
}

func TestCompletedReportsAreReusedWithinTheTTL(t *testing.T) {
	querier := &countingQuerier{}
	service := newService(t, querier, Config{CacheTTL: time.Minute})
	window, _ := ParseWindow("1h")
	for i := 0; i < 5; i++ {
		if _, err := service.Query(context.Background(), deployment("ns", "app", 1), nil, window, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if got := querier.rangeCalls.Load(); got != 6 {
		t.Fatalf("want one evaluation (6 range queries) reused four times, got %d range queries", got)
	}
}

func TestConcurrentRequestsShareOneEvaluation(t *testing.T) {
	querier := &countingQuerier{release: make(chan struct{})}
	service := newService(t, querier, Config{CacheTTL: time.Minute})
	window, _ := ParseWindow("1h")

	const callers = 8
	var wg sync.WaitGroup
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = service.Query(context.Background(), deployment("ns", "app", 1), nil, window, time.Now())
		}(i)
	}
	// Let the callers pile up on the same key, then let the evaluation finish.
	time.Sleep(50 * time.Millisecond)
	close(querier.release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	if got := querier.rangeCalls.Load(); got != 6 {
		t.Fatalf("want one shared evaluation (6 range queries), got %d", got)
	}
}

func TestDifferentWindowsAreCachedSeparately(t *testing.T) {
	querier := &countingQuerier{}
	service := newService(t, querier, Config{CacheTTL: time.Minute})
	for _, name := range []string{"1h", "6h", "1h"} {
		window, _ := ParseWindow(name)
		if _, err := service.Query(context.Background(), deployment("ns", "app", 1), nil, window, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if got := querier.rangeCalls.Load(); got != 12 {
		t.Fatalf("want two evaluations (12 range queries), got %d", got)
	}
}

func TestConcurrentQueriesAreBounded(t *testing.T) {
	var inFlight, peak atomic.Int64
	querier := &gaugeQuerier{inFlight: &inFlight, peak: &peak}
	service := newService(t, querier, Config{MaxConcurrentQueries: 2})
	window, _ := ParseWindow("1h")
	if _, err := service.Query(context.Background(), deployment("ns", "app", 1), nil, window, time.Now()); err != nil {
		t.Fatal(err)
	}
	if peak.Load() > 2 {
		t.Fatalf("concurrency cap of 2 exceeded: peak %d", peak.Load())
	}
}

type gaugeQuerier struct {
	inFlight, peak *atomic.Int64
}

func (g *gaugeQuerier) enter() {
	current := g.inFlight.Add(1)
	for {
		peak := g.peak.Load()
		if current <= peak || g.peak.CompareAndSwap(peak, current) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond)
}

func (g *gaugeQuerier) QueryPrometheusRange(context.Context, string, time.Time, time.Time, time.Duration) ([]integrations.TimeSeries, error) {
	g.enter()
	defer g.inFlight.Add(-1)
	return nil, nil
}

func (g *gaugeQuerier) QueryPrometheusInstant(context.Context, string, time.Time) ([]integrations.InstantSample, error) {
	g.enter()
	defer g.inFlight.Add(-1)
	return nil, nil
}

// --- helpers -----------------------------------------------------------------

func deployment(namespace, name string, replicas int32) collect.Microservice {
	return collect.Microservice{
		Key: fmt.Sprintf("%s/%s", namespace, name), Namespace: namespace, Name: name,
		Kind: "Deployment", DesiredReplica: replicas,
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
