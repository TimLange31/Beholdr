package integrations

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestQueryPrometheusRange(t *testing.T) {
	var gotQuery, gotStart, gotEnd, gotStep string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prometheus/api/v1/query_range" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("want POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("unexpected authorization header %q", r.Header.Get("Authorization"))
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		gotQuery, gotStart, gotEnd, gotStep = r.FormValue("query"), r.FormValue("start"), r.FormValue("end"), r.FormValue("step")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"service":"video"},"values":[[1000,"1.5"],[1060,"NaN"],[1120,"2.5"]]}]}}`))
	}))
	defer server.Close()

	m := New(Config{
		PrometheusURL:         server.URL + "/prometheus",
		PrometheusBearerToken: "secret",
		Timeout:               time.Second,
	}, discardLogger())
	start := time.Unix(1000, 0)
	end := time.Unix(1120, 0)
	series, err := m.QueryPrometheusRange(context.Background(), "up", start, end, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "up" || gotStart != "1000.000" || gotEnd != "1120.000" || gotStep != "60" {
		t.Fatalf("unexpected form: query=%q start=%q end=%q step=%q", gotQuery, gotStart, gotEnd, gotStep)
	}
	if len(series) != 1 || series[0].Metric["service"] != "video" || len(series[0].Values) != 2 {
		t.Fatalf("unexpected parsed series: %+v", series)
	}
	if series[0].Values[1].Value != 2.5 {
		t.Fatalf("unexpected last value: %+v", series[0].Values[1])
	}
}

func TestQueryPrometheusRangeRejectsUnconfiguredAndUnboundedQueries(t *testing.T) {
	m := New(Config{}, discardLogger())
	now := time.Now()
	if _, err := m.QueryPrometheusRange(context.Background(), "up", now.Add(-time.Hour), now, time.Minute); err != ErrPrometheusNotConfigured {
		t.Fatalf("want ErrPrometheusNotConfigured, got %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("invalid query must not reach Prometheus")
	}))
	defer server.Close()
	m = New(Config{PrometheusURL: server.URL}, discardLogger())
	if _, err := m.QueryPrometheusRange(context.Background(), strings.Repeat("x", 33<<10), now.Add(-time.Hour), now, time.Minute); !errors.Is(err, ErrPrometheusQueryRejected) {
		t.Fatalf("want bounded-query rejection, got %v", err)
	}
	if _, err := m.QueryPrometheusRange(context.Background(), "up", now.Add(-30*24*time.Hour), now, time.Second); !errors.Is(err, ErrPrometheusQueryRejected) {
		t.Fatalf("want point-count rejection, got %v", err)
	}
	if _, err := m.QueryPrometheusRange(context.Background(), "up", now, now.Add(-time.Hour), time.Minute); !errors.Is(err, ErrPrometheusQueryRejected) {
		t.Fatalf("want inverted-range rejection, got %v", err)
	}
}

func TestQueryPrometheusRangeDoesNotExposeUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "sensitive upstream parser detail", http.StatusBadRequest)
	}))
	defer server.Close()

	m := New(Config{PrometheusURL: server.URL}, discardLogger())
	now := time.Now()
	_, err := m.QueryPrometheusRange(context.Background(), "up", now.Add(-time.Hour), now, time.Minute)
	if err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("want sanitized query failure, got %v", err)
	}
}

func TestQueryPrometheusInstant(t *testing.T) {
	var gotPath, gotTime string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotPath, gotTime = r.URL.Path, r.FormValue("time")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1000,"4.25"]}]}}`))
	}))
	defer server.Close()

	m := New(Config{PrometheusURL: server.URL, Timeout: time.Second}, discardLogger())
	samples, err := m.QueryPrometheusInstant(context.Background(), "up", time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/query" || gotTime != "1000.000" {
		t.Fatalf("unexpected request: path=%q time=%q", gotPath, gotTime)
	}
	if len(samples) != 1 || samples[0].Sample.Value != 4.25 {
		t.Fatalf("unexpected samples: %+v", samples)
	}
}

