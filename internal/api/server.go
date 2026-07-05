// Package api serves the JSON API and the embedded UI from one http.Server.
package api

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/delangetimm/beholdr/internal/collect"
	"github.com/delangetimm/beholdr/internal/webui"
)

type Server struct {
	col      *collect.Collector
	log      *slog.Logger
	cors     bool
}

func NewServer(col *collect.Collector, cors bool, log *slog.Logger) *Server {
	return &Server{col: col, log: log, cors: cors}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/cluster", s.cluster)
	mux.HandleFunc("GET /api/nodes", s.nodes)
	mux.HandleFunc("GET /api/nodes/{name}", s.nodeDetail)
	mux.HandleFunc("GET /api/microservices", s.microservices)
	mux.HandleFunc("GET /api/microservices/{ns}/{name}", s.microserviceDetail)
	mux.HandleFunc("GET /api/pods", s.pods)
	mux.Handle("/", s.spa())
	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cors {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// --- API handlers -----------------------------------------------------------

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ready": s.col.Snapshot().Ready})
}

func (s *Server) cluster(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.ready(w)
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
	snap, ok := s.ready(w)
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
	snap, ok := s.ready(w)
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
	snap, ok := s.ready(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"updated_at": snap.UpdatedAt, "microservices": snap.Microservices,
	})
}

func (s *Server) microserviceDetail(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.ready(w)
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

func (s *Server) pods(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.ready(w)
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

func (s *Server) ready(w http.ResponseWriter) (collect.Snapshot, bool) {
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
