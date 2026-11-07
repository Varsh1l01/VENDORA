// Command inventory-service owns stock levels and reservations.
package main

import (
	"context"
	"time"

	inventoryv1 "github.com/Varsh1l01/VENDORA/gen/vendora/inventory/v1"
	"github.com/Varsh1l01/VENDORA/pkg/admin"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/config"
	"github.com/Varsh1l01/VENDORA/pkg/db"
	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/pkg/grpcx"
	"github.com/Varsh1l01/VENDORA/pkg/kafka"
	"github.com/Varsh1l01/VENDORA/pkg/lifecycle"
	"github.com/Varsh1l01/VENDORA/pkg/logx"
	"github.com/Varsh1l01/VENDORA/services/inventory-service/consumer"
	"github.com/Varsh1l01/VENDORA/services/inventory-service/handler"
	"github.com/Varsh1l01/VENDORA/services/inventory-service/migrations"
	"github.com/Varsh1l01/VENDORA/services/inventory-service/repository"
	"github.com/Varsh1l01/VENDORA/services/inventory-service/service"
)

const name = "inventory-service"

func main() {
	l := config.New()
	common := l.LoadCommon(name)
	lifecycle.HandleHealthcheckFlag(common.AdminAddr)

	grpcAddr := l.String("GRPC_ADDR", ":50056")
	poolCfg := db.LoadPoolConfig(l, "INVENTORY_DB")
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
	startCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	pool, err := db.Open(startCtx, poolCfg, "inventory")
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
	store := repository.New(pool)
	svc := service.New(store)
	srv := grpcx.NewServer(grpcx.ServerOptions{Tokens: tokens, Policy: handler.Policy})
	inventoryv1.RegisterInventoryServiceServer(srv.Server, handler.New(svc))

	cons := kafka.NewConsumer(kafka.ConsumerConfig{
		Brokers: brokers, GroupID: consumer.Group,
		Topics: []string{events.TopicProduct, events.TopicOrder},
	}, (&consumer.Handler{Svc: svc, Store: store}).Handle)

	adm := admin.New(common.AdminAddr, admin.Check{Name: "postgres", Fn: pool.Ping})
	err = lifecycle.Run(ctx, func() { adm.SetDraining(); srv.Health.Shutdown() },
		lifecycle.GRPCServer(srv.Server, grpcAddr, common.ShutdownTTL),
		lifecycle.Component{Name: "admin", Run: adm.Run},
		lifecycle.Component{Name: "consumer", Run: cons.Run},
	)
	if err != nil {
		lifecycle.Fatal("service stopped with error", err)
	}
}
