// Package servicehealth defines Beholdr-owned PromQL queries and turns their
// results into bounded, per-service health signals for the API and UI.
package servicehealth

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/delangetimm/beholdr/internal/collect"
	"github.com/delangetimm/beholdr/internal/integrations"
)

type RangeQuerier interface {
	QueryPrometheusRange(context.Context, string, time.Time, time.Time, time.Duration) ([]integrations.TimeSeries, error)
}

type Config struct {
	HTTPRequestsMetric string
	HTTPErrorsMetric   string
	HTTPStatusLabel    string
	AppNamespaceLabel  string
	AppServiceLabel    string
	AppPodLabel        string
	KubeNamespaceLabel string
	KubePodLabel       string
	Thresholds         Thresholds
}

type Thresholds struct {
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
}

type Window struct {
	Name     string
	Duration time.Duration
	Step     time.Duration
}

var windows = map[string]Window{
	"1h":  {Name: "1h", Duration: time.Hour, Step: 15 * time.Second},
	"6h":  {Name: "6h", Duration: 6 * time.Hour, Step: time.Minute},
	"24h": {Name: "24h", Duration: 24 * time.Hour, Step: 5 * time.Minute},
	"7d":  {Name: "7d", Duration: 7 * 24 * time.Hour, Step: 30 * time.Minute},
	"21d": {Name: "21d", Duration: 21 * 24 * time.Hour, Step: time.Hour},
}

var promIdentifier = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)

type Service struct {
	query RangeQuerier
	cfg   Config
}

type Severity string

const (
	SeverityUnknown  Severity = "unknown"
	SeverityHealthy  Severity = "healthy"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

type Point map[string]float64

type Line struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Color string `json:"color"`
}

type Signal struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Unit        string   `json:"unit"`
	Description string   `json:"description"`
	Current     *float64 `json:"current,omitempty"`
	Previous    *float64 `json:"previous,omitempty"`
	Difference  *float64 `json:"difference,omitempty"`
	Warning     float64  `json:"warning"`
	Critical    float64  `json:"critical"`
	Severity    Severity `json:"severity"`
	Lines       []Line   `json:"lines"`
	Points      []Point  `json:"points"`
	Error       string   `json:"error,omitempty"`
}

type Report struct {
	Namespace string   `json:"namespace"`
	Service   string   `json:"service"`
	Window    string   `json:"window"`
	Start     float64  `json:"start"`
	End       float64  `json:"end"`
	Step      float64  `json:"step"`
	Severity  Severity `json:"severity"`
	Signals   []Signal `json:"signals"`
}

func DefaultConfig() Config {
	return Config{
		HTTPRequestsMetric: "aspnetcore_requests_duration_seconds_count",
		HTTPErrorsMetric:   "",
		HTTPStatusLabel:    "code",
		AppNamespaceLabel:  "kubernetes_namespace",
		AppServiceLabel:    "app_kubernetes_io_name",
		AppPodLabel:        "kubernetes_pod_name",
		KubeNamespaceLabel: "namespace",
		KubePodLabel:       "pod",
		Thresholds: Thresholds{
			ErrorRateWarning:      1,
			ErrorRateCritical:     5,
			ErrorIncreaseWarning:  0.5,
			ErrorIncreaseCritical: 2,
			CPUWarning:            80,
			CPUCritical:           95,
			MemoryWarning:         80,
			MemoryCritical:        95,
			FailingPodsWarning:    1,
			FailingPodsCritical:   2,
		},
	}
}

func New(query RangeQuerier, cfg Config) *Service {
	defaults := DefaultConfig()
	cfg.HTTPRequestsMetric = identifierOr(cfg.HTTPRequestsMetric, defaults.HTTPRequestsMetric)
	cfg.HTTPErrorsMetric = identifierOr(cfg.HTTPErrorsMetric, defaults.HTTPErrorsMetric)
	cfg.HTTPStatusLabel = optionalIdentifier(cfg.HTTPStatusLabel, defaults.HTTPStatusLabel)
	cfg.AppNamespaceLabel = identifierOr(cfg.AppNamespaceLabel, defaults.AppNamespaceLabel)
	cfg.AppServiceLabel = optionalIdentifier(cfg.AppServiceLabel, defaults.AppServiceLabel)
	cfg.AppPodLabel = identifierOr(cfg.AppPodLabel, defaults.AppPodLabel)
	cfg.KubeNamespaceLabel = identifierOr(cfg.KubeNamespaceLabel, defaults.KubeNamespaceLabel)
	cfg.KubePodLabel = identifierOr(cfg.KubePodLabel, defaults.KubePodLabel)
	cfg.Thresholds = thresholdsOr(cfg.Thresholds, defaults.Thresholds)
	return &Service{query: query, cfg: cfg}
}

