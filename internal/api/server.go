// Package api serves the JSON API and the embedded UI from one http.Server.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/delangetimm/beholdr/internal/collect"
	"github.com/delangetimm/beholdr/internal/integrations"
	"github.com/delangetimm/beholdr/internal/servicehealth"
	"github.com/delangetimm/beholdr/internal/webui"
)

type Server struct {
	col           *collect.Collector
	integrations  *integrations.Monitor
	serviceHealth *servicehealth.Service
	log           *slog.Logger
	corsOrigins   []string
}

// NewServer builds the API server. corsOrigins is an explicit allowlist of
// origins permitted to make cross-origin requests; nil/empty disables CORS
// entirely (the default). "*" may be included to allow any origin, which is
// only appropriate for local development.
// serviceHealth may be nil, in which case the per-service metrics endpoint
// reports that Prometheus is not configured. It is built by the caller so an
// unusable metric profile fails at startup rather than per request.
func NewServer(col *collect.Collector, integrations *integrations.Monitor, serviceHealth *servicehealth.Service, corsOrigins []string, log *slog.Logger) *Server {
	return &Server{col: col, integrations: integrations, serviceHealth: serviceHealth, log: log, corsOrigins: corsOrigins}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Liveness: the process can accept and handle HTTP requests. Must not
	// depend on collector state, or a cluster/API-server outage would cause
	// Kubernetes to kill and restart a perfectly healthy process.
	mux.HandleFunc("GET /live", s.live)
	// Readiness: there has been a successful collection recently. Kubernetes
	// should stop sending traffic here (and the LB should drop the pod) once
	// the cached data goes stale, rather than silently serving old state.
	mux.HandleFunc("GET /ready", s.ready)
	// Rich status for the UI: always 200 so the frontend can poll it and
	// render last-success/last-error without treating "not ready yet" as a
	// network failure.
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/integrations", s.integrationStatus)
	mux.HandleFunc("GET /api/cluster", s.cluster)
	mux.HandleFunc("GET /api/nodes", s.nodes)
	mux.HandleFunc("GET /api/nodes/{name}", s.nodeDetail)
	mux.HandleFunc("GET /api/microservices", s.microservices)
	mux.HandleFunc("GET /api/microservices/{ns}/{name}/metrics", s.microserviceMetrics)
	mux.HandleFunc("GET /api/microservices/{ns}/{name}", s.microserviceDetail)
	mux.HandleFunc("GET /api/pods", s.pods)
	mux.Handle("/", s.spa())
	return s.middleware(mux)
}

func (s *Server) integrationStatus(w http.ResponseWriter, r *http.Request) {
	if s.integrations == nil {
		writeJSON(w, http.StatusOK, integrations.Snapshot{Providers: []integrations.ProviderStatus{}})
		return
	}
	writeJSON(w, http.StatusOK, s.integrations.Snapshot())
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && s.corsAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) corsAllowed(origin string) bool {
	for _, o := range s.corsOrigins {
		if o == "*" || o == origin {
			return true
		}
	}
	return false
}

// --- health / readiness ------------------------------------------------------

func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	hs := s.col.Health()
	status := http.StatusOK
	if !hs.Ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, hs)
}

// health is a rich, always-200 status view for the UI: it reports the same
// readiness signal as /ready plus metrics availability, without the 503 that
// would make a naive fetch() throw.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	hs := s.col.Health()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"ready":             hs.Ready,
		"last_success":      hs.LastSuccess,
		"last_error":        hs.LastError,
		"last_error_at":     hs.LastErrorAt,
		"metrics_available": s.col.Snapshot().MetricsAvailable,
	})
}

func (s *Server) cluster(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.requireData(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"updated_at":        snap.UpdatedAt,
		"metrics_available": snap.MetricsAvailable,
		"cluster":           snap.Cluster,
		"history":           s.col.History.Get("cluster"),
	})
}