func TestInstantAndRangeUseTheirOwnPaths(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer server.Close()

	m := New(Config{PrometheusURL: server.URL + "/prom", Timeout: time.Second}, discardLogger())
	now := time.Now()
	_, _ = m.QueryPrometheusRange(context.Background(), "up", now.Add(-time.Hour), now, time.Minute)
	_, _ = m.QueryPrometheusInstant(context.Background(), "up", now)
	want := []string{"/prom/api/v1/query_range", "/prom/api/v1/query"}
	if len(paths) != 2 || paths[0] != want[0] || paths[1] != want[1] {
		t.Fatalf("want %v, got %v", want, paths)
	}
}

// Prometheus reports a small documented enum alongside its free-text error.
// Only the enum is trusted, and it is what tells an operator whether to fix
// their query, their credentials, or their Prometheus.
func TestPrometheusErrorsAreClassified(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"bad query", http.StatusBadRequest, `{"status":"error","errorType":"bad_data","error":"parse error at char 3"}`, ErrPrometheusQueryRejected},
		{"credentials", http.StatusUnauthorized, ``, ErrPrometheusUnauthorized},
		{"upstream down", http.StatusBadGateway, ``, ErrPrometheusUnavailable},
		{"query timeout", http.StatusUnprocessableEntity, `{"status":"error","errorType":"timeout","error":"query timed out"}`, ErrPrometheusTimeout},
		{"execution error", http.StatusUnprocessableEntity, `{"status":"error","errorType":"execution","error":"too many samples"}`, ErrPrometheusUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()

			m := New(Config{PrometheusURL: server.URL, Timeout: time.Second}, discardLogger())
			now := time.Now()
			_, err := m.QueryPrometheusRange(context.Background(), "up", now.Add(-time.Hour), now, time.Minute)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if err != nil && strings.Contains(err.Error(), "parse error") {
				t.Fatalf("upstream detail leaked into the error: %v", err)
			}
		})
	}
}

// The query client is selected by provider name. Taking it positionally from
// the provider slice meant that reordering that slice would start sending
// another backend's credential — Elasticsearch's API key — to Prometheus's
// query API.
func TestQueriesOnlyEverCarryThePrometheusCredential(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer server.Close()

	m := New(Config{
		PrometheusURL:         server.URL,
		PrometheusBearerToken: "prom-token",
		ElasticsearchURL:      server.URL,
		ElasticsearchAPIKey:   "elastic-key-should-never-appear",
		Timeout:               time.Second,
	}, discardLogger())
	now := time.Now()
	if _, err := m.QueryPrometheusRange(context.Background(), "up", now.Add(-time.Hour), now, time.Minute); err != nil {
		t.Fatal(err)
	}
	if seen != "Bearer prom-token" {
		t.Fatalf("unexpected credential on the query path: %q", seen)
	}
}

// A multi-week range query is normal work and must not inherit the timeout
// sized for a sub-second liveness probe.
func TestQueriesDoNotInheritTheHealthCheckTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer server.Close()

	m := New(Config{
		PrometheusURL: server.URL,
		Timeout:       20 * time.Millisecond, // health checks stay snappy
		QueryTimeout:  5 * time.Second,       // queries get room to work
	}, discardLogger())
	now := time.Now()
	if _, err := m.QueryPrometheusRange(context.Background(), "up", now.Add(-time.Hour), now, time.Minute); err != nil {
		t.Fatalf("query should outlive the health-check timeout: %v", err)
	}
}

func TestSlowQueriesReportATimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()

	m := New(Config{PrometheusURL: server.URL, QueryTimeout: 20 * time.Millisecond}, discardLogger())
	now := time.Now()
	if _, err := m.QueryPrometheusRange(context.Background(), "up", now.Add(-time.Hour), now, time.Minute); !errors.Is(err, ErrPrometheusTimeout) {
		t.Fatalf("want ErrPrometheusTimeout, got %v", err)
	}
}