func ParseWindow(value string) (Window, bool) {
	if value == "" {
		return windows["24h"], true
	}
	w, ok := windows[value]
	return w, ok
}

func (s *Service) Query(ctx context.Context, workload collect.Microservice, window Window, end time.Time) (Report, error) {
	if s.query == nil {
		return Report{}, integrations.ErrPrometheusNotConfigured
	}
	start := end.Add(-window.Duration)
	queries := s.queries(workload)
	results := make(map[string][]integrations.TimeSeries, len(queries))
	errs := make(map[string]error, len(queries))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for key, query := range queries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			series, err := s.query.QueryPrometheusRange(ctx, query, start, end, window.Step)
			mu.Lock()
			results[key], errs[key] = series, err
			mu.Unlock()
		}()
	}
	wg.Wait()

	for _, err := range errs {
		if errors.Is(err, integrations.ErrPrometheusNotConfigured) {
			return Report{}, err
		}
	}

	report := Report{
		Namespace: workload.Namespace,
		Service:   workload.Name,
		Window:    window.Name,
		Start:     unixSeconds(start),
		End:       unixSeconds(end),
		Step:      window.Step.Seconds(),
		Severity:  SeverityHealthy,
	}
	report.Signals = []Signal{
		s.errorSignal(results["errors"], results["errors_previous"], errs["errors"], errs["errors_previous"]),
		s.simpleSignal("cpu", "CPU usage", "% of requests", "Container CPU usage divided by configured CPU requests.", s.cfg.Thresholds.CPUWarning, s.cfg.Thresholds.CPUCritical, results["cpu"], errs["cpu"], "current", "CPU", "#818cf8"),
		s.simpleSignal("memory", "Memory usage", "% of limits", "Container working set divided by configured memory limits.", s.cfg.Thresholds.MemoryWarning, s.cfg.Thresholds.MemoryCritical, results["memory"], errs["memory"], "current", "Memory", "#10b981"),
		s.failingPodsSignal(workload.DesiredReplica, results["waiting"], results["failed_phase"], errs["waiting"], errs["failed_phase"]),
	}
	for _, signal := range report.Signals {
		report.Severity = maxSeverity(report.Severity, signal.Severity)
	}
	return report, nil
}

func (s *Service) queries(workload collect.Microservice) map[string]string {
	podRegex := workloadPodRegex(workload.Kind, workload.Name)
	appScope := matcher(s.cfg.AppNamespaceLabel, "=", workload.Namespace)
	if s.cfg.AppServiceLabel != "" {
		appScope += "," + matcher(s.cfg.AppServiceLabel, "=", workload.Name)
	} else {
		appScope += "," + matcher(s.cfg.AppPodLabel, "=~", podRegex)
	}
	kubeScope := matcher(s.cfg.KubeNamespaceLabel, "=", workload.Namespace) + "," + matcher(s.cfg.KubePodLabel, "=~", podRegex)

	rate := func(metric, selector, offset string) string {
		return fmt.Sprintf("rate(%s{%s}[5m]%s)", metric, selector, offset)
	}
	errorRate := func(offset string) string {
		errorMetric := s.cfg.HTTPErrorsMetric
		errorScope := appScope
		if errorMetric == "" {
			errorMetric = s.cfg.HTTPRequestsMetric
			errorScope += "," + matcher(s.cfg.HTTPStatusLabel, "=~", "5..")
		}
		return fmt.Sprintf("100 * sum(%s) / clamp_min(sum(%s), 0.000001)", rate(errorMetric, errorScope, offset), rate(s.cfg.HTTPRequestsMetric, appScope, offset))
	}

	return map[string]string{
		"errors":          errorRate(""),
		"errors_previous": errorRate(" offset 1w"),
		"cpu": fmt.Sprintf(
			`100 * sum(rate(container_cpu_usage_seconds_total{%s,container!="",container!="POD"}[5m])) / clamp_min(sum(kube_pod_container_resource_requests{%s,container!="",resource="cpu",unit="core"} > 0), 0.001)`,
			kubeScope, kubeScope,
		),
		"memory": fmt.Sprintf(
			`100 * sum(container_memory_working_set_bytes{%s,container!="",container!="POD"}) / clamp_min(sum(kube_pod_container_resource_limits{%s,container!="",resource="memory",unit="byte"} > 0), 1)`,
			kubeScope, kubeScope,
		),
		"waiting": fmt.Sprintf(
			`(sum(max by (%s,%s) (kube_pod_container_status_waiting_reason{%s,reason=~"CrashLoopBackOff|ImagePullBackOff|ErrImagePull|CreateContainerConfigError"} == 1)) or 0 * count(kube_pod_info{%s}))`,
			s.cfg.KubeNamespaceLabel, s.cfg.KubePodLabel, kubeScope, kubeScope,
		),
		"failed_phase": fmt.Sprintf(
			`(sum(kube_pod_status_phase{%s,phase=~"Failed|Unknown"} == 1) or 0 * count(kube_pod_info{%s}))`,
			kubeScope, kubeScope,
		),
	}
}

