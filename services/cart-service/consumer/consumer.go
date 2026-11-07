// Package consumer removes checked-out lines from carts.
package consumer

import (
	"context"

	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/services/cart-service/repository"
)

// Group is the Kafka consumer group.
const Group = "cart-service"

// Subtractor removes ordered quantities exactly once per event.
type Subtractor interface {
	SubtractOrdered(ctx context.Context, eventID, userID string, lines []repository.Line) error
}

// Handler handles order events.
type Handler struct{ Store Subtractor }

// Handle implements kafka.Handler.
func (h *Handler) Handle(ctx context.Context, ev events.Envelope) error {
	if ev.Type != events.OrderCreated {
		return nil
	}
	var d events.OrderCreatedData
	if err := ev.Decode(&d); err != nil {
		return err
	}
	lines := make([]repository.Line, 0, len(d.Lines))
	for _, l := range d.Lines {
		lines = append(lines, repository.Line{ProductID: l.ProductID, Quantity: l.Quantity})
	}
	return h.Store.SubtractOrdered(ctx, ev.ID, d.UserID, lines)
}
