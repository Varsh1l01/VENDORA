// Command user-service owns accounts, credentials and token issuance.
package main

import (
	"context"
	"time"

	userv1 "github.com/Varsh1l01/VENDORA/gen/vendora/user/v1"
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
	"github.com/Varsh1l01/VENDORA/services/user-service/handler"
	"github.com/Varsh1l01/VENDORA/services/user-service/migrations"
	"github.com/Varsh1l01/VENDORA/services/user-service/repository"
	"github.com/Varsh1l01/VENDORA/services/user-service/service"
)

const name = "user-service"

func main() {
	l := config.New()
	common := l.LoadCommon(name)
	lifecycle.HandleHealthcheckFlag(common.AdminAddr)

	grpcAddr := l.String("GRPC_ADDR", ":50051")
	poolCfg := db.LoadPoolConfig(l, "USER_DB")
	brokers := l.List("KAFKA_BROKERS", nil)
	if len(brokers) == 0 {
		l.Required("KAFKA_BROKERS")
	}
	svcCfg := service.Config{
		AccessTTL:  l.Duration("ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTTL: l.Duration("REFRESH_TOKEN_TTL", 7*24*time.Hour),
		BcryptCost: l.Int("BCRYPT_COST", 12),
	}
	adminEmail := l.String("ADMIN_EMAIL", "")
	adminPassword := l.String("ADMIN_PASSWORD", "")
	logger := logx.New(name, common.LogLevel)
	if err := l.Err(); err != nil {
		lifecycle.Fatal("invalid configuration", err)
	}

	ctx, stop := lifecycle.SignalContext()
	defer stop()

	startCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	pool, err := db.Open(startCtx, poolCfg, "users")
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
	svc := service.New(repository.New(pool), tokens, svcCfg)
	if adminEmail != "" && adminPassword != "" {
		if err := svc.EnsureAdmin(startCtx, adminEmail, adminPassword); err != nil {
			lifecycle.Fatal("bootstrap admin", err)
		}
		logger.Info("admin account ensured", "email", logx.RedactEmail(adminEmail))
	}

	srv := grpcx.NewServer(grpcx.ServerOptions{Tokens: tokens, Policy: handler.Policy})
	userv1.RegisterUserServiceServer(srv.Server, handler.New(svc))

	writer := kafka.NewWriter(brokers)
	defer writer.Close()
	relay := &outbox.Relay{Pool: pool, Writer: writer, Service: name}

	adm := admin.New(common.AdminAddr, admin.Check{Name: "postgres", Fn: pool.Ping})
	err = lifecycle.Run(ctx, func() { adm.SetDraining(); srv.Health.Shutdown() },
		lifecycle.GRPCServer(srv.Server, grpcAddr, common.ShutdownTTL),
		lifecycle.Component{Name: "admin", Run: adm.Run},
		lifecycle.Component{Name: "outbox-relay", Run: relay.Run},
	)
	if err != nil {
		lifecycle.Fatal("service stopped with error", err)
	}
}
