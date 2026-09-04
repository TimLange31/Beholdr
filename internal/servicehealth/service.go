// Package servicehealth defines Beholdr-owned PromQL queries and turns their
// results into bounded, per-service health signals for the API and UI.
//
// Two evaluations back every signal. The chart comes from a range query over
// the operator's selected window; the score comes from an instant query at
// "now". They are deliberately separate: a range query's last point is only as
// fresh as that range's step, so scoring from it would make a service's
// severity depend on which chart window happened to be selected — a spike in
// the last ten minutes would be invisible on the 21-day view.
package servicehealth

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/delangetimm/beholdr/internal/collect"
	"github.com/delangetimm/beholdr/internal/integrations"
)

// Querier is the Prometheus surface this package consumes. Beholdr owns every
// query template; nothing here is ever built from an HTTP request body.
type Querier interface {
	QueryPrometheusRange(context.Context, string, time.Time, time.Time, time.Duration) ([]integrations.TimeSeries, error)
	QueryPrometheusInstant(context.Context, string, time.Time) ([]integrations.InstantSample, error)
}

// CPUBasis selects what container CPU usage is compared against.
type CPUBasis string

const (
	// CPUBasisLimits scores CPU against the container's CPU limit, where
	// exceeding the value means throttling. This is the default because it is
	// the only basis on which a percentage over 100 is actually a problem.
	CPUBasisLimits CPUBasis = "limits"
	// CPUBasisRequests scores CPU against requests. Requests are a scheduling
	// floor, not a ceiling — healthy bursty services routinely run at several
	// hundred percent of request — so thresholds must be set accordingly.
	CPUBasisRequests CPUBasis = "requests"
)

type Config struct {
	HTTPRequestsMetric string
	HTTPErrorsMetric   string
	HTTPStatusLabel    string
	AppNamespaceLabel  string
	AppServiceLabel    string
	AppPodLabel        string
	KubeNamespaceLabel string
	KubePodLabel       string
	CPUBasis           CPUBasis
	Thresholds         Thresholds
	// CacheTTL is how long a completed report is reused. Zero selects the
	// default; negative disables caching.
	CacheTTL time.Duration
	// MaxConcurrentQueries bounds how many Prometheus queries this process has
	// in flight at once, across all callers.
	MaxConcurrentQueries int
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

// comparisonOffset is how far back the "week before" overlay reaches.
const comparisonOffset = 7 * 24 * time.Hour

// Comparable reports whether a week-before overlay is meaningful for this
// window. Beyond one week the offset series overlaps the current series — the
// same wall-clock samples drawn twice — which is a comparison in appearance
// only, so it is suppressed rather than shown.
func (w Window) Comparable() bool { return w.Duration <= comparisonOffset }

var windows = map[string]Window{
	"1h":  {Name: "1h", Duration: time.Hour, Step: 15 * time.Second},
	"6h":  {Name: "6h", Duration: 6 * time.Hour, Step: time.Minute},
	"24h": {Name: "24h", Duration: 24 * time.Hour, Step: 5 * time.Minute},
	"7d":  {Name: "7d", Duration: 7 * 24 * time.Hour, Step: 30 * time.Minute},
	"21d": {Name: "21d", Duration: 21 * 24 * time.Hour, Step: time.Hour},
}

var promIdentifier = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)

const (
	defaultCacheTTL             = 30 * time.Second
	defaultMaxConcurrentQueries = 6
	maxCacheEntries             = 512
)

type Service struct {
	query Querier
	cfg   Config
	cache *reportCache
	sem   chan struct{}
}

type Severity string

