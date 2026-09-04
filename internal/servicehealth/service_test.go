package servicehealth

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delangetimm/beholdr/internal/collect"
	"github.com/delangetimm/beholdr/internal/integrations"
)

type fakeQuerier struct {
	mu      sync.Mutex
	queries []string
	err     error
}

func (f *fakeQuerier) QueryPrometheusRange(_ context.Context, query string, start, _ time.Time, _ time.Duration) ([]integrations.TimeSeries, error) {
	f.mu.Lock()
	f.queries = append(f.queries, query)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	value := 0.0
	switch {
	case strings.Contains(query, "offset 1w"):
		value = 1
	case strings.Contains(query, `code=~"5.."`):
		value = 3
	case strings.Contains(query, "container_cpu_usage_seconds_total"):
		value = 85
	case strings.Contains(query, "container_memory_working_set_bytes"):
		value = 96
	case strings.Contains(query, "waiting_reason"):
		value = 2
	case strings.Contains(query, "pod_status_phase"):
		value = 1
	}
	return []integrations.TimeSeries{{Values: []integrations.Sample{
		{Timestamp: unixSeconds(start), Value: value / 2},
		{Timestamp: unixSeconds(start.Add(time.Minute)), Value: value},
	}}}, nil
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

func TestQueryBuildsPerServiceSignalsAndSeverity(t *testing.T) {
	querier := &fakeQuerier{}
	service := New(querier, Config{})
	window, _ := ParseWindow("7d")
	end := time.Unix(2_000_000, 0)
	report, err := service.Query(context.Background(), collect.Microservice{
		Namespace:      "production",
		Name:           "video-api",
		Kind:           "Deployment",
		DesiredReplica: 10,
	}, window, end)
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

	querier.mu.Lock()
	defer querier.mu.Unlock()
	if len(querier.queries) != 6 {
		t.Fatalf("want six bounded Prometheus queries, got %d", len(querier.queries))
	}
	for _, query := range querier.queries {
		if strings.Contains(query, "aspnetcore_requests_") {
			if !strings.Contains(query, `kubernetes_namespace="production"`) || !strings.Contains(query, `app_kubernetes_io_name="video-api"`) {
				t.Errorf("application query is not scoped to the service: %s", query)
			}
		} else if !strings.Contains(query, `namespace="production"`) || !strings.Contains(query, `pod=~"^video-api-[a-z0-9]{8,10}-[a-z0-9]{5}$"`) {
			t.Errorf("Kubernetes query is not scoped to the service: %s", query)
		}
	}
}

func TestQueryReportsMissingMetricsAsUnknown(t *testing.T) {
	service := New(emptyQuerier{}, Config{})
	window, _ := ParseWindow("1h")
	report, err := service.Query(context.Background(), collect.Microservice{Namespace: "ns", Name: "app", Kind: "Deployment"}, window, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Severity != SeverityUnknown {
		t.Fatalf("missing metrics must not be reported healthy, got %s", report.Severity)
	}
	for _, signal := range report.Signals {
		if signal.Severity != SeverityUnknown || signal.Error == "" {
			t.Errorf("missing signal should be explicit: %+v", signal)
		}
	}
}

type partialQuerier struct{}

func (partialQuerier) QueryPrometheusRange(_ context.Context, query string, start, _ time.Time, _ time.Duration) ([]integrations.TimeSeries, error) {
	if !strings.Contains(query, "container_cpu_usage_seconds_total") {
		return nil, nil
	}
	return []integrations.TimeSeries{{Values: []integrations.Sample{{Timestamp: unixSeconds(start), Value: 25}}}}, nil
}

func TestOneHealthySignalDoesNotHideUnknownSignals(t *testing.T) {
	service := New(partialQuerier{}, Config{})
	window, _ := ParseWindow("1h")
	report, err := service.Query(context.Background(), collect.Microservice{Namespace: "ns", Name: "app", Kind: "Deployment"}, window, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Signals[1].Severity != SeverityHealthy || report.Severity != SeverityUnknown {
		t.Fatalf("partial telemetry must keep aggregate state unknown: %+v", report)
	}
}

type emptyQuerier struct{}

func (emptyQuerier) QueryPrometheusRange(context.Context, string, time.Time, time.Time, time.Duration) ([]integrations.TimeSeries, error) {
	return nil, nil
}

func TestWorkloadPodRegexEscapesNames(t *testing.T) {
	got := workloadPodRegex("Deployment", "video.api")
	if got != `^video\.api-[a-z0-9]{8,10}-[a-z0-9]{5}$` {
		t.Fatalf("unexpected escaped pod regex %q", got)
	}
}

func TestSingleReplicaFailureIsCritical(t *testing.T) {
	service := New(&fakeQuerier{}, Config{})
	window, _ := ParseWindow("1h")
	report, err := service.Query(context.Background(), collect.Microservice{
		Namespace: "production", Name: "singleton", Kind: "Deployment", DesiredReplica: 1,
	}, window, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// The fake returns two waiting-pod samples at the end. A singleton must not
	// stay merely warning when its only replica is unavailable.
	signal := report.Signals[3]
	if signal.Critical != 1 || signal.Severity != SeverityCritical {
		t.Fatalf("single-replica failure should be critical: %+v", signal)
	}
}
