package integrations

import (
	"context"
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
	if _, err := m.QueryPrometheusRange(context.Background(), strings.Repeat("x", 33<<10), now.Add(-time.Hour), now, time.Minute); err != ErrPrometheusQueryFailed {
		t.Fatalf("want bounded-query failure, got %v", err)
	}
	if _, err := m.QueryPrometheusRange(context.Background(), "up", now.Add(-30*24*time.Hour), now, time.Second); err != ErrPrometheusQueryFailed {
		t.Fatalf("want point-count failure, got %v", err)
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
