package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// maxPrometheusResponseBytes bounds what a single query may pull into memory.
// A query is capped at maxRangePoints evaluation points and Beholdr's own
// templates aggregate to a handful of series, so a legitimate response is tens
// of kilobytes. This cap exists for a broken or hostile upstream, and several
// queries run concurrently per request, so it is deliberately close to the
// realistic ceiling rather than an order of magnitude above it.
const maxPrometheusResponseBytes = 2 << 20

// maxRangePoints is the number of evaluation points a single range query may
// request. It also bounds the response size together with the cap above.
const maxRangePoints = 2_000

var (
	// ErrPrometheusNotConfigured means no Prometheus URL was supplied.
	ErrPrometheusNotConfigured = errors.New("prometheus is not configured")
	// ErrPrometheusQueryFailed is the unclassified fallback: a transport
	// failure, or a response Beholdr could not parse.
	ErrPrometheusQueryFailed = errors.New("prometheus query failed")
	// ErrPrometheusQueryRejected means Prometheus understood the request and
	// refused it — almost always a malformed query or an unparseable
	// parameter. Operator-actionable: the query template or its configured
	// metric/label names are wrong.
	ErrPrometheusQueryRejected = errors.New("prometheus rejected the query")
	// ErrPrometheusUnauthorized means the configured credential was refused.
	ErrPrometheusUnauthorized = errors.New("prometheus rejected the credentials")
	// ErrPrometheusUnavailable means Prometheus answered but could not serve
	// the query (5xx, or its own "unavailable" error type).
	ErrPrometheusUnavailable = errors.New("prometheus is unavailable")
	// ErrPrometheusTimeout means the query exceeded its deadline, on either
	// side of the connection.
	ErrPrometheusTimeout = errors.New("prometheus query timed out")
)

type Sample struct {
	Timestamp float64 `json:"t"`
	Value     float64 `json:"value"`
}

type TimeSeries struct {
	Metric map[string]string `json:"metric"`
	Values []Sample          `json:"values"`
}

// InstantSample is one element of a vector result.
type InstantSample struct {
	Metric map[string]string `json:"metric"`
	Sample Sample            `json:"sample"`
}

// QueryPrometheusRange evaluates one bounded matrix query. Callers own the
// query templates; Beholdr never accepts arbitrary PromQL directly from an HTTP
// request. This method only handles transport and the stable Prometheus API
// envelope.
func (m *Monitor) QueryPrometheusRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]TimeSeries, error) {
	if step <= 0 || !end.After(start) {
		return nil, ErrPrometheusQueryRejected
	}
	if end.Sub(start) > 31*24*time.Hour || end.Sub(start)/step > maxRangePoints {
		return nil, ErrPrometheusQueryRejected
	}
	form := url.Values{
		"query": {query},
		"start": {formatTime(start)},
		"end":   {formatTime(end)},
		"step":  {strconv.FormatFloat(step.Seconds(), 'f', -1, 64)},
	}
	envelope, err := m.postPrometheus(ctx, "range", queryRangePath, query, form)
	if err != nil {
		return nil, err
	}
	if envelope.Data.ResultType != "matrix" {
		return nil, ErrPrometheusQueryFailed
	}
	result := make([]TimeSeries, 0, len(envelope.Data.Result))
	for _, raw := range envelope.Data.Result {
		series := TimeSeries{Metric: raw.Metric, Values: make([]Sample, 0, len(raw.Values))}
		for _, value := range raw.Values {
			if sample, ok := value.sample(); ok {
				series.Values = append(series.Values, sample)
			}
		}
		result = append(result, series)
	}
	return result, nil
}

// QueryPrometheusInstant evaluates one vector query at a single instant. It is
// what the health signals are scored from: a range query's last point is only
// as fresh as that range's step, so scoring from it would make a service's
// severity depend on which chart window the operator happened to select.
func (m *Monitor) QueryPrometheusInstant(ctx context.Context, query string, at time.Time) ([]InstantSample, error) {
	form := url.Values{"query": {query}, "time": {formatTime(at)}}
	envelope, err := m.postPrometheus(ctx, "instant", queryPath, query, form)
	if err != nil {
		return nil, err
	}
	if envelope.Data.ResultType != "vector" {
		return nil, ErrPrometheusQueryFailed
	}
	result := make([]InstantSample, 0, len(envelope.Data.Result))
	for _, raw := range envelope.Data.Result {
		sample, ok := raw.Value.sample()
		if !ok {
			continue
		}
		result = append(result, InstantSample{Metric: raw.Metric, Sample: sample})
	}
	return result, nil
}