func (s *Server) nodes(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.requireData(w)
	if !ok {
		return
	}
	// strip per-node pod lists from the collection view to keep it light
	nodes := make([]collect.Node, len(snap.Nodes))
	copy(nodes, snap.Nodes)
	for i := range nodes {
		nodes[i].Pods = nil
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated_at": snap.UpdatedAt, "nodes": nodes})
}

func (s *Server) nodeDetail(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.requireData(w)
	if !ok {
		return
	}
	name := r.PathValue("name")
	for _, n := range snap.Nodes {
		if n.Name == name {
			writeJSON(w, http.StatusOK, map[string]any{
				"node": n, "history": s.col.History.Get("node::" + name),
			})
			return
		}
	}
	http.Error(w, "node not found", http.StatusNotFound)
}

func (s *Server) microservices(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.requireData(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"updated_at": snap.UpdatedAt, "microservices": snap.Microservices,
	})
}

func (s *Server) microserviceDetail(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.requireData(w)
	if !ok {
		return
	}
	ns, name := r.PathValue("ns"), r.PathValue("name")
	key := ns + "/" + name
	var ms *collect.Microservice
	for i := range snap.Microservices {
		if snap.Microservices[i].Key == key {
			ms = &snap.Microservices[i]
			break
		}
	}
	if ms == nil {
		http.Error(w, "microservice not found", http.StatusNotFound)
		return
	}
	pods := []collect.Pod{}
	for _, p := range snap.Pods {
		if p.Namespace == ns && p.Workload == name {
			pods = append(pods, p)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"microservice": ms, "pods": pods, "history": s.col.History.Get("ms::" + key),
	})
}

func (s *Server) microserviceMetrics(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.requireData(w)
	if !ok {
		return
	}
	ns, name := r.PathValue("ns"), r.PathValue("name")
	var workload *collect.Microservice
	for i := range snap.Microservices {
		if snap.Microservices[i].Namespace == ns && snap.Microservices[i].Name == name {
			workload = &snap.Microservices[i]
			break
		}
	}
	// Resolving against the snapshot before querying is what keeps the path
	// parameters out of the PromQL templates: only the names of Kubernetes
	// objects the collector actually saw can reach them.
	if workload == nil {
		http.Error(w, "microservice not found", http.StatusNotFound)
		return
	}
	window, ok := servicehealth.ParseWindow(r.URL.Query().Get("range"))
	if !ok {
		http.Error(w, "range must be one of: 1h, 6h, 24h, 7d, 21d", http.StatusBadRequest)
		return
	}
	if s.serviceHealth == nil {
		http.Error(w, "Prometheus is not configured", http.StatusServiceUnavailable)
		return
	}

	// The live pod names let the score pin itself to pods this workload
	// actually owns; the charts still use the name-shape selector so pods
	// replaced by earlier rollouts stay in the history.
	podNames := make([]string, 0, 8)
	for _, p := range snap.Pods {
		if p.Namespace == ns && p.Workload == name {
			podNames = append(podNames, p.Name)
		}
	}

	report, err := s.serviceHealth.Query(r.Context(), *workload, podNames, window, time.Now())
	switch {
	case err == nil:
	case errors.Is(err, integrations.ErrPrometheusNotConfigured):
		http.Error(w, "Prometheus is not configured", http.StatusServiceUnavailable)
		return
	case errors.Is(err, context.Canceled):
		// The client went away mid-flight; there is nobody to answer.
		return
	default:
		s.log.Warn("service health query failed", "namespace", ns, "service", name, "err", err)
		http.Error(w, "service metrics unavailable", http.StatusBadGateway)
		return
	}
	// Reports are cached server-side for a short window; letting a browser or
	// proxy hold one for longer would show an operator a stale severity badge.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) pods(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.requireData(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated_at": snap.UpdatedAt, "pods": snap.Pods})
}

// --- static (SPA) -----------------------------------------------------------

func (s *Server) spa() http.Handler {
	sub, err := fs.Sub(webui.Assets, "dist")
	if err != nil {
		s.log.Error("embedded UI sub FS", "err", err)
		sub = webui.Assets
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if f, err := sub.Open(p); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: unknown path -> index.html for client-side routing
		r2 := new(http.Request)
		*r2 = *r
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}

// --- helpers ----------------------------------------------------------------

// requireData gates the data endpoints on "has a collection ever succeeded",
// so the API can keep serving the last known-good snapshot even while /ready
// reports the data is going stale. Deliberately more lenient than /ready.
func (s *Server) requireData(w http.ResponseWriter) (collect.Snapshot, bool) {
	snap := s.col.Snapshot()
	if !snap.Ready {
		http.Error(w, "collector warming up", http.StatusServiceUnavailable)
		return snap, false
	}
	return snap, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