const (
	SeverityUnknown  Severity = "unknown"
	SeverityHealthy  Severity = "healthy"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// State separates "this signal was measured" from the two ways it can be
// absent. The distinction matters for the aggregate: a query that failed is a
// gap in Beholdr's knowledge and must not read as green, but a metric that
// simply does not exist for this workload — no memory limit configured, no HTTP
// traffic — says nothing about the service's health and must not drag every
// other signal down with it.
type State string

const (
	StateOK     State = "ok"
	StateNoData State = "no_data"
	StateError  State = "error"
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
	// Warning is omitted when there is no meaningful warning band — a
	// single-replica service, where one failing pod is already critical.
	Warning  *float64 `json:"warning,omitempty"`
	Critical float64  `json:"critical"`
	Severity Severity `json:"severity"`
	State    State    `json:"state"`
	Lines    []Line   `json:"lines"`
	Points   []Point  `json:"points"`
	Error    string   `json:"error,omitempty"`
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
	// Compared reports whether the week-before overlay was evaluated for this
	// window, so the UI can explain its absence rather than silently omitting a
	// line the legend promises.
	Compared bool `json:"compared"`
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
		CPUBasis:           CPUBasisLimits,
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

// New validates cfg and builds the service. Unset fields take the defaults;
// values that are present but unusable are an error rather than a silent
// substitution, because quietly replacing a bad threshold can invert a pair
// (warning 10 / critical 5) and quietly replacing a bad metric name sends every
// query at a metric the operator never chose.
func New(query Querier, cfg Config) (*Service, error) {
	defaults := DefaultConfig()
	var err error
	if cfg.HTTPRequestsMetric, err = identifier("http requests metric", cfg.HTTPRequestsMetric, defaults.HTTPRequestsMetric, false); err != nil {
		return nil, err
	}
	if cfg.HTTPErrorsMetric, err = identifier("http errors metric", cfg.HTTPErrorsMetric, defaults.HTTPErrorsMetric, true); err != nil {
		return nil, err
	}
	if cfg.HTTPStatusLabel, err = identifier("http status label", cfg.HTTPStatusLabel, defaults.HTTPStatusLabel, true); err != nil {
		return nil, err
	}
	if cfg.AppNamespaceLabel, err = identifier("app namespace label", cfg.AppNamespaceLabel, defaults.AppNamespaceLabel, false); err != nil {
		return nil, err
	}
	if cfg.AppServiceLabel, err = identifier("app service label", cfg.AppServiceLabel, defaults.AppServiceLabel, true); err != nil {
		return nil, err
	}
	if cfg.AppPodLabel, err = identifier("app pod label", cfg.AppPodLabel, defaults.AppPodLabel, false); err != nil {
		return nil, err
	}
	if cfg.KubeNamespaceLabel, err = identifier("kube namespace label", cfg.KubeNamespaceLabel, defaults.KubeNamespaceLabel, false); err != nil {
		return nil, err
	}
	if cfg.KubePodLabel, err = identifier("kube pod label", cfg.KubePodLabel, defaults.KubePodLabel, false); err != nil {
		return nil, err
	}
	switch cfg.CPUBasis {
	case "":
		cfg.CPUBasis = defaults.CPUBasis
	case CPUBasisLimits, CPUBasisRequests:
	default:
		return nil, fmt.Errorf("service health: cpu basis must be %q or %q, got %q", CPUBasisLimits, CPUBasisRequests, cfg.CPUBasis)
	}
	if cfg.Thresholds, err = validateThresholds(cfg.Thresholds, defaults.Thresholds); err != nil {
		return nil, err
	}
	if cfg.HTTPErrorsMetric != "" && cfg.HTTPStatusLabel == "" {
		// Fine: an explicit error metric does not need a status filter.
		_ = cfg.HTTPStatusLabel
	}
	if cfg.HTTPErrorsMetric == "" && cfg.HTTPStatusLabel == "" {
		return nil, errors.New("service health: either an http errors metric or an http status label is required")
	}
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = defaultCacheTTL
	}
	if cfg.MaxConcurrentQueries <= 0 {
		cfg.MaxConcurrentQueries = defaultMaxConcurrentQueries
	}
	return &Service{
		query: query,
		cfg:   cfg,
		cache: newReportCache(cfg.CacheTTL, maxCacheEntries),
		sem:   make(chan struct{}, cfg.MaxConcurrentQueries),
	}, nil
}

func ParseWindow(value string) (Window, bool) {
	if value == "" {
		return windows["24h"], true
	}
	w, ok := windows[value]
	return w, ok
}

// Query returns the report for one workload and window. Identical concurrent
// requests share a single evaluation and completed reports are reused for
// CacheTTL: the UI polls this endpoint per open tab, Beholdr has no
// authentication of its own, and every uncached call is ten-plus range and
// instant queries against the same Prometheus an operator is depending on
// during an incident.
func (s *Service) Query(ctx context.Context, workload collect.Microservice, podNames []string, window Window, end time.Time) (Report, error) {
	key := workload.Namespace + "/" + workload.Name + "@" + window.Name
	return s.cache.do(ctx, key, func(ctx context.Context) (Report, error) {
		return s.evaluate(ctx, workload, podNames, window, end)
	})
}

func (s *Service) evaluate(ctx context.Context, workload collect.Microservice, podNames []string, window Window, end time.Time) (Report, error) {
	if s.query == nil {
		return Report{}, integrations.ErrPrometheusNotConfigured
	}
	start := end.Add(-window.Duration)
	queries := s.queries(workload, window)
	scoring := s.scoringQueries(workload, podNames, window)

	results := make(map[string]queryOutcome, len(queries))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for key, query := range queries {
		wg.Add(1)
		go func(key, query string) {
			defer wg.Done()
			var got queryOutcome
			// The chart and the score are two evaluations of the same
			// template. A failure of either marks the signal as errored.
			got.series, got.err = s.runRange(ctx, query, start, end, window.Step)
			if value, err := s.runInstant(ctx, scoring[key], end); err != nil {
				if got.err == nil {
					got.err = err
				}
			} else {
				got.instant = value
			}
			mu.Lock()
			results[key] = got
			mu.Unlock()
		}(key, query)
	}
	wg.Wait()

	for _, got := range results {
		if errors.Is(got.err, integrations.ErrPrometheusNotConfigured) {
			return Report{}, got.err
		}
	}

	report := Report{
		Namespace: workload.Namespace,
		Service:   workload.Name,
		Window:    window.Name,
		Start:     unixSeconds(start),
		End:       unixSeconds(end),
		Step:      window.Step.Seconds(),
		Compared:  window.Comparable(),
	}
	report.Signals = []Signal{
		s.errorSignal(results["errors"], results["errors_previous"], window.Comparable()),
		s.simpleSignal(signalSpec{
			key: "cpu", label: "CPU usage", unit: s.cpuUnit(), description: s.cpuDescription(),
			warning: s.cfg.Thresholds.CPUWarning, critical: s.cfg.Thresholds.CPUCritical,
			lineKey: "current", lineLabel: "CPU", color: "#818cf8",
		}, results["cpu"]),
		s.simpleSignal(signalSpec{
			key: "memory", label: "Memory usage", unit: "% of limits",
			description: "Container working set divided by configured memory limits.",
			warning:     s.cfg.Thresholds.MemoryWarning, critical: s.cfg.Thresholds.MemoryCritical,
			lineKey: "current", lineLabel: "Memory", color: "#10b981",
		}, results["memory"]),
		s.failingPodsSignal(workload.DesiredReplica, results["waiting"], results["failed_phase"]),
	}
	report.Severity = aggregate(report.Signals)
	return report, nil
}

// runRange and runInstant hold the shared semaphore for the duration of one
// Prometheus call, so a burst of requests queues instead of multiplying.
func (s *Service) runRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]integrations.TimeSeries, error) {
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.query.QueryPrometheusRange(ctx, query, start, end, step)
}

