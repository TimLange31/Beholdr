// Package config holds runtime configuration, all overridable via env vars.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr          string        // listen address
	PollInterval  time.Duration // how often to poll the cluster
	HistorySize   int           // samples retained per series
	Namespaces    []string      // restrict monitoring; empty = all
	KubeMode      string        // "auto" | "in-cluster" | "kubeconfig"
	Kubeconfig    string        // path when KubeMode != in-cluster
	AllowCORS     bool          // enable permissive CORS (handy for local dev)
	RequestTimout time.Duration // per-call timeout to the API server
}

func Load() Config {
	return Config{
		Addr:          env("BEHOLDR_ADDR", ":8000"),
		PollInterval:  time.Duration(envInt("BEHOLDR_POLL_INTERVAL", 15)) * time.Second,
		HistorySize:   envInt("BEHOLDR_HISTORY_SIZE", 240),
		Namespaces:    splitCSV(env("BEHOLDR_NAMESPACES", "")),
		KubeMode:      env("BEHOLDR_KUBE_MODE", "auto"),
		Kubeconfig:    env("KUBECONFIG", ""),
		AllowCORS:     env("BEHOLDR_CORS", "true") == "true",
		RequestTimout: time.Duration(envInt("BEHOLDR_REQUEST_TIMEOUT", 10)) * time.Second,
	}
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, ok := os.LookupEnv(k); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
