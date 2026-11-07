// Package events is the shared contract for asynchronous messages.
//
// Every message on Kafka is an Envelope (JSON). Payloads deliberately avoid
// PII: consumers resolve contact details from the owning service.
package events

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Topics. One topic per aggregate; messages are keyed by aggregate id so all
// events for one entity are ordered within a partition.
const (
	TopicUser    = "vendora.user.events"
	TopicProduct = "vendora.product.events"
	TopicOrder   = "vendora.order.events"
	TopicPayment = "vendora.payment.events"
)

// DLQ returns the dead-letter topic for topic.
func DLQ(topic string) string { return topic + ".dlq" }

// AllTopics lists every topic including dead-letter topics.
func AllTopics() []string {
	base := []string{TopicUser, TopicProduct, TopicOrder, TopicPayment}
	out := append([]string{}, base...)
	for _, t := range base {
		out = append(out, DLQ(t))
	}
	return out
}

// Event types.
const (
	UserRegistered     = "user.registered"
	ProductCreated     = "product.created"
	OrderCreated       = "order.created"
	OrderConfirmed     = "order.confirmed"
	OrderCancelled     = "order.cancelled"
	OrderStatusChanged = "order.status_changed"
	PaymentSucceeded   = "payment.succeeded"
	PaymentFailed      = "payment.failed"
	PaymentRefunded    = "payment.refunded"
)

// Envelope wraps every event.
type Envelope struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Version     int             `json:"version"`
	AggregateID string          `json:"aggregate_id"`
	OccurredAt  time.Time       `json:"occurred_at"`
	RequestID   string          `json:"request_id,omitempty"`
	Data        json.RawMessage `json:"data"`
}

// New builds an envelope with a fresh id for payload.
func New(eventType, aggregateID, requestID string, payload any) (Envelope, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal %s payload: %w", eventType, err)
	}
	return Envelope{
		ID:          uuid.NewString(),
		Type:        eventType,
		Version:     1,
		AggregateID: aggregateID,
		OccurredAt:  time.Now().UTC(),
		RequestID:   requestID,
		Data:        data,
	}, nil
}

// Decode unmarshals the envelope payload into v.
func (e Envelope) Decode(v any) error {
	if err := json.Unmarshal(e.Data, v); err != nil {
		return fmt.Errorf("decode %s payload: %w", e.Type, err)
	}
	return nil
}

// Payloads.

type UserRegisteredData struct {
	UserID string `json:"user_id"`
}

type ProductCreatedData struct {
	ProductID    string `json:"product_id"`
	InitialStock int32  `json:"initial_stock"`
}

type OrderLine struct {
	ProductID string `json:"product_id"`
	Quantity  int32  `json:"quantity"`
}

type OrderCreatedData struct {
	OrderID    string      `json:"order_id"`
	UserID     string      `json:"user_id"`
	TotalCents int64       `json:"total_cents"`
	Currency   string      `json:"currency"`
	Lines      []OrderLine `json:"lines"`
}

type OrderConfirmedData struct {
	OrderID string `json:"order_id"`
	UserID  string `json:"user_id"`
}

type OrderCancelledData struct {
	OrderID string `json:"order_id"`
	UserID  string `json:"user_id"`
	Reason  string `json:"reason"`
}

type OrderStatusChangedData struct {
	OrderID string `json:"order_id"`
	UserID  string `json:"user_id"`
	Status  string `json:"status"`
}

type PaymentSucceededData struct {
	PaymentID   string `json:"payment_id"`
	OrderID     string `json:"order_id"`
	UserID      string `json:"user_id"`
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
}

type PaymentFailedData struct {
	PaymentID string `json:"payment_id"`
	OrderID   string `json:"order_id"`
	UserID    string `json:"user_id"`
	Reason    string `json:"reason"`
}

type PaymentRefundedData struct {
	PaymentID   string `json:"payment_id"`
	OrderID     string `json:"order_id"`
	UserID      string `json:"user_id"`
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
}
