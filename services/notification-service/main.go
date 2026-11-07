// Command notification-service emails customers about account, order and
// payment events.
package main

import (
	"context"
	"time"

	userv1 "github.com/Varsh1l01/VENDORA/gen/vendora/user/v1"
	"github.com/Varsh1l01/VENDORA/pkg/admin"
	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/config"
	"github.com/Varsh1l01/VENDORA/pkg/db"
	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/pkg/grpcx"
	"github.com/Varsh1l01/VENDORA/pkg/kafka"
	"github.com/Varsh1l01/VENDORA/pkg/lifecycle"
	"github.com/Varsh1l01/VENDORA/pkg/logx"
	"github.com/Varsh1l01/VENDORA/services/notification-service/migrations"
	"github.com/Varsh1l01/VENDORA/services/notification-service/notifier"
)

const name = "notification-service"

type userClient struct{ c userv1.UserServiceClient }

func (u userClient) Contact(ctx context.Context, id string) (string, string, error) {
	resp, err := u.c.GetUser(ctx, &userv1.GetUserRequest{Id: id})
	if err != nil {
		return "", "", apperr.FromStatus(err, "user.GetUser")
	}
	return resp.GetUser().GetEmail(), resp.GetUser().GetFirstName(), nil
}

func main() {
	l := config.New()
	common := l.LoadCommon(name)
	lifecycle.HandleHealthcheckFlag(common.AdminAddr)

	poolCfg := db.LoadPoolConfig(l, "NOTIFICATION_DB")
	brokers := l.List("KAFKA_BROKERS", nil)
	if len(brokers) == 0 {
		l.Required("KAFKA_BROKERS")
	}
	userAddr := l.Required("USER_SERVICE_ADDR")
	smtpEnabled := l.Bool("SMTP_ENABLED", false)
	var mailer notifier.Mailer = notifier.LogMailer{}
	if smtpEnabled {
		mailer = notifier.SMTPMailer{
			Host: l.Required("SMTP_HOST"), Port: l.String("SMTP_PORT", "587"),
			Username: l.String("SMTP_USERNAME", ""), Password: l.String("SMTP_PASSWORD", ""),
			From: l.Required("SMTP_FROM"),
		}
	}
	logx.New(name, common.LogLevel)
	if err := l.Err(); err != nil {
		lifecycle.Fatal("invalid configuration", err)
	}

	ctx, stop := lifecycle.SignalContext()
	defer stop()
	startCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	pool, err := db.Open(startCtx, poolCfg, "notifications")
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
	userConn, err := grpcx.Dial(userAddr, grpcx.ClientOptions{ServiceIdentity: grpcx.NewServiceIdentity(tokens, name)})
	if err != nil {
		lifecycle.Fatal("dial user-service", err)
	}
	defer userConn.Close()

	n := &notifier.Notifier{
		Users:  userClient{c: userv1.NewUserServiceClient(userConn)},
		Mailer: mailer,
		Log:    notifier.PGLog{Pool: pool},
	}
	cons := kafka.NewConsumer(kafka.ConsumerConfig{
		Brokers: brokers, GroupID: notifier.Group,
		Topics: []string{events.TopicUser, events.TopicOrder, events.TopicPayment},
	}, n.Handle)

	adm := admin.New(common.AdminAddr, admin.Check{Name: "postgres", Fn: pool.Ping})
	err = lifecycle.Run(ctx, adm.SetDraining,
		lifecycle.Component{Name: "admin", Run: adm.Run},
		lifecycle.Component{Name: "consumer", Run: cons.Run},
	)
	if err != nil {
		lifecycle.Fatal("service stopped with error", err)
	}
}
