// Package admin runs each service's internal HTTP port: liveness, readiness,
// Prometheus metrics and pprof. It must never be exposed publicly.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/pprof"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Check is a named readiness probe.
type Check struct {
	Name string
	Fn   func(context.Context) error
}

// Server is the admin HTTP server.
type Server struct {
	srv      *http.Server
	checks   []Check
	draining atomic.Bool
}

// New returns an admin server listening on addr.
func New(addr string, checks ...Check) *Server {
	s := &Server{checks: checks}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", s.ready)
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	s.srv = &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return s
}

// SetDraining makes /readyz fail so load balancers stop routing traffic
// before shutdown.
func (s *Server) SetDraining() { s.draining.Store(true) }

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if s.draining.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "draining"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	results := make(map[string]string, len(s.checks))
	var mu sync.Mutex
	var wg sync.WaitGroup
	ok := true
	for _, c := range s.checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := c.Fn(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				ok = false
				results[c.Name] = err.Error()
			} else {
				results[c.Name] = "ok"
			}
		}()
	}
	wg.Wait()
	code := http.StatusOK
	if !ok {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"status": map[bool]string{true: "ready", false: "not_ready"}[ok], "checks": results})
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	errc := make(chan error, 1)
	go func() { errc <- s.srv.ListenAndServe() }()
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("admin server: %w", err)
	case <-ctx.Done():
		shCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return s.srv.Shutdown(shCtx)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Probe performs an HTTP GET against a local /readyz (or any URL) and returns
// an error unless it answers 200. Used by the "-healthcheck" flag so images
// without a shell can run container health checks.
func Probe(url string) error {
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: %s", resp.Status)
	}
	return nil
}