func (s *Service) runInstant(ctx context.Context, query string, at time.Time) (*float64, error) {
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	samples, err := s.query.QueryPrometheusInstant(ctx, query, at)
	if err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, nil
	}
	if len(samples) > 1 {
		// Every template here aggregates to a single series. More than one
		// means the query no longer matches its contract, and silently taking
		// the first would report a confident wrong number.
		return nil, fmt.Errorf("service health: expected one series, got %d", len(samples))
	}
	value := samples[0].Sample.Value
	return &value, nil
}

func (s *Service) cpuUnit() string {
	if s.cfg.CPUBasis == CPUBasisRequests {
		return "% of requests"
	}
	return "% of limits"
}

func (s *Service) cpuDescription() string {
	if s.cfg.CPUBasis == CPUBasisRequests {
		return "Container CPU usage divided by configured CPU requests. Requests are a scheduling floor, not a ceiling — values above 100% are normal for bursty services."
	}
	return "Container CPU usage divided by configured CPU limits. Sustained values near 100% mean the container is being throttled."
}

// queries builds the chart (range) templates, which select pods by name shape
// so that pods replaced by earlier rollouts stay in the history.
func (s *Service) queries(workload collect.Microservice, window Window) map[string]string {
	return s.buildQueries(workload, window, workloadPodRegex(workload.Kind, workload.Name))
}

