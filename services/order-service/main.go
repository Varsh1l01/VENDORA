// Command order-service owns orders and orchestrates the checkout saga.
package main

import (
	"context"
	"time"

	"google.golang.org/grpc"

	cartv1 "github.com/Varsh1l01/VENDORA/gen/vendora/cart/v1"
	inventoryv1 "github.com/Varsh1l01/VENDORA/gen/vendora/inventory/v1"
	orderv1 "github.com/Varsh1l01/VENDORA/gen/vendora/order/v1"
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
	"github.com/Varsh1l01/VENDORA/services/order-service/consumer"
	"github.com/Varsh1l01/VENDORA/services/order-service/handler"
	"github.com/Varsh1l01/VENDORA/services/order-service/migrations"
	"github.com/Varsh1l01/VENDORA/services/order-service/repository"
	"github.com/Varsh1l01/VENDORA/services/order-service/service"
)

const name = "order-service"

func main() {
	l := config.New()
	common := l.LoadCommon(name)
	lifecycle.HandleHealthcheckFlag(common.AdminAddr)

	grpcAddr := l.String("GRPC_ADDR", ":50054")
	poolCfg := db.LoadPoolConfig(l, "ORDER_DB")
	brokers := l.List("KAFKA_BROKERS", nil)
	if len(brokers) == 0 {
		l.Required("KAFKA_BROKERS")
	}
	cartAddr := l.Required("CART_SERVICE_ADDR")
	productAddr := l.Required("PRODUCT_SERVICE_ADDR")
	inventoryAddr := l.Required("INVENTORY_SERVICE_ADDR")
	svcCfg := service.Config{
		PaymentTimeout: l.Duration("ORDER_PAYMENT_TIMEOUT", 15*time.Minute),
		PendingTimeout: l.Duration("ORDER_PENDING_TIMEOUT", 2*time.Minute),
	}
	sweepEvery := l.Duration("ORDER_SWEEP_INTERVAL", 30*time.Second)
	logx.New(name, common.LogLevel)
	if err := l.Err(); err != nil {
		lifecycle.Fatal("invalid configuration", err)
	}

	ctx, stop := lifecycle.SignalContext()
	defer stop()
	startCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	pool, err := db.Open(startCtx, poolCfg, "orders")
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

	tokens := auth.NewTokens(common.JWTSecret, common.JWTIssuer, common.JWTAudience)
	identity := grpcx.NewServiceIdentity(tokens, name)
	dial := func(addr string, timeout time.Duration) *grpc.ClientConn {
		c, err := grpcx.Dial(addr, grpcx.ClientOptions{ServiceIdentity: identity, Timeout: timeout})
		if err != nil {
			lifecycle.Fatal("dial "+addr, err)
		}
		return c
	}
	cartConn := dial(cartAddr, 2*time.Second)
	defer cartConn.Close()
	productConn := dial(productAddr, 2*time.Second)
	defer productConn.Close()
	inventoryConn := dial(inventoryAddr, 3*time.Second)
	defer inventoryConn.Close()

	svc := service.New(repository.New(pool),
		handler.CartClient{C: cartv1.NewCartServiceClient(cartConn)},
		handler.CatalogClient{C: productv1.NewProductServiceClient(productConn)},
		handler.InventoryClient{C: inventoryv1.NewInventoryServiceClient(inventoryConn)},
		svcCfg)

	srv := grpcx.NewServer(grpcx.ServerOptions{Tokens: tokens, Policy: handler.Policy})
	orderv1.RegisterOrderServiceServer(srv.Server, handler.New(svc))

	writer := kafka.NewWriter(brokers)
	defer writer.Close()
	relay := &outbox.Relay{Pool: pool, Writer: writer, Service: name}
	cons := kafka.NewConsumer(kafka.ConsumerConfig{
		Brokers: brokers, GroupID: consumer.Group, Topics: []string{events.TopicPayment},
	}, (&consumer.Handler{Svc: svc}).Handle)

	adm := admin.New(common.AdminAddr, admin.Check{Name: "postgres", Fn: pool.Ping})
	err = lifecycle.Run(ctx, func() { adm.SetDraining(); srv.Health.Shutdown() },
		lifecycle.GRPCServer(srv.Server, grpcAddr, common.ShutdownTTL),
		lifecycle.Component{Name: "admin", Run: adm.Run},
		lifecycle.Component{Name: "outbox-relay", Run: relay.Run},
		lifecycle.Component{Name: "payment-consumer", Run: cons.Run},
		lifecycle.Component{Name: "saga-sweeper", Run: func(ctx context.Context) error { return svc.RunSweeper(ctx, sweepEvery) }},
	)
	if err != nil {
		lifecycle.Fatal("service stopped with error", err)
	}
}
