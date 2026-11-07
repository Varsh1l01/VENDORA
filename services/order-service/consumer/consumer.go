// Package consumer advances the checkout saga from payment events.
package consumer

import (
	"context"

	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/services/order-service/service"
)

// Group is the Kafka consumer group and dedupe consumer name.
const Group = "order-service"

// Handler handles payment events.
type Handler struct{ Svc *service.Service }

// Handle implements kafka.Handler.
func (h *Handler) Handle(ctx context.Context, ev events.Envelope) error {
	switch ev.Type {
	case events.PaymentSucceeded:
		var d events.PaymentSucceededData
		if err := ev.Decode(&d); err != nil {
			return err
		}
		return h.Svc.HandlePaymentSucceeded(ctx, Group, ev.ID, d.OrderID)
	case events.PaymentFailed:
		var d events.PaymentFailedData
		if err := ev.Decode(&d); err != nil {
			return err
		}
		return h.Svc.HandlePaymentFailed(ctx, Group, ev.ID, d.OrderID, d.Reason)
	}
	return nil
}