// scoringQueries builds the templates the severity is computed from. When the
// collector knows which pods the workload currently has, they are matched
// exactly: name-shape matching cannot always separate a workload called "api"
// from one called "api-gateway", and a badge that alerts on another service's
// pods is worse than a chart that does. Falls back to the shape when the
// workload has no pods right now.
func (s *Service) scoringQueries(workload collect.Microservice, podNames []string, window Window) map[string]string {
	selector := exactPodRegex(podNames)
	if selector == "" {
		selector = workloadPodRegex(workload.Kind, workload.Name)
	}
	return s.buildQueries(workload, window, selector)
}

func exactPodRegex(names []string) string {
	if len(names) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		if name != "" {
			quoted = append(quoted, regexp.QuoteMeta(name))
		}
	}
	if len(quoted) == 0 {
		return ""
	}
	sort.Strings(quoted)
	return "^(" + strings.Join(quoted, "|") + ")$"
}

func (s *Service) buildQueries(workload collect.Microservice, window Window, podRegex string) map[string]string {
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

	cpuDenominator := "kube_pod_container_resource_limits"
	if s.cfg.CPUBasis == CPUBasisRequests {
		cpuDenominator = "kube_pod_container_resource_requests"
	}

	out := map[string]string{
		"errors": errorRate(""),
		"cpu": fmt.Sprintf(
			`100 * sum(rate(container_cpu_usage_seconds_total{%s,container!="",container!="POD"}[5m])) / clamp_min(sum(%s{%s,container!="",resource="cpu",unit="core"} > 0), 0.001)`,
			kubeScope, cpuDenominator, kubeScope,
		),
		"memory": fmt.Sprintf(
			`100 * sum(container_memory_working_set_bytes{%s,container!="",container!="POD"}) / clamp_min(sum(kube_pod_container_resource_limits{%s,container!="",resource="memory",unit="byte"} > 0), 1)`,
			kubeScope, kubeScope,
		),
		"waiting": fmt.Sprintf(
			`(sum(max by (%s,%s) (kube_pod_container_status_waiting_reason{%s,reason=~"CrashLoopBackOff|ImagePullBackOff|ErrImagePull|CreateContainerConfigError"} == 1)) or 0 * count(kube_pod_info{%s}))`,
			s.cfg.KubeNamespaceLabel, s.cfg.KubePodLabel, kubeScope, kubeScope,
		),
		// Job pods are excluded: a Pod that failed weeks ago keeps reporting
		// phase="Failed" for as long as the object exists, so counting them
		// would pin a namespace to critical until someone reaped it by hand.
		"failed_phase": fmt.Sprintf(
			`(sum(kube_pod_status_phase{%s,phase=~"Failed|Unknown"} == 1 unless on (%s,%s) kube_pod_owner{%s,owner_kind="Job"}) or 0 * count(kube_pod_info{%s}))`,
			kubeScope, s.cfg.KubeNamespaceLabel, s.cfg.KubePodLabel, kubeScope, kubeScope,
		),
	}
	if window.Comparable() {
		out["errors_previous"] = errorRate(" offset 1w")
	}
	return out
}

type signalSpec struct {
	key, label, unit, description string
	warning, critical             float64
	lineKey, lineLabel, color     string
}

func (s *Service) errorSignal(current, previous queryOutcome, compared bool) Signal {
	t := s.cfg.Thresholds
	warning := t.ErrorRateWarning
	lines := []Line{{Key: "current", Label: "Current", Color: "#f43f5e"}}
	series := map[string][]integrations.TimeSeries{"current": current.series}
	if compared {
		lines = append(lines, Line{Key: "week_ago", Label: "Week before", Color: "#64748b"})
		series["week_ago"] = previous.series
	}
	signal := Signal{
		Key: "error_rate", Label: "HTTP error rate", Unit: "%",
		Description: s.errorDescription(compared),
		Warning:     &warning, Critical: t.ErrorRateCritical,
		Severity: SeverityUnknown, State: StateError,
		Lines:  lines,
		Points: mergeSeries(series),
	}
	if current.err != nil {
		signal.Error = queryErrorMessage(current.err)
		return signal
	}
	signal.Current = current.instant
	if compared && previous.err == nil {
		signal.Previous = previous.instant
	}
	if signal.Current == nil {
		signal.State, signal.Error = StateNoData, "No matching HTTP request metric"
		return signal
	}
	signal.State = StateOK
	signal.Severity = thresholdSeverity(*signal.Current, t.ErrorRateWarning, t.ErrorRateCritical)
	if signal.Previous != nil {
		difference := *signal.Current - *signal.Previous
		signal.Difference = &difference
		if difference > 0 {
			signal.Severity = maxSeverity(signal.Severity, thresholdSeverity(difference, t.ErrorIncreaseWarning, t.ErrorIncreaseCritical))
		}
	}
	return signal
}