const (
	queryPath      = "/api/v1/query"
	queryRangePath = "/api/v1/query_range"
)

func (m *Monitor) postPrometheus(ctx context.Context, kind, path, query string, form url.Values) (prometheusEnvelope, error) {
	var zero prometheusEnvelope
	p := m.prometheusQuery
	if p.baseURL == "" {
		return zero, ErrPrometheusNotConfigured
	}
	if p.setupErr != "" || p.client == nil {
		return zero, ErrPrometheusQueryFailed
	}
	if strings.TrimSpace(query) == "" || len(query) > 32<<10 {
		return zero, ErrPrometheusQueryRejected
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(p.baseURL, path), strings.NewReader(form.Encode()))
	if err != nil {
		return zero, ErrPrometheusQueryFailed
	}
	if (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.Host == "" || req.URL.User != nil {
		return zero, ErrPrometheusQueryFailed
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if p.authHeader != "" {
		req.Header.Set("Authorization", p.authHeader)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		reason := classify(err)
		m.log.Warn("Prometheus query failed", "kind", kind, "reason", reason, "err", err)
		if errors.Is(err, context.DeadlineExceeded) || reason == "timed out" {
			return zero, ErrPrometheusTimeout
		}
		return zero, ErrPrometheusQueryFailed
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxPrometheusResponseBytes+1))
	if readErr != nil || len(body) > maxPrometheusResponseBytes {
		m.log.Warn("Prometheus response was unreadable or oversized", "kind", kind, "bytes", len(body))
		return zero, ErrPrometheusQueryFailed
	}

	var envelope prometheusEnvelope
	parsed := json.Unmarshal(body, &envelope) == nil

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		queryErr := statusError(resp.StatusCode)
		// Prometheus reports a small, documented errorType enum alongside its
		// error text. Only the enum is trusted; the free-text message stays in
		// the log so nothing an upstream writes can reach Beholdr's own API.
		if parsed && envelope.ErrorType != "" {
			if mapped, ok := errorTypeError(envelope.ErrorType); ok {
				queryErr = mapped
			}
		}
		m.log.Warn("Prometheus returned an error",
			"kind", kind, "status", resp.StatusCode,
			"error_type", envelope.ErrorType, "detail", envelope.Error)
		return zero, queryErr
	}
	if !parsed || envelope.Status != "success" {
		if parsed && envelope.ErrorType != "" {
			if mapped, ok := errorTypeError(envelope.ErrorType); ok {
				m.log.Warn("Prometheus reported a query error",
					"kind", kind, "error_type", envelope.ErrorType, "detail", envelope.Error)
				return zero, mapped
			}
		}
		return zero, ErrPrometheusQueryFailed
	}
	return envelope, nil
}

func statusError(status int) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ErrPrometheusUnauthorized
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return ErrPrometheusTimeout
	case status == http.StatusTooManyRequests || status >= 500:
		return ErrPrometheusUnavailable
	case status >= 400:
		return ErrPrometheusQueryRejected
	}
	return ErrPrometheusQueryFailed
}

// errorTypeError maps Prometheus's documented errorType enum onto Beholdr's
// closed error vocabulary. Unknown values are ignored rather than surfaced.
func errorTypeError(errorType string) (error, bool) {
	switch errorType {
	case "bad_data":
		return ErrPrometheusQueryRejected, true
	case "timeout", "canceled":
		return ErrPrometheusTimeout, true
	case "unavailable":
		return ErrPrometheusUnavailable, true
	case "execution", "internal":
		return ErrPrometheusUnavailable, true
	}
	return nil, false
}

func formatTime(t time.Time) string {
	return strconv.FormatFloat(float64(t.UnixNano())/1e9, 'f', 3, 64)
}

type prometheusEnvelope struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
	Data      struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values []prometheusValue `json:"values"`
			Value  prometheusValue   `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

type prometheusValue [2]json.RawMessage

func (v prometheusValue) sample() (Sample, bool) {
	if len(v[0]) == 0 || len(v[1]) == 0 {
		return Sample{}, false
	}
	var timestamp float64
	var valueText string
	if err := json.Unmarshal(v[0], &timestamp); err != nil {
		return Sample{}, false
	}
	if err := json.Unmarshal(v[1], &valueText); err != nil {
		return Sample{}, false
	}
	value, err := strconv.ParseFloat(valueText, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return Sample{}, false
	}
	return Sample{Timestamp: timestamp, Value: value}, true
}
