package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-redis/redis_rate/v10"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/logx"
	"github.com/Varsh1l01/VENDORA/pkg/requestid"
)

var httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "http_server_request_duration_seconds",
	Help:    "Latency of HTTP requests handled by the gateway.",
	Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
}, []string{"method", "route", "status"})

var rateLimited = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "ratelimit_rejected_total", Help: "Requests rejected by the rate limiter.",
}, []string{"policy"})

func init() { prometheus.MustRegister(httpDuration, rateLimited) }

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// requestContext assigns a request id, client IP and request-scoped logger,
// recovers panics, and records access logs and metrics.
func (s *Server) requestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := requestid.Sanitize(r.Header.Get(requestid.Header))
		ip := s.clientIP(r)
		ctx := requestid.With(r.Context(), id)
		ctx = context.WithValue(ctx, ipKey{}, ip)
		ctx = logx.With(ctx, "request_id", id)
		w.Header().Set(requestid.Header, id)
		rec := &statusRecorder{ResponseWriter: w}
		r = r.WithContext(ctx)

		defer func() {
			if p := recover(); p != nil {
				logx.From(ctx).Error("panic", "panic", fmt.Sprint(p))
				if rec.status == 0 {
					writeProblem(rec, r, http.StatusInternalServerError, "internal error")
				}
			}
			route := chi.RouteContext(r.Context()).RoutePattern()
			if route == "" {
				route = "unmatched"
			}
			dur := time.Since(start)
			httpDuration.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Observe(dur.Seconds())
			logx.From(ctx).Info("http request",
				"method", r.Method, "route", route, "status", rec.status, "bytes", rec.bytes,
				"duration_ms", dur.Milliseconds(), "client_ip", ip)
		}()
		next.ServeHTTP(rec, r)
	})
}

type ipKey struct{}

func clientIPFrom(ctx context.Context) string { s, _ := ctx.Value(ipKey{}).(string); return s }

// clientIP uses X-Forwarded-For only when the gateway runs behind a trusted
// proxy; otherwise the header is attacker-controlled.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxyHeaders {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// cors allows only configured origins (parsed once at startup).
func (s *Server) cors(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range s.cfg.CORSOrigins {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Add("Vary", "Origin")
			if allowed[origin] {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Request-ID")
				h.Set("Access-Control-Expose-Headers", "X-Request-ID, RateLimit-Remaining, Retry-After")
				h.Set("Access-Control-Max-Age", "600")
			}
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type tokenKey struct{}

// authenticate verifies the bearer token when present and stores the
// principal and raw token. requireAuth enforces presence.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if h == "" {
			next.ServeHTTP(w, r)
			return
		}
		tok, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || tok == "" {
			writeProblem(w, r, http.StatusUnauthorized, "Authorization header must be 'Bearer <token>'")
			return
		}
		p, err := s.tokens.Verify(tok)
		if err != nil || p.IsService() {
			// Service tokens are for internal calls only; never accepted at the edge.
			writeProblem(w, r, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		ctx := auth.WithPrincipal(r.Context(), p)
		ctx = context.WithValue(ctx, tokenKey{}, tok)
		ctx = logx.With(ctx, "user_id", p.Subject)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.FromContext(r.Context()); !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="vendora"`)
			writeProblem(w, r, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := auth.FromContext(r.Context()); !ok || !p.IsAdmin() {
			writeProblem(w, r, http.StatusForbidden, "admin role required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RateLimit describes a limiter policy.
type RateLimit struct {
	Name       string
	Limit      redis_rate.Limit
	FailClosed bool // reject when Redis is unavailable (sensitive routes)
}

// rateLimit applies a GCRA limit keyed by user id (if authenticated) or IP.
func (s *Server) rateLimit(p RateLimit) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if s.limiter == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := "ip:" + clientIPFrom(r.Context())
			if pr, ok := auth.FromContext(r.Context()); ok {
				key = "user:" + pr.Subject
			}
			ctx, cancel := context.WithTimeout(r.Context(), 200*time.Millisecond)
			res, err := s.limiter.Allow(ctx, "rl:"+p.Name+":"+key, p.Limit)
			cancel()
			if err != nil {
				logx.From(r.Context()).Warn("rate limiter unavailable", "policy", p.Name, "error", err)
				if p.FailClosed {
					writeProblem(w, r, http.StatusServiceUnavailable, "temporarily unavailable, retry shortly")
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("RateLimit-Remaining", strconv.Itoa(res.Remaining))
			if res.Allowed == 0 {
				rateLimited.WithLabelValues(p.Name).Inc()
				w.Header().Set("Retry-After", strconv.Itoa(int(res.RetryAfter.Seconds())+1))
				writeProblem(w, r, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