func (s *Service) errorDescription(compared bool) string {
	base := "HTTP 5xx responses as a percentage of all requests"
	if compared {
		return base + ", compared with the same time one week earlier."
	}
	return base + ". The week-before overlay is not shown on windows longer than seven days, where it would overlap the current series."
}

func (s *Service) simpleSignal(spec signalSpec, got queryOutcome) Signal {
	warning := spec.warning
	signal := Signal{
		Key: spec.key, Label: spec.label, Unit: spec.unit, Description: spec.description,
		Warning: &warning, Critical: spec.critical,
		Severity: SeverityUnknown, State: StateError,
		Lines:  []Line{{Key: spec.lineKey, Label: spec.lineLabel, Color: spec.color}},
		Points: mergeSeries(map[string][]integrations.TimeSeries{spec.lineKey: got.series}),
	}
	if got.err != nil {
		signal.Error = queryErrorMessage(got.err)
		return signal
	}
	signal.Current = got.instant
	if signal.Current == nil {
		signal.State, signal.Error = StateNoData, "No matching metric for this workload"
		return signal
	}
	signal.State = StateOK
	signal.Severity = thresholdSeverity(*signal.Current, spec.warning, spec.critical)
	return signal
}

func (s *Service) failingPodsSignal(desired int32, waiting, failed queryOutcome) Signal {
	t := s.cfg.Thresholds
	warning := math.Max(t.FailingPodsWarning, math.Ceil(float64(desired)*0.10))
	critical := math.Max(t.FailingPodsCritical, math.Ceil(float64(desired)*0.25))
	warningPtr := &warning
	if desired <= 1 {
		// One failing pod is the whole service. There is no band between
		// "degraded" and "down", so no warning threshold is reported rather
		// than reporting one equal to critical.
		critical = 1
		warningPtr = nil
	}
	signal := Signal{
		Key: "failing_pods", Label: "Failing pods", Unit: "pods",
		Description: "Pods in Failed/Unknown phase (excluding Job pods) plus containers blocked by crash and image/config errors.",
		Warning:     warningPtr, Critical: critical,
		Severity: SeverityUnknown, State: StateError,
		Lines:  []Line{{Key: "current", Label: "Failing pods", Color: "#f59e0b"}},
		Points: sumSeries("current", waiting.series, failed.series),
	}
	if waiting.err != nil && failed.err != nil {
		signal.Error = queryErrorMessage(waiting.err)
		return signal
	}
	if waiting.instant == nil && failed.instant == nil {
		signal.State, signal.Error = StateNoData, "No matching pod-state metrics"
		return signal
	}
	value := valueOrZero(waiting.instant) + valueOrZero(failed.instant)
	signal.Current = &value
	signal.State = StateOK
	warningValue := critical
	if warningPtr != nil {
		warningValue = *warningPtr
	}
	signal.Severity = thresholdSeverity(value, warningValue, critical)
	return signal
}

// queryOutcome is the per-query result carried from evaluate into the signal
// builders. It exists so the builders take one value rather than three
// positional arguments that are easy to transpose.
type queryOutcome struct {
	series  []integrations.TimeSeries
	instant *float64
	err     error
}

// aggregate rolls the signals up. Signals that could not be measured (no such
// metric for this workload) are skipped, because a service without a memory
// limit is not thereby in an unknown state. Signals whose query failed do
// count as unknown: that is a gap in what Beholdr knows, and it outranks
// healthy so one broken query cannot paint a service green.
func aggregate(signals []Signal) Severity {
	worst := SeverityHealthy
	measured := false
	for _, signal := range signals {
		switch signal.State {
		case StateOK:
			measured = true
			worst = maxSeverity(worst, signal.Severity)
		case StateError:
			measured = true
			worst = maxSeverity(worst, SeverityUnknown)
		case StateNoData:
			// deliberately ignored
		}
	}
	if !measured {
		return SeverityUnknown
	}
	return worst
}

