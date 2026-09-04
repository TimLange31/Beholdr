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

const maxPrometheusResponseBytes = 16 << 20

var (
	ErrPrometheusNotConfigured = errors.New("prometheus is not configured")
	ErrPrometheusQueryFailed   = errors.New("prometheus query failed")
)

type Sample struct {
	Timestamp float64 `json:"t"`
	Value     float64 `json:"value"`
}

type TimeSeries struct {
	Metric map[string]string `json:"metric"`
	Values []Sample          `json:"values"`
}

// QueryPrometheusRange evaluates one bounded matrix query. Callers own the
// query templates; Beholdr never accepts arbitrary PromQL directly from an HTTP
// request. This method only handles transport and the stable Prometheus API
// envelope.
func (m *Monitor) QueryPrometheusRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]TimeSeries, error) {
	p := m.prometheusQuery
	if p.endpoint == "" {
		return nil, ErrPrometheusNotConfigured
	}
	if p.setupErr != "" || p.client == nil {
		return nil, ErrPrometheusQueryFailed
	}
	if strings.TrimSpace(query) == "" || len(query) > 32<<10 || step <= 0 || !end.After(start) {
		return nil, ErrPrometheusQueryFailed
	}
	if end.Sub(start) > 31*24*time.Hour || end.Sub(start)/step > 2_000 {
		return nil, ErrPrometheusQueryFailed
	}

	form := url.Values{
		"query": {query},
		"start": {strconv.FormatFloat(float64(start.UnixNano())/1e9, 'f', 3, 64)},
		"end":   {strconv.FormatFloat(float64(end.UnixNano())/1e9, 'f', 3, 64)},
		"step":  {strconv.FormatFloat(step.Seconds(), 'f', -1, 64)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, ErrPrometheusQueryFailed
	}
	if (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.Host == "" || req.URL.User != nil {
		return nil, ErrPrometheusQueryFailed
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if p.authHeader != "" {
		req.Header.Set("Authorization", p.authHeader)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		m.log.Warn("Prometheus range query failed", "reason", classify(err), "err", err)
		return nil, ErrPrometheusQueryFailed
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		m.log.Warn("Prometheus range query returned an error", "status", resp.StatusCode)
		return nil, ErrPrometheusQueryFailed
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPrometheusResponseBytes+1))
	if err != nil || len(body) > maxPrometheusResponseBytes {
		return nil, ErrPrometheusQueryFailed
	}
	var envelope prometheusEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Status != "success" || envelope.Data.ResultType != "matrix" {
		return nil, ErrPrometheusQueryFailed
	}

	result := make([]TimeSeries, 0, len(envelope.Data.Result))
	for _, raw := range envelope.Data.Result {
		series := TimeSeries{Metric: raw.Metric, Values: make([]Sample, 0, len(raw.Values))}
		for _, value := range raw.Values {
			sample, ok := value.sample()
			if ok {
				series.Values = append(series.Values, sample)
			}
		}
		result = append(result, series)
	}
	return result, nil
}

type prometheusEnvelope struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values []prometheusValue `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

type prometheusValue [2]json.RawMessage

func (v prometheusValue) sample() (Sample, bool) {
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