func (s *Service) errorSignal(currentSeries, previousSeries []integrations.TimeSeries, currentErr, previousErr error) Signal {
	t := s.cfg.Thresholds
	signal := Signal{
		Key: "error_rate", Label: "HTTP error rate", Unit: "%",
		Description: "HTTP 5xx responses as a percentage of all requests, compared with the same time one week earlier.",
		Warning:     t.ErrorRateWarning, Critical: t.ErrorRateCritical, Severity: SeverityUnknown,
		Lines:  []Line{{Key: "current", Label: "Current", Color: "#f43f5e"}, {Key: "week_ago", Label: "Week before", Color: "#64748b"}},
		Points: mergeSeries(map[string][]integrations.TimeSeries{"current": currentSeries, "week_ago": previousSeries}),
	}
	if currentErr != nil {
		signal.Error = "Prometheus query failed"
		return signal
	}
	signal.Current = lastValue(currentSeries)
	signal.Previous = lastValue(previousSeries)
	if signal.Current == nil {
		signal.Error = "No matching HTTP request metric"
		return signal
	}
	signal.Severity = thresholdSeverity(*signal.Current, t.ErrorRateWarning, t.ErrorRateCritical)
	if previousErr == nil && signal.Previous != nil {
		difference := *signal.Current - *signal.Previous
		signal.Difference = &difference
		if difference > 0 {
			signal.Severity = maxSeverity(signal.Severity, thresholdSeverity(difference, t.ErrorIncreaseWarning, t.ErrorIncreaseCritical))
		}
	}
	return signal
}

func (s *Service) simpleSignal(key, label, unit, description string, warning, critical float64, series []integrations.TimeSeries, queryErr error, lineKey, lineLabel, color string) Signal {
	signal := Signal{
		Key: key, Label: label, Unit: unit, Description: description,
		Warning: warning, Critical: critical, Severity: SeverityUnknown,
		Lines:  []Line{{Key: lineKey, Label: lineLabel, Color: color}},
		Points: mergeSeries(map[string][]integrations.TimeSeries{lineKey: series}),
	}
	if queryErr != nil {
		signal.Error = "Prometheus query failed"
		return signal
	}
	signal.Current = lastValue(series)
	if signal.Current == nil {
		signal.Error = "No matching metric"
		return signal
	}
	signal.Severity = thresholdSeverity(*signal.Current, warning, critical)
	return signal
}

func (s *Service) failingPodsSignal(desired int32, waiting, failed []integrations.TimeSeries, waitingErr, failedErr error) Signal {
	t := s.cfg.Thresholds
	warning := math.Max(t.FailingPodsWarning, math.Ceil(float64(desired)*0.10))
	critical := math.Max(t.FailingPodsCritical, math.Ceil(float64(desired)*0.25))
	if desired <= 1 {
		critical = 1
	}
	signal := Signal{
		Key: "failing_pods", Label: "Failing pods", Unit: "pods",
		Description: "Pods in failed/unknown phases or containers blocked by crash and image/config errors.",
		Warning:     warning, Critical: critical, Severity: SeverityUnknown,
		Lines:  []Line{{Key: "current", Label: "Failing pods", Color: "#f59e0b"}},
		Points: sumSeries("current", waiting, failed),
	}
	if waitingErr != nil && failedErr != nil {
		signal.Error = "Prometheus query failed"
		return signal
	}
	w, f := lastValue(waiting), lastValue(failed)
	if w == nil && f == nil {
		signal.Error = "No matching pod-state metrics"
		return signal
	}
	value := valueOrZero(w) + valueOrZero(f)
	signal.Current = &value
	signal.Severity = thresholdSeverity(value, warning, critical)
	return signal
}