// queryErrorMessage maps a query failure onto the same closed vocabulary the
// connectivity checks use. Nothing derived from an upstream response body ever
// reaches this string.
func queryErrorMessage(err error) string {
	switch {
	case errors.Is(err, integrations.ErrPrometheusQueryRejected):
		return "Prometheus rejected the query — check the configured metric and label names"
	case errors.Is(err, integrations.ErrPrometheusUnauthorized):
		return "Prometheus rejected Beholdr's credentials"
	case errors.Is(err, integrations.ErrPrometheusTimeout):
		return "Prometheus query timed out — try a shorter range"
	case errors.Is(err, integrations.ErrPrometheusUnavailable):
		return "Prometheus is unavailable"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "Query cancelled"
	}
	return "Prometheus query failed"
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

func matcher(label, operator, value string) string {
	return label + operator + strconv.Quote(value)
}

// workloadPodRegex matches the pods of one workload by name shape. It is
// anchored and segment-counted so that a workload named "api" can never match
// the pods of "api-gateway": every alternative fixes the number of "-"
// separated segments after the workload name.
//
// The collector currently reports only "Deployment" and "Other", so the
// default branch has to cover StatefulSets, DaemonSets and Jobs — it is a union
// of the known shapes rather than a wildcard, which is what made the previous
// "^name(-.+)?$" absorb sibling services' metrics.
//
// One ambiguity is irreducible: pod "api-gateway-abcde" is a valid pod name for
// both a DaemonSet called "api-gateway" and a Deployment called "api". Scoring
// therefore prefers the exact current pod names (see scoringQueries); only the
// historical chart can still be affected.
func workloadPodRegex(kind, name string) string {
	name = regexp.QuoteMeta(name)
	deployment := "^" + name + "-[a-z0-9]+-[a-z0-9]{5}$"
	statefulSet := "^" + name + "-[0-9]+$"
	daemonSet := "^" + name + "-[a-z0-9]{5}$"
	switch kind {
	case "Deployment":
		return deployment
	case "StatefulSet":
		return statefulSet
	case "DaemonSet":
		return daemonSet
	default:
		return strings.Join([]string{deployment, statefulSet, daemonSet}, "|")
	}
}

func identifier(field, value, fallback string, optional bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		if optional {
			return fallback, nil
		}
		if fallback == "" {
			return "", fmt.Errorf("service health: %s is required", field)
		}
		return fallback, nil
	}
	if !promIdentifier.MatchString(value) {
		return "", fmt.Errorf("service health: %s %q is not a valid Prometheus identifier", field, value)
	}
	return value, nil
}

func validateThresholds(value, fallback Thresholds) (Thresholds, error) {
	pairs := []struct {
		name                       string
		warning, critical          *float64
		fbWarning, fbCritical      float64
		allowWarningEqualsCritical bool
	}{
		{"error rate", &value.ErrorRateWarning, &value.ErrorRateCritical, fallback.ErrorRateWarning, fallback.ErrorRateCritical, false},
		{"error increase", &value.ErrorIncreaseWarning, &value.ErrorIncreaseCritical, fallback.ErrorIncreaseWarning, fallback.ErrorIncreaseCritical, false},
		{"cpu", &value.CPUWarning, &value.CPUCritical, fallback.CPUWarning, fallback.CPUCritical, false},
		{"memory", &value.MemoryWarning, &value.MemoryCritical, fallback.MemoryWarning, fallback.MemoryCritical, false},
		{"failing pods", &value.FailingPodsWarning, &value.FailingPodsCritical, fallback.FailingPodsWarning, fallback.FailingPodsCritical, true},
	}
	for _, p := range pairs {
		if *p.warning == 0 {
			*p.warning = p.fbWarning
		}
		if *p.critical == 0 {
			*p.critical = p.fbCritical
		}
		if *p.warning < 0 || *p.critical < 0 {
			return value, fmt.Errorf("service health: %s thresholds must not be negative", p.name)
		}
		if *p.critical < *p.warning || (!p.allowWarningEqualsCritical && *p.critical == *p.warning) {
			return value, fmt.Errorf("service health: %s critical threshold (%g) must be above the warning threshold (%g)", p.name, *p.critical, *p.warning)
		}
	}
	return value, nil
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
