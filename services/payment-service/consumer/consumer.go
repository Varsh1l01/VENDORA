// Package consumer refunds charges for orders cancelled after payment.
package consumer

import (
	"context"

	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/services/payment-service/service"
)

// Group is the Kafka consumer group.
const Group = "payment-service"

// Handler handles order events.
type Handler struct{ Svc *service.Service }

// Handle implements kafka.Handler.
func (h *Handler) Handle(ctx context.Context, ev events.Envelope) error {
	if ev.Type != events.OrderCancelled {
		return nil
	}
	var d events.OrderCancelledData
	if err := ev.Decode(&d); err != nil {
		return err
	}
	return h.Svc.HandleOrderCancelled(ctx, Group, ev.ID, d.OrderID)
}
