// Package consumer reacts to catalog and order events.
package consumer

import (
	"context"

	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/pkg/logx"
	"github.com/Varsh1l01/VENDORA/services/inventory-service/service"
)

// Group is the Kafka consumer group (and processed_events consumer name).
const Group = "inventory-service"

// Provisioner creates stock rows exactly once per product.created event.
type Provisioner interface {
	Provision(ctx context.Context, consumer, eventID, productID string, initial int32) error
}

// Handler routes events to inventory operations. All operations are
// idempotent, so redelivered events are harmless.
type Handler struct {
	Svc   *service.Service
	Store Provisioner
}

// Handle implements kafka.Handler.
func (h *Handler) Handle(ctx context.Context, ev events.Envelope) error {
	switch ev.Type {
	case events.ProductCreated:
		var d events.ProductCreatedData
		if err := ev.Decode(&d); err != nil {
			return err
		}
		return h.Store.Provision(ctx, Group, ev.ID, d.ProductID, d.InitialStock)
	case events.OrderCancelled:
		var d events.OrderCancelledData
		if err := ev.Decode(&d); err != nil {
			return err
		}
		logx.From(ctx).Info("releasing stock for cancelled order", "order_id", d.OrderID)
		return h.Svc.Release(ctx, d.OrderID)
	case events.OrderConfirmed:
		var d events.OrderConfirmedData
		if err := ev.Decode(&d); err != nil {
			return err
		}
		return h.Svc.Commit(ctx, d.OrderID)
	}
	return nil // not interested
}
