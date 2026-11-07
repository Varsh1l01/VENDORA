// Command api-gateway is the public REST entry point of VENDORA.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-redis/redis_rate/v10"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	cartv1 "github.com/Varsh1l01/VENDORA/gen/vendora/cart/v1"
	inventoryv1 "github.com/Varsh1l01/VENDORA/gen/vendora/inventory/v1"
	orderv1 "github.com/Varsh1l01/VENDORA/gen/vendora/order/v1"
	paymentv1 "github.com/Varsh1l01/VENDORA/gen/vendora/payment/v1"
	productv1 "github.com/Varsh1l01/VENDORA/gen/vendora/product/v1"
	userv1 "github.com/Varsh1l01/VENDORA/gen/vendora/user/v1"
	"github.com/Varsh1l01/VENDORA/pkg/admin"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/config"
	"github.com/Varsh1l01/VENDORA/pkg/grpcx"
	"github.com/Varsh1l01/VENDORA/pkg/lifecycle"
	"github.com/Varsh1l01/VENDORA/pkg/logx"
	"github.com/Varsh1l01/VENDORA/services/api-gateway/server"
)

const name = "api-gateway"

func main() {
	l := config.New()
	common := l.LoadCommon(name)
	lifecycle.HandleHealthcheckFlag(common.AdminAddr)

	httpAddr := l.String("HTTP_ADDR", ":8080")
	addrs := map[string]string{
		"user":      l.Required("USER_SERVICE_ADDR"),
		"product":   l.Required("PRODUCT_SERVICE_ADDR"),
		"cart":      l.Required("CART_SERVICE_ADDR"),
		"order":     l.Required("ORDER_SERVICE_ADDR"),
		"payment":   l.Required("PAYMENT_SERVICE_ADDR"),
		"inventory": l.Required("INVENTORY_SERVICE_ADDR"),
	}
	cfg := server.Config{
		CORSOrigins:       l.List("CORS_ALLOWED_ORIGINS", nil),
		TrustProxyHeaders: l.Bool("TRUST_PROXY_HEADERS", false),
		RequestTimeout:    l.Duration("REQUEST_TIMEOUT", 10*time.Second),
		AuthPerMinute:     l.Int("RATE_LIMIT_AUTH_PER_MIN", 20),
		WritePerMinute:    l.Int("RATE_LIMIT_WRITE_PER_MIN", 120),
		ReadPerMinute:     l.Int("RATE_LIMIT_READ_PER_MIN", 600),
	}
	redisAddr := l.String("REDIS_ADDR", "")
	redisPassword := l.String("REDIS_PASSWORD", "")
	logx.New(name, common.LogLevel)
	if err := l.Err(); err != nil {
		lifecycle.Fatal("invalid configuration", err)
	}

	ctx, stop := lifecycle.SignalContext()
	defer stop()

	conns := map[string]*grpc.ClientConn{}
	for svc, addr := range addrs {
		// No service identity: the gateway only forwards end-user tokens, so it
		// can never exceed the caller's own permissions.
		c, err := grpcx.Dial(addr, grpcx.ClientOptions{Timeout: 5 * time.Second})
		if err != nil {
			lifecycle.Fatal("dial "+svc, err)
		}
		defer c.Close()
		conns[svc] = c
	}
	clients := server.Clients{
		User:      userv1.NewUserServiceClient(conns["user"]),
		Product:   productv1.NewProductServiceClient(conns["product"]),
		Cart:      cartv1.NewCartServiceClient(conns["cart"]),
		Order:     orderv1.NewOrderServiceClient(conns["order"]),
		Payment:   paymentv1.NewPaymentServiceClient(conns["payment"]),
		Inventory: inventoryv1.NewInventoryServiceClient(conns["inventory"]),
	}

	checks := make([]admin.Check, 0, len(conns)+1)
	for svc, c := range conns {
		hc := healthpb.NewHealthClient(c)
		checks = append(checks, admin.Check{Name: svc, Fn: func(ctx context.Context) error {
			resp, err := hc.Check(ctx, &healthpb.HealthCheckRequest{})
			if err != nil {
				return err
			}
			if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
				return fmt.Errorf("status %s", resp.GetStatus())
			}
			return nil
		}})
	}

	var limiter *redis_rate.Limiter
	if redisAddr != "" {
		rdb := redis.NewClient(&redis.Options{Addr: redisAddr, Password: redisPassword, DB: 2,
			DialTimeout: time.Second, ReadTimeout: 200 * time.Millisecond, WriteTimeout: 200 * time.Millisecond})
		defer rdb.Close()
		limiter = redis_rate.NewLimiter(rdb)
		checks = append(checks, admin.Check{Name: "redis", Fn: func(ctx context.Context) error { return rdb.Ping(ctx).Err() }})
	} else {
		slog.Warn("REDIS_ADDR not set: rate limiting disabled")
	}

	tokens := auth.NewTokens(common.JWTSecret, common.JWTIssuer, common.JWTAudience)
	srv := &http.Server{
		Addr:              httpAddr,
		Handler:           server.New(cfg, tokens, clients, limiter).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	adm := admin.New(common.AdminAddr, checks...)

	err := lifecycle.Run(ctx, adm.SetDraining,
		lifecycle.Component{Name: "http", Run: func(ctx context.Context) error {
			errc := make(chan error, 1)
			go func() { errc <- srv.ListenAndServe() }()
			slog.Info("http server listening", "addr", httpAddr)
			select {
			case err := <-errc:
				if errors.Is(err, http.ErrServerClosed) {
					return nil
				}
				return err
			case <-ctx.Done():
			}
			shCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), common.ShutdownTTL)
			defer cancel()
			return srv.Shutdown(shCtx)
		}},
		lifecycle.Component{Name: "admin", Run: adm.Run},
	)
	if err != nil {
		lifecycle.Fatal("gateway stopped with error", err)
	}
}
