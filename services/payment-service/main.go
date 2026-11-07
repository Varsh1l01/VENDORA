// Command payment-service charges orders through a payment provider.
package main

import (
	"context"
	"fmt"
	"time"

	orderv1 "github.com/Varsh1l01/VENDORA/gen/vendora/order/v1"
	paymentv1 "github.com/Varsh1l01/VENDORA/gen/vendora/payment/v1"
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
	"github.com/Varsh1l01/VENDORA/services/payment-service/consumer"
	"github.com/Varsh1l01/VENDORA/services/payment-service/handler"
	"github.com/Varsh1l01/VENDORA/services/payment-service/migrations"
	"github.com/Varsh1l01/VENDORA/services/payment-service/provider"
	"github.com/Varsh1l01/VENDORA/services/payment-service/repository"
	"github.com/Varsh1l01/VENDORA/services/payment-service/service"
)

const name = "payment-service"

func main() {
	l := config.New()
	common := l.LoadCommon(name)
	lifecycle.HandleHealthcheckFlag(common.AdminAddr)

	grpcAddr := l.String("GRPC_ADDR", ":50055")
	poolCfg := db.LoadPoolConfig(l, "PAYMENT_DB")
	brokers := l.List("KAFKA_BROKERS", nil)
	if len(brokers) == 0 {
		l.Required("KAFKA_BROKERS")
	}
	orderAddr := l.Required("ORDER_SERVICE_ADDR")
	providerName := l.String("PAYMENT_PROVIDER", "fake")
	fakeLatency := l.Duration("FAKE_PROVIDER_LATENCY", 50*time.Millisecond)
	logx.New(name, common.LogLevel)
	if err := l.Err(); err != nil {
		lifecycle.Fatal("invalid configuration", err)
	}
	if providerName != "fake" {
		lifecycle.Fatal("invalid configuration", fmt.Errorf("unsupported PAYMENT_PROVIDER %q (supported: fake)", providerName))
	}

	ctx, stop := lifecycle.SignalContext()
	defer stop()
	startCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	pool, err := db.Open(startCtx, poolCfg, "payments")
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
	orderConn, err := grpcx.Dial(orderAddr, grpcx.ClientOptions{ServiceIdentity: identity, Timeout: 2 * time.Second})
	if err != nil {
		lifecycle.Fatal("dial order-service", err)
	}
	defer orderConn.Close()

	svc := service.New(repository.New(pool), handler.OrderClient{C: orderv1.NewOrderServiceClient(orderConn)},
		provider.NewFake(fakeLatency))
	srv := grpcx.NewServer(grpcx.ServerOptions{Tokens: tokens, Policy: handler.Policy})
	paymentv1.RegisterPaymentServiceServer(srv.Server, handler.New(svc))

	writer := kafka.NewWriter(brokers)
	defer writer.Close()
	relay := &outbox.Relay{Pool: pool, Writer: writer, Service: name}
	cons := kafka.NewConsumer(kafka.ConsumerConfig{
		Brokers: brokers, GroupID: consumer.Group, Topics: []string{events.TopicOrder},
	}, (&consumer.Handler{Svc: svc}).Handle)

	adm := admin.New(common.AdminAddr, admin.Check{Name: "postgres", Fn: pool.Ping})
	err = lifecycle.Run(ctx, func() { adm.SetDraining(); srv.Health.Shutdown() },
		lifecycle.GRPCServer(srv.Server, grpcAddr, common.ShutdownTTL),
		lifecycle.Component{Name: "admin", Run: adm.Run},
		lifecycle.Component{Name: "outbox-relay", Run: relay.Run},
		lifecycle.Component{Name: "order-consumer", Run: cons.Run},
	)
	if err != nil {
		lifecycle.Fatal("service stopped with error", err)
	}
}
