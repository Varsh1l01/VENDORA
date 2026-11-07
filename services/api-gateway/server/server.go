// Package server is the VENDORA REST gateway: it authenticates requests,
// applies edge policies (rate limits, body limits, timeouts, CORS) and
// translates REST/JSON to gRPC calls.
package server

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-redis/redis_rate/v10"

	cartv1 "github.com/Varsh1l01/VENDORA/gen/vendora/cart/v1"
	inventoryv1 "github.com/Varsh1l01/VENDORA/gen/vendora/inventory/v1"
	orderv1 "github.com/Varsh1l01/VENDORA/gen/vendora/order/v1"
	paymentv1 "github.com/Varsh1l01/VENDORA/gen/vendora/payment/v1"
	productv1 "github.com/Varsh1l01/VENDORA/gen/vendora/product/v1"
	userv1 "github.com/Varsh1l01/VENDORA/gen/vendora/user/v1"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/grpcx"
	"github.com/Varsh1l01/VENDORA/services/api-gateway/openapi"
)

// Clients are the downstream gRPC clients.
type Clients struct {
	User      userv1.UserServiceClient
	Product   productv1.ProductServiceClient
	Cart      cartv1.CartServiceClient
	Order     orderv1.OrderServiceClient
	Payment   paymentv1.PaymentServiceClient
	Inventory inventoryv1.InventoryServiceClient
}

// Config holds gateway settings.
type Config struct {
	CORSOrigins       []string
	TrustProxyHeaders bool
	RequestTimeout    time.Duration
	AuthPerMinute     int
	WritePerMinute    int
	ReadPerMinute     int
}

// Server is the HTTP gateway.
type Server struct {
	cfg     Config
	tokens  *auth.Tokens
	c       Clients
	limiter *redis_rate.Limiter // nil disables rate limiting
}

// New returns a Server.
func New(cfg Config, tokens *auth.Tokens, c Clients, limiter *redis_rate.Limiter) *Server {
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 10 * time.Second
	}
	return &Server{cfg: cfg, tokens: tokens, c: c, limiter: limiter}
}

// Handler returns the HTTP handler with all routes.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(s.requestContext, securityHeaders, s.cors, middleware.CleanPath)

	authLimit := s.rateLimit(RateLimit{Name: "auth", Limit: redis_rate.PerMinute(s.cfg.AuthPerMinute), FailClosed: true})
	readLimit := s.rateLimit(RateLimit{Name: "read", Limit: redis_rate.PerMinute(s.cfg.ReadPerMinute)})
	writeLimit := s.rateLimit(RateLimit{Name: "write", Limit: redis_rate.PerMinute(s.cfg.WritePerMinute)})

	r.Get("/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(openapi.Spec)
	})
	r.Get("/docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(openapi.SwaggerUI)
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(middleware.Timeout(s.cfg.RequestTimeout), s.authenticate)

		r.Group(func(r chi.Router) {
			r.Use(authLimit)
			r.Post("/auth/register", s.register)
			r.Post("/auth/login", s.login)
			r.Post("/auth/refresh", s.refresh)
			r.Post("/auth/logout", s.logout)
		})

		r.Group(func(r chi.Router) {
			r.Use(readLimit)
			r.Get("/products", s.listProducts)
			r.Get("/products/{id}", s.getProduct)
			r.Get("/products/{id}/stock", s.getStock)
		})

		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.With(readLimit).Get("/me", s.me)

			r.With(readLimit).Get("/cart", s.getCart)
			r.With(writeLimit).Post("/cart/items", s.addCartItem)
			r.With(writeLimit).Put("/cart/items/{productId}", s.setCartItem)
			r.With(writeLimit).Delete("/cart/items/{productId}", s.removeCartItem)
			r.With(writeLimit).Delete("/cart", s.clearCart)

			r.With(writeLimit).Post("/orders", s.createOrder)
			r.With(readLimit).Get("/orders", s.listOrders)
			r.With(readLimit).Get("/orders/{id}", s.getOrder)
			r.With(writeLimit).Post("/orders/{id}/cancel", s.cancelOrder)
			r.With(writeLimit).Post("/orders/{id}/payment", s.payOrder)
			r.With(readLimit).Get("/orders/{id}/payment", s.getPayment)

			r.Route("/admin", func(r chi.Router) {
				r.Use(requireAdmin, writeLimit)
				r.Post("/products", s.adminCreateProduct)
				r.Patch("/products/{id}", s.adminUpdateProduct)
				r.Delete("/products/{id}", s.adminDeleteProduct)
				r.Put("/inventory/{productId}", s.adminSetStock)
				r.Post("/inventory/{productId}/adjust", s.adminAdjustStock)
				r.Patch("/orders/{id}/status", s.adminUpdateOrderStatus)
			})
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, http.StatusNotFound, "no such endpoint")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, http.StatusMethodNotAllowed, "method not allowed")
	})
	return r
}

// call returns a context for downstream calls that forwards the end user's
// token, so each service enforces authorization itself.
func call(r *http.Request) context.Context {
	ctx := r.Context()
	if tok, ok := ctx.Value(tokenKey{}).(string); ok {
		return grpcx.WithBearer(ctx, tok)
	}
	return ctx
}