func mergeSeries(series map[string][]integrations.TimeSeries) []Point {
	byTime := map[float64]Point{}
	for key, all := range series {
		if len(all) == 0 {
			continue
		}
		for _, sample := range all[0].Values {
			point := byTime[sample.Timestamp]
			if point == nil {
				point = Point{"t": sample.Timestamp}
				byTime[sample.Timestamp] = point
			}
			point[key] = sample.Value
		}
	}
	return sortedPoints(byTime)
}

func sumSeries(key string, groups ...[]integrations.TimeSeries) []Point {
	byTime := map[float64]Point{}
	for _, all := range groups {
		if len(all) == 0 {
			continue
		}
		for _, sample := range all[0].Values {
			point := byTime[sample.Timestamp]
			if point == nil {
				point = Point{"t": sample.Timestamp}
				byTime[sample.Timestamp] = point
			}
			point[key] += sample.Value
		}
	}
	return sortedPoints(byTime)
}

func sortedPoints(byTime map[float64]Point) []Point {
	timestamps := make([]float64, 0, len(byTime))
	for timestamp := range byTime {
		timestamps = append(timestamps, timestamp)
	}
	sort.Float64s(timestamps)
	points := make([]Point, 0, len(timestamps))
	for _, timestamp := range timestamps {
		points = append(points, byTime[timestamp])
	}
	return points
}

func lastValue(series []integrations.TimeSeries) *float64 {
	if len(series) == 0 || len(series[0].Values) == 0 {
		return nil
	}
	value := series[0].Values[len(series[0].Values)-1].Value
	return &value
}

func matcher(label, operator, value string) string {
	return label + operator + strconv.Quote(value)
}

func workloadPodRegex(kind, name string) string {
	name = regexp.QuoteMeta(name)
	switch kind {
	case "Deployment":
		return "^" + name + "-[a-z0-9]{8,10}-[a-z0-9]{5}$"
	case "StatefulSet":
		return "^" + name + "-[0-9]+$"
	case "DaemonSet":
		return "^" + name + "-[a-z0-9]{5}$"
	default:
		return "^" + name + "(-.+)?$"
	}
}

func identifierOr(value, fallback string) string {
	if promIdentifier.MatchString(value) {
		return value
	}
	return fallback
}

func optionalIdentifier(value, fallback string) string {
	if value == "" {
		return fallback
	}
	if promIdentifier.MatchString(value) {
		return value
	}
	return fallback
}

func thresholdsOr(value, fallback Thresholds) Thresholds {
	if value.ErrorRateWarning <= 0 {
		value.ErrorRateWarning = fallback.ErrorRateWarning
	}
	if value.ErrorRateCritical <= value.ErrorRateWarning {
		value.ErrorRateCritical = fallback.ErrorRateCritical
	}
	if value.ErrorIncreaseWarning <= 0 {
		value.ErrorIncreaseWarning = fallback.ErrorIncreaseWarning
	}
	if value.ErrorIncreaseCritical <= value.ErrorIncreaseWarning {
		value.ErrorIncreaseCritical = fallback.ErrorIncreaseCritical
	}
	if value.CPUWarning <= 0 {
		value.CPUWarning = fallback.CPUWarning
	}
	if value.CPUCritical <= value.CPUWarning {
		value.CPUCritical = fallback.CPUCritical
	}
	if value.MemoryWarning <= 0 {
		value.MemoryWarning = fallback.MemoryWarning
	}
	if value.MemoryCritical <= value.MemoryWarning {
		value.MemoryCritical = fallback.MemoryCritical
	}
	if value.FailingPodsWarning <= 0 {
		value.FailingPodsWarning = fallback.FailingPodsWarning
	}
	if value.FailingPodsCritical <= value.FailingPodsWarning {
		value.FailingPodsCritical = fallback.FailingPodsCritical
	}
	return value
}

func thresholdSeverity(value, warning, critical float64) Severity {
	switch {
	case value >= critical:
		return SeverityCritical
	case value >= warning:
		return SeverityWarning
	default:
		return SeverityHealthy
	}
}

func maxSeverity(a, b Severity) Severity {
	// Unknown outranks healthy so one missing signal cannot make the service's
	// aggregate state look green. A real warning/critical still takes priority.
	rank := map[Severity]int{SeverityHealthy: 0, SeverityUnknown: 1, SeverityWarning: 2, SeverityCritical: 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func valueOrZero(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func unixSeconds(t time.Time) float64 {
	return float64(t.UnixNano()) / 1e9
}
