package notifier

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/pkg/logx"
)

// Group is the Kafka consumer group.
const Group = "notification-service"

// Users resolves contact details (events carry ids, not PII).
type Users interface {
	Contact(ctx context.Context, userID string) (email, firstName string, err error)
}

// Log records sent notifications and answers "already sent?".
type Log interface {
	Sent(ctx context.Context, eventID string) (bool, error)
	Record(ctx context.Context, eventID, eventType, userID, channel string) error
}

// Notifier handles events.
type Notifier struct {
	Users  Users
	Mailer Mailer
	Log    Log
}

// Handle implements kafka.Handler. Delivery is at-least-once: the log is
// checked before sending and written after, so a crash between sending and
// recording can at worst send one duplicate email.
func (n *Notifier) Handle(ctx context.Context, ev events.Envelope) error {
	userID, build, ok := n.render(ev)
	if !ok {
		return nil
	}
	if sent, err := n.Log.Sent(ctx, ev.ID); err != nil || sent {
		return err
	}
	email, first, err := n.Users.Contact(ctx, userID)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			logx.From(ctx).Warn("user not found; skipping notification", "user_id", userID)
			return nil
		}
		return err
	}
	msg, err := build(first)
	if err != nil {
		return err
	}
	msg.To = email
	if err := n.Mailer.Send(ctx, msg); err != nil {
		return fmt.Errorf("send email: %w", err)
	}
	return n.Log.Record(ctx, ev.ID, ev.Type, userID, "email")
}

type builder func(firstName string) (Message, error)

func money(cents int64, currency string) string {
	return fmt.Sprintf("%d.%02d %s", cents/100, cents%100, currency)
}

// render maps an event to its recipient and a message builder.
func (n *Notifier) render(ev events.Envelope) (string, builder, bool) {
	switch ev.Type {
	case events.UserRegistered:
		var d events.UserRegisteredData
		if ev.Decode(&d) != nil {
			return "", nil, false
		}
		return d.UserID, func(first string) (Message, error) {
			return Message{Subject: "Welcome to VENDORA!", Body: fmt.Sprintf(
				"Hi %s,\n\nWelcome to VENDORA! Your account is ready.\n\nThe VENDORA Team", first)}, nil
		}, true
	case events.OrderCreated:
		var d events.OrderCreatedData
		if ev.Decode(&d) != nil {
			return "", nil, false
		}
		return d.UserID, func(first string) (Message, error) {
			return Message{Subject: "Order received #" + d.OrderID, Body: fmt.Sprintf(
				"Hi %s,\n\nWe received your order %s (total %s). Complete payment to confirm it.\n\nThe VENDORA Team",
				first, d.OrderID, money(d.TotalCents, d.Currency))}, nil
		}, true
	case events.OrderConfirmed:
		var d events.OrderConfirmedData
		if ev.Decode(&d) != nil {
			return "", nil, false
		}
		return d.UserID, func(first string) (Message, error) {
			return Message{Subject: "Order confirmed #" + d.OrderID, Body: fmt.Sprintf(
				"Hi %s,\n\nYour order %s is confirmed. We'll let you know when it ships.\n\nThe VENDORA Team", first, d.OrderID)}, nil
		}, true
	case events.OrderCancelled:
		var d events.OrderCancelledData
		if ev.Decode(&d) != nil {
			return "", nil, false
		}
		return d.UserID, func(first string) (Message, error) {
			return Message{Subject: "Order cancelled #" + d.OrderID, Body: fmt.Sprintf(
				"Hi %s,\n\nYour order %s was cancelled (%s).\n\nThe VENDORA Team", first, d.OrderID, d.Reason)}, nil
		}, true
	case events.OrderStatusChanged:
		var d events.OrderStatusChangedData
		if ev.Decode(&d) != nil {
			return "", nil, false
		}
		return d.UserID, func(first string) (Message, error) {
			return Message{Subject: "Order update #" + d.OrderID, Body: fmt.Sprintf(
				"Hi %s,\n\nYour order %s is now %s.\n\nThe VENDORA Team", first, d.OrderID, d.Status)}, nil
		}, true
	case events.PaymentSucceeded:
		var d events.PaymentSucceededData
		if ev.Decode(&d) != nil {
			return "", nil, false
		}
		return d.UserID, func(first string) (Message, error) {
			return Message{Subject: "Payment receipt - VENDORA", Body: fmt.Sprintf(
				"Hi %s,\n\nWe received your payment of %s for order %s.\nPayment ID: %s\n\nThe VENDORA Team",
				first, money(d.AmountCents, d.Currency), d.OrderID, d.PaymentID)}, nil
		}, true
	case events.PaymentFailed:
		var d events.PaymentFailedData
		if ev.Decode(&d) != nil {
			return "", nil, false
		}
		return d.UserID, func(first string) (Message, error) {
			return Message{Subject: "Payment failed - VENDORA", Body: fmt.Sprintf(
				"Hi %s,\n\nYour payment for order %s failed (%s).\n\nThe VENDORA Team", first, d.OrderID, d.Reason)}, nil
		}, true
	case events.PaymentRefunded:
		var d events.PaymentRefundedData
		if ev.Decode(&d) != nil {
			return "", nil, false
		}
		return d.UserID, func(first string) (Message, error) {
			return Message{Subject: "Refund issued - VENDORA", Body: fmt.Sprintf(
				"Hi %s,\n\nWe refunded %s for cancelled order %s.\n\nThe VENDORA Team",
				first, money(d.AmountCents, d.Currency), d.OrderID)}, nil
		}, true
	}
	return "", nil, false
}

// PGLog is the Postgres-backed notification log.
type PGLog struct{ Pool *pgxpool.Pool }

// Sent implements Log.
func (l PGLog) Sent(ctx context.Context, eventID string) (bool, error) {
	var ok bool
	err := l.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notification_log WHERE event_id = $1)`, eventID).Scan(&ok)
	return ok, err
}

// Record implements Log.
func (l PGLog) Record(ctx context.Context, eventID, eventType, userID, channel string) error {
	_, err := l.Pool.Exec(ctx, `
		INSERT INTO notification_log (event_id, event_type, user_id, channel) VALUES ($1, $2, $3, $4)
		ON CONFLICT (event_id) DO NOTHING`, eventID, eventType, userID, channel)
	return err
}
