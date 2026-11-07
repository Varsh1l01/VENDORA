// Command product-service owns the product catalog.
package main

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	productv1 "github.com/Varsh1l01/VENDORA/gen/vendora/product/v1"
	"github.com/Varsh1l01/VENDORA/pkg/admin"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/config"
	"github.com/Varsh1l01/VENDORA/pkg/db"
	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/pkg/grpcx"
	"github.com/Varsh1l01/VENDORA/pkg/kafka"
	"github.com/Varsh1l01/VENDORA/pkg/lifecycle"
	"github.com/Varsh1l01/VENDORA/pkg/logx"
	"github.com/Varsh1l01/VENDORA/pkg/outbox"
	"github.com/Varsh1l01/VENDORA/services/product-service/handler"
	"github.com/Varsh1l01/VENDORA/services/product-service/migrations"
	"github.com/Varsh1l01/VENDORA/services/product-service/repository"
	"github.com/Varsh1l01/VENDORA/services/product-service/service"
)

const name = "product-service"

func main() {
	l := config.New()
	common := l.LoadCommon(name)
	lifecycle.HandleHealthcheckFlag(common.AdminAddr)

	grpcAddr := l.String("GRPC_ADDR", ":50052")
	poolCfg := db.LoadPoolConfig(l, "PRODUCT_DB")
	brokers := l.List("KAFKA_BROKERS", nil)
	if len(brokers) == 0 {
		l.Required("KAFKA_BROKERS")
	}
	redisAddr := l.String("REDIS_ADDR", "")
	redisPassword := l.String("REDIS_PASSWORD", "")
	cacheTTL := l.Duration("PRODUCT_CACHE_TTL", 5*time.Minute)
	logx.New(name, common.LogLevel)
	if err := l.Err(); err != nil {
		lifecycle.Fatal("invalid configuration", err)
	}

	ctx, stop := lifecycle.SignalContext()
	defer stop()
	startCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	pool, err := db.Open(startCtx, poolCfg, "products")
	if err != nil {
		lifecycle.Fatal("open database", err)
	}
	defer pool.Close()
	if err := db.Migrate(startCtx, pool, migrations.FS); err != nil {
		lifecycle.Fatal("migrate", err)
	}
	if err := kafka.EnsureTopics(startCtx, brokers, 3, 1, events.AllTopics()...); err != nil {
		lifecycle.Fatal("ensure topics", err)
	}

	checks := []admin.Check{{Name: "postgres", Fn: pool.Ping}}
	var cache *redis.Client
	if redisAddr != "" {
		cache = redis.NewClient(&redis.Options{Addr: redisAddr, Password: redisPassword, DB: 1,
			DialTimeout: 2 * time.Second, ReadTimeout: 500 * time.Millisecond, WriteTimeout: 500 * time.Millisecond})
		defer cache.Close()
		checks = append(checks, admin.Check{Name: "redis", Fn: func(ctx context.Context) error { return cache.Ping(ctx).Err() }})
	}

	tokens := auth.NewTokens(common.JWTSecret, common.JWTIssuer, common.JWTAudience)
	svc := service.New(repository.New(pool), cache, cacheTTL)
	srv := grpcx.NewServer(grpcx.ServerOptions{Tokens: tokens, Policy: handler.Policy})
	productv1.RegisterProductServiceServer(srv.Server, handler.New(svc))

	writer := kafka.NewWriter(brokers)
	defer writer.Close()
	relay := &outbox.Relay{Pool: pool, Writer: writer, Service: name}

	adm := admin.New(common.AdminAddr, checks...)
	err = lifecycle.Run(ctx, func() { adm.SetDraining(); srv.Health.Shutdown() },
		lifecycle.GRPCServer(srv.Server, grpcAddr, common.ShutdownTTL),
		lifecycle.Component{Name: "admin", Run: adm.Run},
		lifecycle.Component{Name: "outbox-relay", Run: relay.Run},
	)
	if err != nil {
		lifecycle.Fatal("service stopped with error", err)
	}
}
