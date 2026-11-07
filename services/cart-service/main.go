// Command cart-service manages shopping carts in Redis.
package main

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	cartv1 "github.com/Varsh1l01/VENDORA/gen/vendora/cart/v1"
	productv1 "github.com/Varsh1l01/VENDORA/gen/vendora/product/v1"
	"github.com/Varsh1l01/VENDORA/pkg/admin"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/config"
	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/pkg/grpcx"
	"github.com/Varsh1l01/VENDORA/pkg/kafka"
	"github.com/Varsh1l01/VENDORA/pkg/lifecycle"
	"github.com/Varsh1l01/VENDORA/pkg/logx"
	"github.com/Varsh1l01/VENDORA/services/cart-service/consumer"
	"github.com/Varsh1l01/VENDORA/services/cart-service/handler"
	"github.com/Varsh1l01/VENDORA/services/cart-service/repository"
	"github.com/Varsh1l01/VENDORA/services/cart-service/service"
)

const name = "cart-service"

func main() {
	l := config.New()
	common := l.LoadCommon(name)
	lifecycle.HandleHealthcheckFlag(common.AdminAddr)

	grpcAddr := l.String("GRPC_ADDR", ":50053")
	redisAddr := l.Required("REDIS_ADDR")
	redisPassword := l.String("REDIS_PASSWORD", "")
	productAddr := l.Required("PRODUCT_SERVICE_ADDR")
	brokers := l.List("KAFKA_BROKERS", nil)
	if len(brokers) == 0 {
		l.Required("KAFKA_BROKERS")
	}
	logx.New(name, common.LogLevel)
	if err := l.Err(); err != nil {
		lifecycle.Fatal("invalid configuration", err)
	}

	ctx, stop := lifecycle.SignalContext()
	defer stop()

	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr, Password: redisPassword, DB: 0,
		DialTimeout: 2 * time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 50,
	})
	defer rdb.Close()
	store := repository.New(rdb)

	tokens := auth.NewTokens(common.JWTSecret, common.JWTIssuer, common.JWTAudience)
	identity := grpcx.NewServiceIdentity(tokens, name)
	productConn, err := grpcx.Dial(productAddr, grpcx.ClientOptions{ServiceIdentity: identity, Timeout: 2 * time.Second})
	if err != nil {
		lifecycle.Fatal("dial product-service", err)
	}
	defer productConn.Close()

	svc := service.New(store, handler.Catalog{Client: productv1.NewProductServiceClient(productConn)})
	srv := grpcx.NewServer(grpcx.ServerOptions{Tokens: tokens, Policy: handler.Policy})
	cartv1.RegisterCartServiceServer(srv.Server, handler.New(svc))

	startCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := kafka.EnsureTopics(startCtx, brokers, 3, 1, events.AllTopics()...); err != nil {
		lifecycle.Fatal("ensure topics", err)
	}
	cons := kafka.NewConsumer(kafka.ConsumerConfig{
		Brokers: brokers, GroupID: consumer.Group, Topics: []string{events.TopicOrder},
	}, (&consumer.Handler{Store: store}).Handle)

	adm := admin.New(common.AdminAddr, admin.Check{Name: "redis", Fn: store.Ping})
	err = lifecycle.Run(ctx, func() { adm.SetDraining(); srv.Health.Shutdown() },
		lifecycle.GRPCServer(srv.Server, grpcAddr, common.ShutdownTTL),
		lifecycle.Component{Name: "admin", Run: adm.Run},
		lifecycle.Component{Name: "consumer", Run: cons.Run},
	)
	if err != nil {
		lifecycle.Fatal("service stopped with error", err)
	}
}
